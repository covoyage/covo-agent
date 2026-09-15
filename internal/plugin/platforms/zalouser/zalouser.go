// Package zalouser implements the Zalo personal account platform.
//
// PROTOCOL NOTE (reference parity): the reference implementation's zalouser extension
// (extensions/zalouser) drives Zalo's private web protocol via zca-js —
// QR-code login producing cookie + imei + userAgent credentials and a
// WebSocket listener for inbound events. That private protocol cannot be
// faithfully reproduced here. This implementation instead uses the official
// Zalo Open API v2 REST endpoints (openapi.zalo.me) for sending plus a
// webhook for receiving. Personal-account tokens must be obtained through
// the Zalo Open Platform (open.zalo.me); this path has NOT been verified
// against a real personal account. For full personal-account support,
// follow the reference implementation/extensions/zalouser.
package zalouser

import (
	"bytes"
	"context"
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
	PlatformName      = "zalouser"
	defaultAPIBase    = "https://openapi.zalo.me"
	defaultListenAddr = ":8095"
	webhookSecretHdr  = "X-Bot-Api-Secret-Token"
)

type Adapter struct {
	accessToken   string
	deviceID      string
	webhookSecret string // optional ZALOUSER_WEBHOOK_SECRET for webhook auth
	listenAddr    string
	apiBase       string // injectable for tests
	httpClient    *http.Client
	logger        *slog.Logger

	server    *http.Server
	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	listenAddr := strings.TrimSpace(os.Getenv("ZALOUSER_LISTEN_ADDR"))
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}

	return &Adapter{
		accessToken:   strings.TrimSpace(os.Getenv("ZALOUSER_ACCESS_TOKEN")),
		deviceID:      strings.TrimSpace(os.Getenv("ZALOUSER_DEVICE_ID")),
		webhookSecret: strings.TrimSpace(os.Getenv("ZALOUSER_WEBHOOK_SECRET")),
		listenAddr:    listenAddr,
		apiBase:       defaultAPIBase,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		logger:        logger,
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
	if a.accessToken == "" {
		return fmt.Errorf("ZALOUSER_ACCESS_TOKEN not set in environment (requires a Zalo personal account token obtained via Zalo login authorisation)")
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
		return fmt.Errorf("zalouser: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("zalouser: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/zalouser/webhook", a.serveWebhook)
	a.server = &http.Server{
		Addr:    a.listenAddr,
		Handler: mux,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	errCh := make(chan error, 1)
	go func() {
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("zalouser: webhook server start failed: %w", err)
	case <-time.After(100 * time.Millisecond):
	}

	a.logger.Info("zalouser: adapter started", "listen", a.listenAddr)
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.server.Shutdown(ctx)
		a.server = nil
	}
	a.running = false
	a.logger.Info("zalouser: adapter stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if a.accessToken == "" {
		return fmt.Errorf("zalouser: access token not configured")
	}
	if strings.TrimSpace(channelID) == "" {
		return fmt.Errorf("zalouser: empty channel ID (to_id)")
	}

	// type "2" = text message.
	body := map[string]string{
		"to_id":   channelID,
		"type":    "2",
		"message": msg.Text,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("zalouser: marshal body: %w", err)
	}

	url := a.apiBase + "/v2.0/za/user/sendmsg/?access_token=" + a.accessToken
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("zalouser: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("zalouser: send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("zalouser: read response: %w", err)
	}

	var result struct {
		Error   int    `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("zalouser: decode response (status %d): %w", resp.StatusCode, err)
	}
	if result.Error != 0 {
		return fmt.Errorf("zalouser: api error %d: %s", result.Error, result.Message)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("zalouser: send failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// serveWebhook handles Zalo user webhook pushes (same JSON event format as
// the OA platform). Only user_send_text events become incoming messages.
// When ZALOUSER_WEBHOOK_SECRET is configured, requests must carry a
// matching X-Bot-Api-Secret-Token header (same optional pattern as the
// zalo adapter).
func (a *Adapter) serveWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if a.webhookSecret != "" && r.Header.Get(webhookSecretHdr) != a.webhookSecret {
		a.logger.Warn("zalouser: webhook secret verification failed")
		http.Error(w, "invalid secret token", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var event struct {
		EventName string `json:"event_name"`
		Sender    struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"sender"`
		Message struct {
			Text string `json:"text"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		a.logger.Warn("zalouser: decode webhook failed", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if event.EventName == "user_send_text" && event.Sender.ID != "" && event.Message.Text != "" {
		incoming := plugin.IncomingMessage{
			Platform:  PlatformName,
			ChannelID: event.Sender.ID,
			UserID:    event.Sender.ID,
			UserName:  event.Sender.Name,
			Text:      event.Message.Text,
			Timestamp: time.Now(),
			Raw:       body,
		}

		a.mu.Lock()
		callback := a.onMessage
		a.mu.Unlock()
		if callback != nil {
			callback(incoming)
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":1}`))
}
