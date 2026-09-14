package apiserver

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
)

const (
	PlatformName     = "api_server"
	defaultListen    = ":8090"
	maxBodyBytes     = 1 << 20
	channelsFileName = "api_server_channels.json"

	// idempotencyTTL bounds how long an Idempotency-Key result is replayed
	// for duplicate POSTs (mirrors hermes api_server_runs.py retention).
	idempotencyTTL = 10 * time.Minute

	// maxTextBytes caps the text accepted through the OpenAI-compatible
	// chat completions endpoint.
	chatReplyWaitTimeout = 2 * time.Second
)

type Adapter struct {
	listenAddr   string
	authToken    string
	httpClient   *http.Client
	logger       *slog.Logger
	channelsPath string
	idem         *idempotencyCache

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	listener  net.Listener
	server    *http.Server
	channels  map[string]string

	// replyMu guards replyWaiters: channels registered while a synchronous
	// reply is expected for a channel (the /v1/chat/completions handler
	// captures the agent's answer through SendMessage this way).
	replyMu      sync.Mutex
	replyWaiters map[string]chan string
}

// idempotencyEntry is a cached HTTP response for a replayed request.
type idempotencyEntry struct {
	status  int
	body    []byte
	expires time.Time
}

// idempotencyCache is an in-memory TTL cache keyed by (path, Idempotency-Key)
// so duplicate deliveries get the original response without re-processing.
type idempotencyCache struct {
	mu      sync.Mutex
	entries map[string]idempotencyEntry
}

func newIdempotencyCache() *idempotencyCache {
	return &idempotencyCache{entries: make(map[string]idempotencyEntry)}
}

func (c *idempotencyCache) get(key string) (idempotencyEntry, bool) {
	if c == nil {
		return idempotencyEntry{}, false
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if ok && now.After(e.expires) {
		delete(c.entries, key)
		return idempotencyEntry{}, false
	}
	return e, ok
}

func (c *idempotencyCache) put(key string, status int, body []byte) {
	if c == nil {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.entries {
		if now.After(e.expires) {
			delete(c.entries, k)
		}
	}
	c.entries[key] = idempotencyEntry{status: status, body: body, expires: now.Add(idempotencyTTL)}
}

// idempotencyKey returns the cache key for a request, scoped per endpoint
// path, or "" when the client did not send an Idempotency-Key header.
func idempotencyKey(r *http.Request) string {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return ""
	}
	return r.URL.Path + "\x00" + key
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	addr := strings.TrimSpace(os.Getenv("API_SERVER_LISTEN_ADDR"))
	if addr == "" {
		addr = defaultListen
	}

	return &Adapter{
		listenAddr:   addr,
		authToken:    strings.TrimSpace(os.Getenv("API_SERVER_AUTH_TOKEN")),
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		logger:       logger,
		channelsPath: defaultChannelsPath(),
		channels:     make(map[string]string),
		idem:         newIdempotencyCache(),
		replyWaiters: make(map[string]chan string),
	}
}

func defaultChannelsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".covo-agent", channelsFileName)
}

func (a *Adapter) Name() string                      { return PlatformName }
func (a *Adapter) GetName() string                   { return PlatformName }
func (a *Adapter) GetID() string                     { return PlatformName }
func (a *Adapter) Category() plugin.Category         { return plugin.CategoryPlatform }
func (a *Adapter) GetCategory() plugin.Category      { return plugin.CategoryPlatform }
func (a *Adapter) ID() string                        { return PlatformName }
func (a *Adapter) Platform() plugin.PlatformProvider { return a }

func (a *Adapter) Validate() error {
	return nil
}

func (a *Adapter) OnMessage(callback func(plugin.IncomingMessage)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onMessage = callback
}

