// Package weixin implements the WeChat personal-account (个人号) bot platform
// over Tencent's iLink Bot API.
//
// 协议对齐 hermes-agent gateway/platforms/weixin.py（iLink Bot API）：
// inbound is a long-poll loop against ilink/bot/getupdates with an opaque
// cursor (get_updates_buf); outbound is ilink/bot/sendmessage which must echo
// the peer's latest context_token captured from an inbound message.
//
// 协议切换说明：本包此前实现的是公众号（Official Account）HTTP 回调协议
// （WEIXIN_APP_ID/WEIXIN_APP_SECRET/WEIXIN_ENCODING_AES_KEY、XML 回调等）。
// 现已切换为 iLink Bot API。旧 env 变量保留兼容映射：
//   - WEIXIN_TOKEN：旧为回调校验 token，现为 iLink bot token（Bearer）。
//   - WEIXIN_APP_ID：旧为公众号 AppID，现作为 WEIXIN_ACCOUNT_ID 的回退值。
//   - WEIXIN_APP_SECRET / WEIXIN_ENCODING_AES_KEY / WEIXIN_LISTEN_ADDR /
//     WEIXIN_INCOMING_PATH：iLink 协议不再使用（无回调服务器、无 AES 信封）。
package weixin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
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
	PlatformName = "weixin"

	// iLink Bot API base (hermes weixin.py ILINK_BASE_URL).
	defaultILinkBase = "https://ilinkai.weixin.qq.com"

	epGetUpdates   = "ilink/bot/getupdates"
	epSendMessage  = "ilink/bot/sendmessage"
	epGetConfig    = "ilink/bot/getconfig"
	epGetUploadURL = "ilink/bot/getuploadurl"

	ilinkAppID           = "bot"
	ilinkChannelVersion  = "2.2.0"
	ilinkClientVersion   = (2 << 16) | (2 << 8) | 0 // 131328
	longPollTimeoutMS    = 35000
	apiTimeoutMS         = 15000
	maxConsecutiveErrors = 3
	retryDelay           = 2 * time.Second
	backoffDelay         = 30 * time.Second

	// msg payload constants (hermes weixin.py).
	msgTypeBot      = 2
	msgStateFinish  = 2
	itemTypeText    = 1
	sessionExpired  = -14 // iLink session expired errcode/ret
	rateLimitErr    = -2  // iLink frequency limit
	maxChunkRunes   = 2000
	maxSendAttempts = 4

	// retryBackoff is the linear retry backoff base (injectable in tests).
	// contextTokenTTL bounds the per-peer context_token cache. hermes
	// persists tokens indefinitely on disk; we keep an in-memory cache with
	// a generous TTL as tokens are refreshed on every inbound message.
	contextTokenTTL = 24 * time.Hour

	// dedupTTL bounds the inbound message-id dedup window.
	dedupTTL = 5 * time.Minute
)

type Adapter struct {
	accountID  string // iLink bot account id
	token      string // iLink bot token (Bearer)
	baseURL    string // injectable for tests
	httpClient *http.Client
	logger     *slog.Logger

	tokenMu       sync.Mutex
	contextTokens map[string]contextTokenEntry
	seenMsgs      map[string]time.Time
	syncBuf       string // getupdates cursor
	rngMu         sync.Mutex

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
}

type contextTokenEntry struct {
	token   string
	expires time.Time
}

// retryBackoff is the linear retry backoff base (injectable in tests).
var retryBackoff = time.Second

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	accountID := strings.TrimSpace(os.Getenv("WEIXIN_ACCOUNT_ID"))
	if accountID == "" {
		// Compat mapping: WEIXIN_APP_ID carried the公众号 AppID before the
		// protocol switch; reuse it as the iLink account id fallback.
		accountID = strings.TrimSpace(os.Getenv("WEIXIN_APP_ID"))
	}

	baseURL := strings.TrimSpace(os.Getenv("WEIXIN_BASE_URL"))
	if baseURL == "" {
		baseURL = defaultILinkBase
	}

	return &Adapter{
		accountID:     accountID,
		token:         strings.TrimSpace(os.Getenv("WEIXIN_TOKEN")),
		baseURL:       baseURL,
		httpClient:    &http.Client{Timeout: 60 * time.Second},
		logger:        logger,
		contextTokens: map[string]contextTokenEntry{},
		seenMsgs:      map[string]time.Time{},
	}
}

