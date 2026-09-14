package synologychat

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
)

const (
	PlatformName = "synology-chat"

	defaultListenAddr = ":8080"
	defaultIncoming   = "/synology-incoming"
	maxMessageLen     = 1000
)

type Adapter struct {
	webhookURL    string
	incomingToken string // optional expected token for incoming webhook auth
	listenAddr    string
	incomingPath  string
	httpClient    *http.Client
	logger        *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)

	listener net.Listener
	server   *http.Server
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	listenAddr := strings.TrimSpace(os.Getenv("SYNOLOGY_LISTEN_ADDR"))
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}
	incomingPath := strings.TrimSpace(os.Getenv("SYNOLOGY_INCOMING_PATH"))
	if incomingPath == "" {
		incomingPath = defaultIncoming
	}

	return &Adapter{
		webhookURL:    strings.TrimSpace(os.Getenv("SYNOLOGY_WEBHOOK_URL")),
		incomingToken: strings.TrimSpace(os.Getenv("SYNOLOGY_INCOMING_TOKEN")),
		listenAddr:    listenAddr,
		incomingPath: incomingPath,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		logger:       logger,
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
	if a.webhookURL == "" {
		return fmt.Errorf("SYNOLOGY_WEBHOOK_URL not set in environment")
	}
	return nil
}

func (a *Adapter) OnMessage(callback func(plugin.IncomingMessage)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onMessage = callback
}

func (a *Adapter) callback(in plugin.IncomingMessage) {
	a.mu.Lock()
	cb := a.onMessage
	a.mu.Unlock()
	if cb != nil {
		cb(in)
	}
}

func (a *Adapter) Start(ctx context.Context) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("synology-chat: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("synology-chat: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc(a.incomingPath, a.handleIncoming)
	listener, err := net.Listen("tcp", a.listenAddr)
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("synology-chat: listen on %s: %w", a.listenAddr, err)
	}
	a.listener = listener
	a.server = &http.Server{Handler: mux}

	a.logger.Info("synology-chat: adapter started", "listen", listener.Addr().String(), "path", a.incomingPath)

	go func() {
		<-ctx.Done()
		_ = a.server.Close()
	}()
	go func() {
		if err := a.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			a.logger.Error("synology-chat: http server stopped", "error", err)
		}
	}()
	return nil
}

func (a *Adapter) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.running {
		return nil
	}
	if a.server != nil {
		_ = a.server.Close()
	}
	if a.cancel != nil {
		a.cancel()
	}
	a.running = false
	a.logger.Info("synology-chat: adapter stopped")
	return nil
}

// handleIncoming serves Synology Chat's outgoing webhook: Synology probes with
// GET and delivers messages as form POSTs with flat fields (token, user_id,
// username, text). Query parameters are accepted as a fallback source, and the
// legacy "payload" JSON form field is still parsed for backwards compatibility.
func (a *Adapter) handleIncoming(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Synology probes the URL with GET before registering the integration.
		w.WriteHeader(http.StatusOK)
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			a.logger.Warn("synology-chat: parse incoming form failed", "error", err)
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}

		// Flat fields first: POST form values merged with query parameters
		// (r.FormValue covers both after ParseForm).
		text := firstFormValue(r, "text", "message", "content")
		userID := firstFormValue(r, "user_id", "userId", "user")
		username := firstFormValue(r, "username", "user_name", "name")
		token := firstFormValue(r, "token")

		// Legacy compatibility: if the flat fields carry neither a token nor
		// text, try the old JSON "payload" form field.
		if text == "" && token == "" {
			var payload struct {
				Text     string `json:"text"`
				UserID   string `json:"user_id"`
				Username string `json:"username"`
				Token    string `json:"token"`
			}
			if raw := r.FormValue("payload"); raw != "" {
				if err := json.Unmarshal([]byte(raw), &payload); err != nil {
					a.logger.Warn("synology-chat: parse incoming payload failed", "error", err)
					http.Error(w, "bad payload", http.StatusBadRequest)
					return
				}
				text = payload.Text
				userID = cmp.Or(userID, payload.UserID)
				username = cmp.Or(username, payload.Username)
				token = payload.Token
			}
		}

		// Enforce the expected incoming token when configured.
		if a.incomingToken != "" && token != a.incomingToken {
			a.logger.Warn("synology-chat: incoming webhook token mismatch")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		if text == "" {
			a.logger.Warn("synology-chat: incoming webhook missing text")
			http.Error(w, "missing text", http.StatusBadRequest)
			return
		}

		channelID := userID
		if channelID == "" {
			channelID = username
		}
		a.callback(plugin.IncomingMessage{
			Platform:  PlatformName,
			ChannelID: channelID,
			UserID:    userID,
			UserName:  username,
			Text:      text,
			Timestamp: time.Now(),
			Raw:       map[string]string{"text": text, "user_id": userID, "username": username},
		})
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// firstFormValue returns the first non-empty value among the given form/query
// keys.
func firstFormValue(r *http.Request, keys ...string) string {
	for _, k := range keys {
		if v := r.FormValue(k); v != "" {
			return v
		}
	}
	return ""
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if a.webhookURL == "" {
		return fmt.Errorf("synology-chat: webhook URL not configured")
	}
	if msg.Text == "" {
		return nil
	}
	chunks := splitLongMessage(msg.Text, maxMessageLen)
	for i, chunk := range chunks {
		if err := a.postWebhook(ctx, chunk); err != nil {
			return fmt.Errorf("synology-chat: send chunk %d/%d: %w", i+1, len(chunks), err)
		}
	}
	return nil
}

func (a *Adapter) postWebhook(ctx context.Context, text string) error {
	payload, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return fmt.Errorf("synology-chat: marshal payload: %w", err)
	}
	form := strings.NewReader(url.Values{"payload": {string(payload)}}.Encode())

	req, err := http.NewRequestWithContext(ctx, "POST", a.webhookURL, form)
	if err != nil {
		return fmt.Errorf("synology-chat: create webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("synology-chat: webhook request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("synology-chat: webhook failed (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

func splitLongMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	var current strings.Builder
	for _, line := range strings.Split(text, "\n") {
		for len(line) > maxLen {
			if current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
			}
			chunks = append(chunks, line[:maxLen])
			line = line[maxLen:]
		}
		if current.Len()+len(line)+1 > maxLen && current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(line)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}