func (a *Adapter) Start(ctx context.Context) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("api_server: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("api_server: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	a.loadChannels()

	handler := http.NewServeMux()
	handler.HandleFunc("/api/message", a.serveMessage)
	handler.HandleFunc("/api/health", a.serveHealth)
	handler.HandleFunc("/v1/chat/completions", a.serveChatCompletions)

	ln, err := net.Listen("tcp", a.listenAddr)
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("api_server: listen on %s: %w", a.listenAddr, err)
	}
	a.listener = ln
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	a.server = srv

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed && ctx.Err() == nil {
			a.logger.Warn("api_server: http server error", "error", err)
		}
	}()

	a.logger.Info("api_server: listening", "addr", ln.Addr().String())
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
		if err := a.server.Close(); err != nil {
			a.logger.Warn("api_server: server close", "error", err)
		}
	}
	a.server = nil
	a.listener = nil
	a.running = false
	a.logger.Info("api_server: stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" {
		return nil
	}

	// A registered reply waiter captures the reply synchronously (used by
	// /v1/chat/completions) instead of POSTing to a reply URL.
	if ch := a.replyWaiterFor(channelID); ch != nil {
		select {
		case ch <- msg.Text:
		default:
		}
		return nil
	}

	replyURL := a.lookupChannel(channelID)
	if replyURL == "" {
		if strings.HasPrefix(channelID, "http://") || strings.HasPrefix(channelID, "https://") {
			replyURL = channelID
		} else {
			return fmt.Errorf("api_server: no reply URL registered for channel %q; have the client POST to /api/message with \"reply_url\" set first", channelID)
		}
	}

	data, err := json.Marshal(map[string]string{"text": msg.Text})
	if err != nil {
		return fmt.Errorf("api_server: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, replyURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("api_server: create request to %s: %w", replyURL, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("api_server: post to reply URL %s: %w", replyURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("api_server: reply URL %s returned status %d: %s", replyURL, resp.StatusCode, string(body))
	}
	return nil
}

func (a *Adapter) lookupChannel(channelID string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.channels[channelID]
}

func (a *Adapter) rememberChannel(channelID, replyURL string) {
	a.mu.Lock()
	if old, ok := a.channels[channelID]; ok && old == replyURL {
		a.mu.Unlock()
		return
	}
	a.channels[channelID] = replyURL
	a.mu.Unlock()
	a.saveChannels()
}

func (a *Adapter) loadChannels() {
	if a.channelsPath == "" {
		return
	}
	data, err := os.ReadFile(a.channelsPath)
	if err != nil {
		return
	}
	var stored map[string]string
	if err := json.Unmarshal(data, &stored); err != nil {
		a.logger.Warn("api_server: parse channels file", "path", a.channelsPath, "error", err)
		return
	}
	a.mu.Lock()
	for k, v := range stored {
		a.channels[k] = v
	}
	a.mu.Unlock()
}

func (a *Adapter) saveChannels() {
	if a.channelsPath == "" {
		return
	}
	a.mu.Lock()
	stored := make(map[string]string, len(a.channels))
	for k, v := range a.channels {
		stored[k] = v
	}
	a.mu.Unlock()

	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		a.logger.Warn("api_server: marshal channels", "error", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(a.channelsPath), 0o700); err != nil {
		a.logger.Warn("api_server: create config dir", "error", err)
		return
	}
	if err := os.WriteFile(a.channelsPath, data, 0o600); err != nil {
		a.logger.Warn("api_server: write channels file", "error", err)
	}
}

// checkAuth validates the optional Bearer token in constant time
// (mirrors hermes api_server.py's hmac.compare_digest usage).
func (a *Adapter) checkAuth(r *http.Request) bool {
	if a.authToken == "" {
		return true
	}
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, prefix)), []byte(a.authToken)) == 1
}

