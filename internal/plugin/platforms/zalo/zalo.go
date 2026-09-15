// Package zalo implements the Zalo Bot API platform adapter (protocol
// aligned with the reference implementation extensions/zalo src/api.ts): REST message sending
// via https://bot-api.zaloplatforms.com/bot{token}/{method} and webhook-based
// message receiving authenticated with the x-bot-api-secret-token header.
package zalo

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
	PlatformName      = "zalo"
	defaultAPIBase    = "https://bot-api.zaloplatforms.com"
	defaultListenAddr = ":8094"
	webhookSecretHdr  = "X-Bot-Api-Secret-Token"
)

type Adapter struct {
	accessToken   string // bot token, embedded in the API URL path
	webhookSecret string // optional ZALO_WEBHOOK_SECRET for webhook auth
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

	// ZALO_BOT_TOKEN is the canonical Bot API token; ZALO_OA_ACCESS_TOKEN /
	// ZALO_ACCESS_TOKEN are kept as compatibility fallbacks so existing
	// deployments keep working after the Bot API migration.
	accessToken := strings.TrimSpace(os.Getenv("ZALO_BOT_TOKEN"))
	if accessToken == "" {
		accessToken = strings.TrimSpace(os.Getenv("ZALO_OA_ACCESS_TOKEN"))
	}
	if accessToken == "" {
		accessToken = strings.TrimSpace(os.Getenv("ZALO_ACCESS_TOKEN"))
	}

	listenAddr := strings.TrimSpace(os.Getenv("ZALO_LISTEN_ADDR"))
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}

	return &Adapter{
		accessToken:   accessToken,
		webhookSecret: strings.TrimSpace(os.Getenv("ZALO_WEBHOOK_SECRET")),
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
		return fmt.Errorf("ZALO_BOT_TOKEN not set in environment")
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
		return fmt.Errorf("zalo: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("zalo: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/zalo/webhook", a.serveWebhook)
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
		return fmt.Errorf("zalo: webhook server start failed: %w", err)
	case <-time.After(100 * time.Millisecond):
	}

	a.logger.Info("zalo: adapter started", "listen", a.listenAddr)
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
	a.logger.Info("zalo: adapter stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

// SendMessage sends a text message via the Zalo Bot API:
// POST {base}/bot{token}/sendMessage with {"chat_id": ..., "text": ...}
// (protocol aligned with the reference implementation extensions/zalo src/api.ts).
func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if a.accessToken == "" {
		return fmt.Errorf("zalo: bot token not configured")
	}
	if strings.TrimSpace(channelID) == "" {
		return fmt.Errorf("zalo: empty channel ID (chat_id)")
	}

	body := map[string]any{
		"chat_id": channelID,
		"text":    msg.Text,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("zalo: marshal body: %w", err)
	}

	endpoint := a.apiBase + "/bot" + a.accessToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("zalo: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("zalo: send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("zalo: read response: %w", err)
	}

	var result struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("zalo: decode response (status %d): %w", resp.StatusCode, err)
	}
	if !result.OK {
		if result.ErrorCode != 0 {
			return fmt.Errorf("zalo: api error %d: %s", result.ErrorCode, result.Description)
		}
		return fmt.Errorf("zalo: send failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// serveWebhook handles Zalo Bot API webhook pushes. When
// ZALO_WEBHOOK_SECRET is configured, requests must carry a matching
// X-Bot-Api-Secret-Token header. Only message.text.received events are
// converted into incoming messages (event shape per the reference implementation
// extensions/zalo monitor).
func (a *Adapter) serveWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if a.webhookSecret != "" && r.Header.Get(webhookSecretHdr) != a.webhookSecret {
		a.logger.Warn("zalo: webhook secret verification failed")
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
		UpdateID  int64  `json:"update_id"`
		Message   struct {
			Chat struct {
				ID string `json:"id"`
			} `json:"chat"`
			Text string `json:"text"`
			From struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"from"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		a.logger.Warn("zalo: decode webhook failed", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if event.EventName == "message.text.received" && event.Message.Chat.ID != "" && event.Message.Text != "" {
		incoming := plugin.IncomingMessage{
			Platform:  PlatformName,
			ChannelID: event.Message.Chat.ID,
			UserID:    event.Message.From.ID,
			UserName:  event.Message.From.Name,
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
