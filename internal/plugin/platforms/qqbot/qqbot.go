// Package qqbot implements the QQ official bot platform (v2 API): OAuth-style
// access tokens, REST message sending and the WebSocket gateway for inbound
// events.
package qqbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/covoyage/covo-agent/internal/safego"
)

const (
	PlatformName = "qqbot"

	defaultAuthURL    = "https://bots.qq.com/app/getAppAccessToken"
	defaultAPIBase    = "https://api.sgroup.qq.com"
	sandboxAPIBase    = "https://sandbox.api.sgroup.qq.com"
	defaultGatewayURL = "wss://api.sgroup.qq.com/websocket/"

	maxChunkRunes = 4000
	maxSendRetry  = 3

	// passive-reply message id cache TTL (mirrors the reference adapter adapter.py _last_msg_id).
	msgIDTTL = 5 * time.Minute
)

// gateway opcodes (QQ official bot websocket protocol).
const (
	opDispatch       = 0
	opHeartbeat      = 1
	opIdentify       = 2
	opResume         = 6
	opReconnect      = 7
	opInvalidSession = 9
	opHello          = 10
	opHeartbeatACK   = 11
)

// intents: C2C_GROUP_AT_MESSAGES (1<<25) | PUBLIC_GUILD_MESSAGES (1<<30) |
// DIRECT_MESSAGE (1<<12), matching the reference adapter adapter.py _send_identify.
const intents = 1<<25 | 1<<30 | 1<<12

// inbound event types we care about.
const (
	eventC2CMessage = "C2C_MESSAGE_CREATE"
	eventGroupAtMsg = "GROUP_AT_MESSAGE_CREATE"
	eventGuildAtMsg = "AT_MESSAGE_CREATE"
)

type Adapter struct {
	appID      string
	appSecret  string
	botToken   string // optional: pre-issued access token (env QQBOT_TOKEN)
	httpClient *http.Client
	logger     *slog.Logger

	// injectable endpoints (overridden in tests).
	authURL    string
	apiBase    string
	gatewayURL string

	mu          sync.Mutex
	running     bool
	cancel      context.CancelFunc
	onMessage   func(plugin.IncomingMessage)
	accessToken string
	tokenExpiry time.Time
	tokenMu     sync.Mutex
	msgSeq      uint32
	endpointKnd sync.Map // channelID (openid / guild channel id) -> "user" | "group" | "guild"

	// lastMsgID caches the most recent inbound message ID per channel
	// (TTL msgIDTTL). QQ group/C2C bots may only reply passively: every
	// outbound message must carry the msg_id of a received message.
	lastMsgID sync.Map // channelID -> msgIDEntry

	// websocket session state, kept across reconnects for op 6 Resume.
	wsMu      sync.Mutex
	sessionID string
	lastSeq   int64
	hasSeq    bool
}

// msgIDEntry is a TTL-wrapped cached inbound message id.
type msgIDEntry struct {
	id      string
	expires time.Time
}

// rememberMsgID stores the inbound message id for a channel (used to build
// passive replies).
func (a *Adapter) rememberMsgID(channelID, msgID string) {
	if channelID == "" || msgID == "" {
		return
	}
	a.lastMsgID.Store(channelID, msgIDEntry{id: msgID, expires: time.Now().Add(msgIDTTL)})
}

// lastMsgIDFor returns the cached inbound message id for a channel, or "".
func (a *Adapter) lastMsgIDFor(channelID string) string {
	v, ok := a.lastMsgID.Load(channelID)
	if !ok {
		return ""
	}
	entry, ok := v.(msgIDEntry)
	if !ok {
		return ""
	}
	if time.Now().After(entry.expires) {
		a.lastMsgID.Delete(channelID)
		return ""
	}
	return entry.id
}

// websocketSession returns the current resume session state.
func (a *Adapter) websocketSession() (sessionID string, seq int64, hasSeq bool) {
	a.wsMu.Lock()
	defer a.wsMu.Unlock()
	return a.sessionID, a.lastSeq, a.hasSeq && a.sessionID != ""
}