func (a *Adapter) serveMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed, use POST"})
		return
	}

	if !a.checkAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	// Idempotency-Key: replay the cached response for duplicate deliveries
	// instead of re-dispatching the message to the agent pipeline.
	if key := idempotencyKey(r); key != "" {
		if e, ok := a.idem.get(key); ok {
			writeRaw(w, e.status, e.body)
			return
		}
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("read body: %v", err)})
		return
	}

	var payload struct {
		ChannelID string `json:"channel_id"`
		UserID    string `json:"user_id"`
		UserName  string `json:"user_name"`
		ReplyURL  string `json:"reply_url"`
		Text      string `json:"text"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid JSON: %v", err)})
		return
	}
	if payload.Text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text is required"})
		return
	}
	if payload.ChannelID == "" {
		payload.ChannelID = "api"
	}
	if payload.UserID == "" {
		payload.UserID = "api"
	}
	if payload.ReplyURL != "" {
		a.rememberChannel(payload.ChannelID, payload.ReplyURL)
	}

	incoming := plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: payload.ChannelID,
		UserID:    payload.UserID,
		UserName:  payload.UserName,
		Text:      payload.Text,
		Timestamp: time.Now(),
		Raw:       body,
	}

	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()
	if callback != nil {
		callback(incoming)
	}

	respBody, _ := json.Marshal(map[string]string{"status": "accepted"})
	if key := idempotencyKey(r); key != "" {
		a.idem.put(key, http.StatusOK, respBody)
	}
	writeRaw(w, http.StatusOK, respBody)
}

// serveChatCompletions implements the OpenAI-compatible non-streaming
// endpoint: the last user message is dispatched through the same agent
// pipeline as /api/message, and the reply is captured synchronously and
// returned as a chat.completion object.
func (a *Adapter) serveChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed, use POST"})
		return
	}

	if !a.checkAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	if key := idempotencyKey(r); key != "" {
		if e, ok := a.idem.get(key); ok {
			writeRaw(w, e.status, e.body)
			return
		}
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("read body: %v", err)})
		return
	}

	var payload struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid JSON: %v", err)})
		return
	}

	text, err := lastUserText(payload.Messages)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// channel_id: optional X-Session-Id header, else a generic client id.
	channelID := strings.TrimSpace(r.Header.Get("X-Session-Id"))
	if channelID == "" {
		channelID = "openai"
	}

	ch := make(chan string, 4)
	a.replyMu.Lock()
	if a.replyWaiters == nil {
		a.replyWaiters = make(map[string]chan string)
	}
	a.replyWaiters[channelID] = ch
	a.replyMu.Unlock()
	defer func() {
		a.replyMu.Lock()
		if a.replyWaiters[channelID] == ch {
			delete(a.replyWaiters, channelID)
		}
		a.replyMu.Unlock()
	}()

	incoming := plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: channelID,
		UserID:    channelID,
		UserName:  "openai",
		Text:      text,
		Timestamp: time.Now(),
		Raw:       body,
	}

	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()

	var reply string
	if callback != nil {
		callback(incoming)
		select {
		case reply = <-ch:
		case <-time.After(chatReplyWaitTimeout):
		}
	}

	resp := map[string]any{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   orDefault(payload.Model, "covo-agent"),
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]string{
				"role":    "assistant",
				"content": reply,
			},
			"finish_reason": "stop",
		}},
	}

	respBody, err := json.Marshal(resp)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "marshal response"})
		return
	}
	if key := idempotencyKey(r); key != "" {
		a.idem.put(key, http.StatusOK, respBody)
	}
	writeRaw(w, http.StatusOK, respBody)
}

// lastUserText extracts the content of the last user message.
func lastUserText(messages []struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}) (string, error) {
	if len(messages) == 0 {
		return "", fmt.Errorf("messages must not be empty")
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		var content string
		if err := json.Unmarshal(messages[i].Content, &content); err != nil {
			return "", fmt.Errorf("unsupported message content encoding")
		}
		if content == "" {
			return "", fmt.Errorf("last user message content is empty")
		}
		return content, nil
	}
	return "", fmt.Errorf("no user message in messages")
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func (a *Adapter) replyWaiterFor(channelID string) chan string {
	a.replyMu.Lock()
	defer a.replyMu.Unlock()
	return a.replyWaiters[channelID]
}

func (a *Adapter) serveHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed, use GET"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "marshal response", http.StatusInternalServerError)
		return
	}
	writeRaw(w, code, body)
}

func writeRaw(w http.ResponseWriter, code int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}
