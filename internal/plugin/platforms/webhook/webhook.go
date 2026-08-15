package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
)

const (
	PlatformName = "webhook"
)

type Adapter struct {
	listenAddr  string
	secret      string
	responseURL string
	httpClient  *http.Client
	logger      *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	whServer  *plugin.WebhookServer
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	addr := strings.TrimSpace(os.Getenv("WEBHOOK_LISTEN_ADDR"))
	if addr == "" {
		addr = ":8080"
	}

	return &Adapter{
		listenAddr:  addr,
		secret:      strings.TrimSpace(os.Getenv("WEBHOOK_SECRET")),
		responseURL: strings.TrimSpace(os.Getenv("WEBHOOK_RESPONSE_URL")),
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		logger:      logger,
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
	return nil
}

func (a *Adapter) OnMessage(callback func(plugin.IncomingMessage)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onMessage = callback
}

func (a *Adapter) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("webhook: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	a.whServer = plugin.NewWebhookServer(a.listenAddr, a.logger)
	a.whServer.RegisterRoute("/webhook", a.handleWebhook)
	a.whServer.SetOnMessage(func(msg plugin.IncomingMessage) {
		a.mu.Lock()
		callback := a.onMessage
		a.mu.Unlock()
		if callback != nil {
			callback(msg)
		}
	})

	if err := a.whServer.Start(ctx); err != nil {
		return fmt.Errorf("webhook: server start failed: %w", err)
	}

	a.logger.Info("webhook: listening", "addr", a.listenAddr)
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
	if a.whServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.whServer.Stop(ctx)
	}
	a.running = false
	a.logger.Info("webhook: stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	// Resolve the delivery target: an outbound webhook posts the reply to a
	// callback URL. The channel_id is used directly when it is itself an
	// http(s) URL (per-channel callbacks), otherwise we fall back to the
	// configured WEBHOOK_RESPONSE_URL and carry channel_id in the body.
	target := a.responseURL
	if strings.HasPrefix(channelID, "http://") || strings.HasPrefix(channelID, "https://") {
		target = channelID
	}
	if target == "" {
		return fmt.Errorf("webhook: cannot send reply — no callback URL (set WEBHOOK_RESPONSE_URL or use a URL channel_id)")
	}

	payload := map[string]any{
		"platform":   PlatformName,
		"channel_id": channelID,
		"text":       msg.Text,
	}
	if msg.ParseMode != "" {
		payload["parse_mode"] = msg.ParseMode
	}
	if msg.ReplyTo != "" {
		payload["reply_to"] = msg.ReplyTo
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("webhook: marshal reply: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("webhook: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if a.secret != "" {
		req.Header.Set("X-Webhook-Secret", a.secret)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: post reply to %s: %w", target, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: callback %s returned status %d", target, resp.StatusCode)
	}
	a.logger.Info("webhook: reply delivered", "target", target, "channel", channelID, "text_len", len(msg.Text))
	return nil
}

func (a *Adapter) handleWebhook(ctx context.Context, body []byte, headers http.Header) (*plugin.IncomingMessage, error) {
	var payload struct {
		Platform  string `json:"platform"`
		ChannelID string `json:"channel_id"`
		UserID    string `json:"user_id"`
		UserName  string `json:"user_name"`
		Text      string `json:"text"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, nil
	}

	if payload.Text == "" {
		return nil, nil
	}

	if payload.Platform == "" {
		payload.Platform = PlatformName
	}
	if payload.ChannelID == "" {
		payload.ChannelID = "webhook"
	}
	if payload.UserID == "" {
		payload.UserID = "webhook"
	}

	return &plugin.IncomingMessage{
		Platform:  payload.Platform,
		ChannelID: payload.ChannelID,
		UserID:    payload.UserID,
		UserName:  payload.UserName,
		Text:      payload.Text,
		Timestamp: time.Now(),
		Raw:       body,
	}, nil
}