// setWebsocketSession records the READY session id for later Resume.
func (a *Adapter) setWebsocketSession(sessionID string) {
	a.wsMu.Lock()
	defer a.wsMu.Unlock()
	a.sessionID = sessionID
}

// clearWebsocketSession drops the session so the next Hello re-Identifies.
func (a *Adapter) clearWebsocketSession() {
	a.wsMu.Lock()
	defer a.wsMu.Unlock()
	a.sessionID = ""
	a.hasSeq = false
	a.lastSeq = 0
}

// trackSeq updates the highest dispatch sequence number seen.
func (a *Adapter) trackSeq(s int64) {
	a.wsMu.Lock()
	defer a.wsMu.Unlock()
	if !a.hasSeq || s > a.lastSeq {
		a.lastSeq = s
		a.hasSeq = true
	}
}

// currentSeq returns the latest dispatch sequence number (for heartbeats).
func (a *Adapter) currentSeq() int64 {
	a.wsMu.Lock()
	defer a.wsMu.Unlock()
	return a.lastSeq
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	botToken := strings.TrimSpace(os.Getenv("QQBOT_TOKEN"))
	if botToken == "" {
		botToken = strings.TrimSpace(os.Getenv("QQBOT_BOT_TOKEN"))
	}

	apiBase := defaultAPIBase
	if isSandbox() {
		apiBase = sandboxAPIBase
	}

	return &Adapter{
		appID:      strings.TrimSpace(os.Getenv("QQBOT_APP_ID")),
		appSecret:  strings.TrimSpace(os.Getenv("QQBOT_APP_SECRET")),
		botToken:   botToken,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     logger,
		authURL:    defaultAuthURL,
		apiBase:    apiBase,
		gatewayURL: defaultGatewayURL,
	}
}

func isSandbox() bool {
	v := strings.TrimSpace(os.Getenv("QQBOT_SANDBOX"))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

func (a *Adapter) Name() string                      { return PlatformName }
func (a *Adapter) GetName() string                   { return PlatformName }
func (a *Adapter) GetID() string                     { return PlatformName }
func (a *Adapter) Category() plugin.Category         { return plugin.CategoryPlatform }
func (a *Adapter) GetCategory() plugin.Category      { return plugin.CategoryPlatform }
func (a *Adapter) ID() string                        { return PlatformName }
func (a *Adapter) Platform() plugin.PlatformProvider { return a }

func (a *Adapter) Validate() error {
	if a.appID == "" || a.appSecret == "" {
		return fmt.Errorf("QQBOT_APP_ID and QQBOT_APP_SECRET must be set in environment")
	}
	return nil
}

func (a *Adapter) OnMessage(callback func(plugin.IncomingMessage)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onMessage = callback
}

func (a *Adapter) Start(ctx context.Context) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("qqbot: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("qqbot: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	safego.SafeGo(func() { a.gatewayLoop(ctx) }, a.logger)
	a.logger.Info("qqbot: started", "api_base", a.apiBase)
	return nil
}

func (a *Adapter) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.running {
		return nil
	}
	if a.cancel != nil {
		a.cancel()
	}
	a.running = false
	a.logger.Info("qqbot: stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if strings.TrimSpace(channelID) == "" {
		return fmt.Errorf("qqbot: empty channel ID")
	}

	token, err := a.getAccessToken(ctx)
	if err != nil {
		return err
	}

	kind := "user"
	if v, ok := a.endpointKnd.Load(channelID); ok {
		if s, ok := v.(string); ok {
			kind = s
		}
	}

	// Passive replies must carry the last inbound message id: prefer an
	// explicit ReplyTo, then fall back to the cached id from dispatch.
	msgID := msg.ReplyTo
	if msgID == "" {
		msgID = a.lastMsgIDFor(channelID)
	}

	chunks := splitLongMessage(msg.Text)
	for i, chunk := range chunks {
		if err := a.sendChunk(ctx, token, channelID, kind, chunk, msgID, i, len(chunks)); err != nil {
			return fmt.Errorf("qqbot: send chunk %d/%d: %w", i+1, len(chunks), err)
		}
	}
	return nil
}