func (a *Adapter) Name() string                      { return PlatformName }
func (a *Adapter) GetName() string                   { return PlatformName }
func (a *Adapter) GetID() string                     { return PlatformName }
func (a *Adapter) Category() plugin.Category         { return plugin.CategoryPlatform }
func (a *Adapter) GetCategory() plugin.Category      { return plugin.CategoryPlatform }
func (a *Adapter) ID() string                        { return PlatformName }
func (a *Adapter) Platform() plugin.PlatformProvider { return a }

func (a *Adapter) Validate() error {
	if a.token == "" {
		return fmt.Errorf("WEIXIN_TOKEN must be set in environment (iLink bot token)")
	}
	if a.accountID == "" {
		return fmt.Errorf("WEIXIN_ACCOUNT_ID must be set in environment (iLink bot account id)")
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
		return fmt.Errorf("weixin: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("weixin: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	safego.SafeGo(func() { a.pollLoop(ctx) }, a.logger)
	a.logger.Info("weixin: started", "base_url", a.baseURL, "account_id", a.accountID)
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
	a.logger.Info("weixin: stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if strings.TrimSpace(channelID) == "" {
		return fmt.Errorf("weixin: empty channel ID (peer user id)")
	}

	contextToken := a.getContextToken(channelID)
	chunks := splitLongMessage(msg.Text)
	for i, chunk := range chunks {
		if err := a.sendChunk(ctx, channelID, chunk, contextToken); err != nil {
			return fmt.Errorf("weixin: send chunk %d/%d: %w", i+1, len(chunks), err)
		}
	}
	return nil
}

// sendChunk posts one text chunk via ilink/bot/sendmessage. On session
// expiry (-14, or -2 with "unknown error") it retries once without the
// context_token, matching hermes weixin.py _send_text_chunk.
func (a *Adapter) sendChunk(ctx context.Context, to, text, contextToken string) error {
	var lastErr error
	retriedWithoutToken := false
	for attempt := 0; attempt < maxSendAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * retryBackoff):
			}
		}

		resp, err := a.apiPost(ctx, epSendMessage,
			map[string]any{"msg": buildTextMessage(to, text, contextToken, newClientID())},
			apiTimeoutMS)
		if err != nil {
			lastErr = err
			continue
		}

		ret := intField(resp, "ret")
		errcode := intField(resp, "errcode")
		if ret == 0 && errcode == 0 {
			return nil
		}
		if isSessionExpired(resp, ret, errcode) && !retriedWithoutToken && contextToken != "" {
			retriedWithoutToken = true
			contextToken = ""
			a.logger.Warn("weixin: session expired; retrying without context_token", "to", to)
			continue
		}
		lastErr = fmt.Errorf("weixin: sendmessage error ret=%d errcode=%d errmsg=%s",
			ret, errcode, stringField(resp, "errmsg"))
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("weixin: sendmessage failed after %d attempts", maxSendAttempts)
	}
	return lastErr
}

// buildTextMessage mirrors hermes _send_message/_send_items: a bot text
// message that echoes the peer's context_token for passive delivery.
func buildTextMessage(to, text, contextToken, clientID string) map[string]any {
	message := map[string]any{
		"from_user_id":  "",
		"to_user_id":    to,
		"client_id":     clientID,
		"message_type":  msgTypeBot,
		"message_state": msgStateFinish,
		"item_list": []any{
			map[string]any{
				"type":      itemTypeText,
				"text_item": map[string]any{"text": text},
			},
		},
	}
	if contextToken != "" {
		message["context_token"] = contextToken
	}
	return message
}

