// Package line implements the LINE Messaging API platform adapter.
//
// Outbound messages prefer the reply endpoint
// (https://api.line.me/v2/bot/message/reply) when a recent webhook
// replyToken is available for the channel, falling back to the push
// endpoint (https://api.line.me/v2/bot/message/push) otherwise. Inbound
// messages are received from a local HTTP server exposing the LINE
// webhook, verified with the X-Line-Signature HMAC-SHA256 header.
package line

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
)

const (
	PlatformName = "line"
	lineAPIBase  = "https://api.line.me"

	defaultListenAddr   = ":8081"
	defaultIncomingPath = "/line/webhook"

	maxMessageLen      = 5000 // LINE: single text message limit
	maxMessagesPerPush = 5    // LINE: max messages per push/reply request
	maxRetries         = 3

	replyTokenTTL = 30 * time.Second // replyToken validity window after webhook receipt
)

type replyTokenEntry struct {
	token     string
	expiresAt time.Time
}

type Adapter struct {
	channelAccessToken string
	channelSecret      string
	listenAddr         string
	incomingPath       string
	apiBase            string // overridable for tests (httptest server)
	httpClient         *http.Client
	logger             *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	server    *http.Server

	replyMu     sync.Mutex
	replyTokens map[string]replyTokenEntry // channelID -> recent webhook replyToken
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	listenAddr := strings.TrimSpace(os.Getenv("LINE_LISTEN_ADDR"))
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}
	incomingPath := strings.TrimSpace(os.Getenv("LINE_INCOMING_PATH"))
	if incomingPath == "" {
		incomingPath = defaultIncomingPath
	}

	return &Adapter{
		channelAccessToken: strings.TrimSpace(os.Getenv("LINE_CHANNEL_ACCESS_TOKEN")),
		channelSecret:      strings.TrimSpace(os.Getenv("LINE_CHANNEL_SECRET")),
		listenAddr:         listenAddr,
		incomingPath:       incomingPath,
		apiBase:            lineAPIBase,
		httpClient:         &http.Client{Timeout: 30 * time.Second},
		logger:             logger,
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
	if a.channelAccessToken == "" {
		return fmt.Errorf("LINE_CHANNEL_ACCESS_TOKEN not set in environment")
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
		return fmt.Errorf("line: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("line: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	if a.channelSecret == "" {
		a.logger.Warn("line: LINE_CHANNEL_SECRET not set; webhook signature verification will reject all requests")
	}

	mux := http.NewServeMux()
	mux.HandleFunc(a.incomingPath, a.handleWebhook)
	server := &http.Server{Handler: mux}

	ln, err := net.Listen("tcp", a.listenAddr)
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("line: listen on %s: %w", a.listenAddr, err)
	}

	a.mu.Lock()
	a.server = server
	a.mu.Unlock()

	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			a.logger.Error("line: webhook server failed", "error", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	a.logger.Info("line: adapter started", "listen", a.listenAddr, "path", a.incomingPath)
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
	if a.server != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.server.Shutdown(shutdownCtx)
		a.server = nil
	}
	a.running = false
	a.logger.Info("line: adapter stopped")
	return nil
}

// rememberReplyToken stores the webhook replyToken for a channel so the next
// outbound send within the TTL can use the reply endpoint.
func (a *Adapter) rememberReplyToken(channelID, token string) {
	if channelID == "" || token == "" {
		return
	}
	now := time.Now()
	a.replyMu.Lock()
	defer a.replyMu.Unlock()
	if a.replyTokens == nil {
		a.replyTokens = make(map[string]replyTokenEntry)
	}
	// Opportunistic pruning keeps the map bounded.
	for id, e := range a.replyTokens {
		if now.After(e.expiresAt) {
			delete(a.replyTokens, id)
		}
	}
	a.replyTokens[channelID] = replyTokenEntry{token: token, expiresAt: now.Add(replyTokenTTL)}
}

// takeReplyToken returns and deletes the (unexpired) reply token stored for a
// channel; reply tokens are single-use.
func (a *Adapter) takeReplyToken(channelID string) string {
	a.replyMu.Lock()
	defer a.replyMu.Unlock()
	e, ok := a.replyTokens[channelID]
	if !ok {
		return ""
	}
	delete(a.replyTokens, channelID)
	if time.Now().After(e.expiresAt) {
		return ""
	}
	return e.token
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" && msg.Media == nil {
		return nil
	}
	if channelID == "" {
		return fmt.Errorf("line: channel ID (userId, groupId or roomId) is required")
	}

	if msg.Text != "" {
		chunks := splitText(msg.Text, maxMessageLen)
		replyToken := a.takeReplyToken(channelID)
		for i := 0; i < len(chunks); i += maxMessagesPerPush {
			end := i + maxMessagesPerPush
			if end > len(chunks) {
				end = len(chunks)
			}
			batch := chunks[i:end]

			// Reply-first: the webhook replyToken is single-use, so only the
			// first batch may go through the reply endpoint; fall back to
			// push if the reply API rejects it.
			if replyToken != "" {
				if err := a.replyMessages(ctx, replyToken, batch); err == nil {
					replyToken = ""
					continue
				} else {
					a.logger.Warn("line: reply failed, falling back to push", "error", err)
				}
			}
			replyToken = ""

			if err := a.pushMessages(ctx, channelID, batch); err != nil {
				return fmt.Errorf("line: push messages %d-%d/%d: %w", i+1, end, len(chunks), err)
			}
		}
	}

	if msg.Media != nil {
		a.logger.Warn("line: media not supported for this platform, text only")
	}
	return nil
}