func (a *Adapter) sendChunk(ctx context.Context, token, channelID, kind, text, msgID string, seqIdx, totalChunks int) error {
	body := map[string]any{
		"content":  text,
		"msg_type": 0,
	}
	if msgID != "" {
		body["msg_id"] = msgID
		body["msg_seq"] = a.nextMsgSeq()
	}

	var path string
	switch kind {
	case "group":
		// Group events: /v2/groups/{group_openid}/messages.
		path = "/v2/groups/" + channelID + "/messages"
	case "guild":
		// Guild (channel) events: /channels/{channel_id}/messages
		// (the reference adapter adapter.py _send_guild_text).
		path = "/channels/" + channelID + "/messages"
	default:
		// C2C events: /v2/users/{user_openid}/messages.
		path = "/v2/users/" + channelID + "/messages"
	}

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := a.restCall(ctx, http.MethodPost, path, token, body, &resp); err != nil {
		return err
	}
	return nil
}

func (a *Adapter) nextMsgSeq() uint32 {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()
	a.msgSeq++
	return a.msgSeq
}

// restCall performs a REST call against the QQ API and decodes the response.
// A non-zero errcode/code field is surfaced as an error.
func (a *Adapter) restCall(ctx context.Context, method, path, token string, body any, result any) error {
	var lastErr error
	for attempt := 0; attempt < maxSendRetry; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}

		var reqBody io.Reader
		if body != nil {
			data, err := json.Marshal(body)
			if err != nil {
				return fmt.Errorf("qqbot: marshal body: %w", err)
			}
			reqBody = bytes.NewReader(data)
		}

		req, err := http.NewRequestWithContext(ctx, method, a.apiBase+path, reqBody)
		if err != nil {
			return fmt.Errorf("qqbot: create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "QQBot "+token)

		resp, err := a.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("qqbot: %s %s: %w", method, path, err)
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("qqbot: read response: %w", err)
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			// The API reports business errors both via HTTP status and an
			// errcode/code JSON field; keep the body for diagnostics.
			var errResp struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}
			_ = json.Unmarshal(respBody, &errResp)
			lastErr = fmt.Errorf("qqbot: %s %s failed (status %d, code %d): %s", method, path, resp.StatusCode, errResp.Code, strings.TrimSpace(string(respBody)))
			continue
		}

		if len(bytes.TrimSpace(respBody)) == 0 {
			return nil
		}

		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("qqbot: unmarshal response: %w", err)
		}

		var errResp struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(respBody, &errResp)
		if errResp.Code != 0 {
			return fmt.Errorf("qqbot: api error code %d: %s", errResp.Code, errResp.Message)
		}
		return nil
	}
	return lastErr
}

// getAccessToken returns a cached access token, requesting a new one from the
// auth endpoint when missing or close to expiry.
func (a *Adapter) getAccessToken(ctx context.Context) (string, error) {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()

	if a.botToken != "" {
		return a.botToken, nil
	}

	if a.accessToken != "" && time.Now().Before(a.tokenExpiry) {
		return a.accessToken, nil
	}
	return a.refreshAccessToken(ctx)
}