// apiPost performs an iLink API POST with the hermes header set and the
// base_info channel-version envelope.
func (a *Adapter) apiPost(ctx context.Context, endpoint string, payload map[string]any, timeoutMS int64) (map[string]any, error) {
	payload["base_info"] = map[string]any{"channel_version": ilinkChannelVersion}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("weixin: marshal %s body: %w", endpoint, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/"+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("weixin: create %s request: %w", endpoint, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("iLink-App-Id", ilinkAppID)
	req.Header.Set("iLink-App-ClientVersion", fmt.Sprintf("%d", ilinkClientVersion))
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	req.Header.Set("X-WECHAT-UIN", a.randomUIN())

	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	httpCtx := ctx
	if dl, ok := ctx.Deadline(); !ok || dl.After(deadline) {
		var cancel context.CancelFunc
		httpCtx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}

	resp, err := a.httpClient.Do(req.WithContext(httpCtx))
	if err != nil {
		return nil, fmt.Errorf("weixin: %s request: %w", endpoint, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("weixin: read %s response: %w", endpoint, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("weixin: %s HTTP %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("weixin: decode %s response: %w", endpoint, err)
	}
	return result, nil
}

// randomUIN mirrors hermes _headers: base64 of a random 4-byte big-endian int.
func (a *Adapter) randomUIN() string {
	var buf [4]byte
	a.rngMu.Lock()
	_, _ = rand.Read(buf[:])
	a.rngMu.Unlock()
	return base64.StdEncoding.EncodeToString(buf[:])
}

// pollLoop long-polls ilink/bot/getupdates, advancing the opaque cursor and
// dispatching inbound messages, until ctx is done.
func (a *Adapter) pollLoop(ctx context.Context) {
	timeoutMS := int64(longPollTimeoutMS)
	consecutiveErrors := 0
	for {
		if ctx.Err() != nil {
			return
		}

		resp, err := a.apiPost(ctx, epGetUpdates, map[string]any{"get_updates_buf": a.syncBuf}, timeoutMS)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			consecutiveErrors++
			a.logger.Warn("weixin: getupdates failed", "error", err, "streak", consecutiveErrors)
			if !a.sleepWithContext(ctx, delayFor(consecutiveErrors)) {
				return
			}
			continue
		}

		ret := intField(resp, "ret")
		errcode := intField(resp, "errcode")
		if ret != 0 || errcode != 0 {
			consecutiveErrors++
			a.logger.Warn("weixin: getupdates error", "ret", ret, "errcode", errcode,
				"errmsg", stringField(resp, "errmsg"), "streak", consecutiveErrors)
			if isSessionExpired(resp, ret, errcode) {
				// Session expired: pause before retrying (hermes sleeps 10 min).
				if !a.sleepWithContext(ctx, 10*time.Minute) {
					return
				}
				consecutiveErrors = 0
				continue
			}
			if !a.sleepWithContext(ctx, delayFor(consecutiveErrors)) {
				return
			}
			continue
		}

		consecutiveErrors = 0
		if suggested := intField(resp, "longpolling_timeout_ms"); suggested > 0 {
			timeoutMS = int64(suggested)
		}
		if buf := stringField(resp, "get_updates_buf"); buf != "" {
			a.syncBuf = buf // cursor advance
		}

		for _, msgAny := range msgList(resp) {
			if ctx.Err() != nil {
				return
			}
			a.processMessage(msgAny)
		}
	}
}

func delayFor(streak int) time.Duration {
	if streak >= maxConsecutiveErrors {
		return backoffDelay
	}
	return retryDelay
}

func (a *Adapter) sleepWithContext(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// processMessage extracts text + context_token from one inbound getupdates
// message and dispatches it to the registered callback.
func (a *Adapter) processMessage(msg map[string]any) {
	senderID := strings.TrimSpace(stringField(msg, "from_user_id"))
	if senderID == "" || senderID == a.accountID {
		return
	}
	messageID := strings.TrimSpace(stringField(msg, "message_id"))
	if messageID != "" && a.isDuplicate(messageID) {
		return
	}

	// Bind the peer's context_token for all future replies to this sender.
	if ct := strings.TrimSpace(stringField(msg, "context_token")); ct != "" {
		a.setContextToken(senderID, ct)
	}

	text := extractText(msg)
	if text == "" {
		return
	}

	incoming := plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: senderID,
		UserID:    senderID,
		UserName:  senderID,
		Text:      text,
		Timestamp: time.Now(),
		Raw:       msg,
	}

	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()
	if callback != nil {
		callback(incoming)
	}
}

// extractText pulls the first text item out of an inbound item_list,
// mirroring hermes _extract_text (text-only handling for this adapter).
func extractText(msg map[string]any) string {
	items, ok := msg["item_list"].([]any)
	if !ok {
		return ""
	}
	for _, itemAny := range items {
		item, ok := itemAny.(map[string]any)
		if !ok {
			continue
		}
		if itemType(item) != itemTypeText {
			continue
		}
		textItem, _ := item["text_item"].(map[string]any)
		return strings.TrimSpace(stringField(textItem, "text"))
	}
	return ""
}

func itemType(item map[string]any) int {
	if item == nil {
		return 0
	}
	switch v := item["type"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func msgList(resp map[string]any) []map[string]any {
	raw, ok := resp["msgs"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, m := range raw {
		if mm, ok := m.(map[string]any); ok {
			out = append(out, mm)
		}
	}
	return out
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func intField(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

// isSessionExpired mirrors hermes _is_session_expired: errcode/ret -14, or a
// -2 "unknown error" which signals a stale session rather than rate limiting.
func isSessionExpired(resp map[string]any, ret, errcode int) bool {
	if ret == sessionExpired || errcode == sessionExpired {
		return true
	}
	if (ret == rateLimitErr || errcode == rateLimitErr) &&
		strings.EqualFold(stringField(resp, "errmsg"), "unknown error") {
		return true
	}
	return false
}

// isDuplicate tracks inbound message ids within a TTL window.
func (a *Adapter) isDuplicate(messageID string) bool {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()
	now := time.Now()
	if len(a.seenMsgs) > 1024 {
		for k, ts := range a.seenMsgs {
			if now.Sub(ts) > dedupTTL {
				delete(a.seenMsgs, k)
			}
		}
	}
	if ts, ok := a.seenMsgs[messageID]; ok && now.Sub(ts) <= dedupTTL {
		return true
	}
	a.seenMsgs[messageID] = now
	return false
}

func (a *Adapter) setContextToken(userID, token string) {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()
	a.contextTokens[userID] = contextTokenEntry{token: token, expires: time.Now().Add(contextTokenTTL)}
}

func (a *Adapter) getContextToken(userID string) string {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()
	entry, ok := a.contextTokens[userID]
	if !ok {
		return ""
	}
	if time.Now().After(entry.expires) {
		delete(a.contextTokens, userID)
		return ""
	}
	return entry.token
}

// newClientID generates a fresh client_id per outbound message
// (hermes: "hermes-weixin-<uuid>").
func newClientID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return fmt.Sprintf("covo-weixin-%x", buf[:])
}

func splitLongMessage(text string) []string {
	if utf8.RuneCountInString(text) <= maxChunkRunes {
		return []string{text}
	}

	var chunks []string
	lines := strings.Split(text, "\n")
	var current []rune

	for _, line := range lines {
		// Hard-split oversized lines on rune boundaries so multi-byte
		// UTF-8 characters are never truncated.
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