// lineTextMessages converts text chunks into LINE text message objects.
func lineTextMessages(chunks []string) []map[string]string {
	messages := make([]map[string]string, 0, len(chunks))
	for _, chunk := range chunks {
		messages = append(messages, map[string]string{"type": "text", "text": chunk})
	}
	return messages
}

// replyMessages sends up to maxMessagesPerPush text messages via the reply API.
func (a *Adapter) replyMessages(ctx context.Context, replyToken string, chunks []string) error {
	data, err := json.Marshal(map[string]any{"replyToken": replyToken, "messages": lineTextMessages(chunks)})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	status, respBody, err := a.postWithRetry(ctx, a.apiBase+"/v2/bot/message/reply", data)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("reply failed (status %d): %s", status, lineErrorMessage(respBody))
	}
	return nil
}

// pushMessages sends up to maxMessagesPerPush text messages in one push API call.
func (a *Adapter) pushMessages(ctx context.Context, channelID string, chunks []string) error {
	data, err := json.Marshal(map[string]any{"to": channelID, "messages": lineTextMessages(chunks)})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	status, respBody, err := a.postWithRetry(ctx, a.apiBase+"/v2/bot/message/push", data)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("push failed (status %d): %s", status, lineErrorMessage(respBody))
	}
	return nil
}

// postWithRetry POSTs body to url with Bearer auth, retrying on transport
// errors and 5xx responses (4xx responses fail immediately).
func (a *Adapter) postWithRetry(ctx context.Context, url string, body []byte) (int, []byte, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return 0, nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+a.channelAccessToken)

		resp, err := a.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			continue
		}
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("server error (status %d): %s", resp.StatusCode, string(data))
			continue
		}
		return resp.StatusCode, data, nil
	}
	return 0, nil, lastErr
}

// lineErrorMessage extracts the "message" field from a LINE API error body.
func lineErrorMessage(body []byte) string {
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Message != "" {
		return payload.Message
	}
	return string(body)
}

// splitText splits text into chunks of at most maxLen runes.
func splitText(text string, maxLen int) []string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(runes) > 0 {
		n := maxLen
		if len(runes) < n {
			n = len(runes)
		}
		chunks = append(chunks, string(runes[:n]))
		runes = runes[n:]
	}
	return chunks
}

// handleWebhook receives LINE webhook POSTs, verifies the X-Line-Signature
// header against the raw body and forwards text messages to the callback.
func (a *Adapter) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if !a.verifySignature(r.Header.Get("X-Line-Signature"), body) {
		a.logger.Warn("line: webhook signature verification failed")
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	for _, ev := range payload.Events {
		if ev.Type != "message" || ev.Message.Type != "text" || ev.Message.Text == "" {
			continue
		}

		// Group/room messages are addressed by groupId/roomId; direct
		// messages by userId. Group/room take precedence.
		channelID := ev.Source.GroupID
		if channelID == "" {
			channelID = ev.Source.RoomID
		}
		if channelID == "" {
			channelID = ev.Source.UserID
		}

		incoming := plugin.IncomingMessage{
			Platform:  PlatformName,
			ChannelID: channelID,
			UserID:    ev.Source.UserID,
			UserName:  ev.Source.UserID,
			Text:      ev.Message.Text,
			Timestamp: time.UnixMilli(ev.Timestamp),
			Raw:       ev,
		}

		// Remember the reply token so the next outbound send can use the
		// reply endpoint (webhook tokens are valid for a short window).
		a.rememberReplyToken(channelID, ev.ReplyToken)

		a.mu.Lock()
		callback := a.onMessage
		a.mu.Unlock()
		if callback != nil {
			callback(incoming)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("{}"))
}

// verifySignature validates the X-Line-Signature header:
// base64(HMAC-SHA256(channelSecret, rawBody)).
func (a *Adapter) verifySignature(signature string, body []byte) bool {
	if signature == "" || a.channelSecret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(a.channelSecret))
	mac.Write(body)
	got, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	return hmac.Equal(mac.Sum(nil), got)
}

type webhookPayload struct {
	Destination string         `json:"destination,omitempty"`
	Events      []webhookEvent `json:"events"`
}

type webhookEvent struct {
	Type       string        `json:"type"`
	ReplyToken string        `json:"replyToken,omitempty"`
	Source     webhookSource `json:"source,omitempty"`
	Message    webhookMsg    `json:"message,omitempty"`
	Timestamp  int64         `json:"timestamp,omitempty"`
}

type webhookSource struct {
	UserID  string `json:"userId,omitempty"`
	GroupID string `json:"groupId,omitempty"`
	RoomID  string `json:"roomId,omitempty"`
}

type webhookMsg struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}