// refreshAccessToken must be called with tokenMu held.
func (a *Adapter) refreshAccessToken(ctx context.Context) (string, error) {
	payload, _ := json.Marshal(map[string]string{
		"appId":        a.appID,
		"clientSecret": a.appSecret,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.authURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("qqbot: create auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("qqbot: auth request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("qqbot: read auth response: %w", err)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   string `json:"expires_in"`
		Code        int    `json:"code"`
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("qqbot: decode auth response: %w (body: %s)", err, strings.TrimSpace(string(respBody)))
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("qqbot: auth failed (code %d): %s", result.Code, result.Message)
	}

	expiresIn, err := time.ParseDuration(result.ExpiresIn + "s")
	if err != nil || expiresIn <= 0 {
		expiresIn = 2 * time.Hour
	}
	// Refresh one minute early to avoid using an expiring token.
	a.accessToken = result.AccessToken
	a.tokenExpiry = time.Now().Add(expiresIn - time.Minute)

	a.logger.Info("qqbot: access token refreshed", "expires_in", result.ExpiresIn)
	return a.accessToken, nil
}

func (a *Adapter) gatewayLoop(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := a.connectGateway(ctx); err != nil && ctx.Err() == nil {
			a.logger.Warn("qqbot: gateway connection lost, reconnecting", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (a *Adapter) connectGateway(ctx context.Context) error {
	gwURL, err := a.resolveGatewayURL(ctx)
	if err != nil {
		return err
	}
	return a.runGatewaySession(ctx, gwURL)
}

// resolveGatewayURL fetches the websocket gateway URL from the REST API.
func (a *Adapter) resolveGatewayURL(ctx context.Context) (string, error) {
	token, err := a.getAccessToken(ctx)
	if err != nil {
		return "", err
	}
	var result struct {
		URL string `json:"url"`
	}
	if err := a.restCall(ctx, http.MethodGet, "/gateway", token, nil, &result); err != nil {
		return "", err
	}
	if result.URL == "" {
		return "", fmt.Errorf("qqbot: gateway URL is empty")
	}
	return result.URL, nil
}

func (a *Adapter) dispatch(ctx context.Context, eventType string, data json.RawMessage) {
	switch eventType {
	case eventC2CMessage:
	case eventGroupAtMsg:
	case eventGuildAtMsg:
	default:
		return
	}

	var payload struct {
		ID        string `json:"id"` // inbound message id (required for passive replies)
		Content   string `json:"content"`
		ChannelID string `json:"channel_id"` // guild events: channel id
		Author    struct {
			ID         string `json:"id"`
			UserOpenID string `json:"user_openid"`
		} `json:"author"`
		GroupOpenID string `json:"group_openid"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		a.logger.Warn("qqbot: decode dispatch payload failed", "error", err)
		return
	}
	if payload.Content == "" {
		return
	}

	channelID := payload.Author.UserOpenID
	kind := "user"
	if eventType == eventGroupAtMsg {
		channelID = payload.GroupOpenID
		kind = "group"
	} else if eventType == eventGuildAtMsg {
		// Guild events carry a channel id and a native author.id (no openid);
		// replies go through the /channels/{id}/messages endpoint.
		channelID = payload.ChannelID
		kind = "guild"
	}
	if channelID == "" {
		channelID = payload.Author.ID
	}
	if channelID == "" {
		return
	}
	a.endpointKnd.Store(channelID, kind)
	a.rememberMsgID(channelID, payload.ID)

	userID := payload.Author.UserOpenID
	if userID == "" {
		userID = payload.Author.ID
	}

	incoming := plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: channelID,
		UserID:    userID,
		UserName:  userID,
		Text:      payload.Content,
		Timestamp: time.Now(),
		Raw:       json.RawMessage(data),
	}

	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()
	if callback != nil {
		callback(incoming)
	}
}

func splitLongMessage(text string) []string {
	if utf8.RuneCountInString(text) <= maxChunkRunes {
		return []string{text}
	}

	var chunks []string
	lines := strings.Split(text, "\n")
	var current []rune

	for _, line := range lines {
		// Hard-split lines that alone exceed the rune limit, on rune
		// boundaries so multi-byte UTF-8 characters are never truncated.
		runes := []rune(line)
		for len(runes) > maxChunkRunes {
			if len(current) > 0 {
				chunks = append(chunks, string(current))
				current = nil
			}
			chunks = append(chunks, string(runes[:maxChunkRunes]))
			runes = runes[maxChunkRunes:]
		}
		line = string(runes)
		if len(current)+len(runes)+1 > maxChunkRunes && len(current) > 0 {
			chunks = append(chunks, string(current))
			current = nil
		}
		if len(current) > 0 {
			current = append(current, '\n')
		}
		current = append(current, runes...)
	}
	if len(current) > 0 {
		chunks = append(chunks, string(current))
	}
	return chunks
}
