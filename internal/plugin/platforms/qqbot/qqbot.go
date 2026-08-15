package qqbot

import (
	"context"
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

const PlatformName = "qqbot"

type Adapter struct {
	appID      string
	appSecret  string
	botToken   string
	httpClient *http.Client
	logger     *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	whServer  *plugin.WebhookServer
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	return &Adapter{
		appID:      strings.TrimSpace(os.Getenv("QQBOT_APP_ID")),
		appSecret:  strings.TrimSpace(os.Getenv("QQBOT_APP_SECRET")),
		botToken:   strings.TrimSpace(os.Getenv("QQBOT_BOT_TOKEN")),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     logger,
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

	addr := strings.TrimSpace(os.Getenv("QQBOT_LISTEN_ADDR"))
	if addr == "" {
		addr = ":8091"
	}

	a.whServer = plugin.NewWebhookServer(addr, a.logger)
	a.whServer.RegisterRoute("/qqbot/callback", a.handleWebhook)
	a.whServer.SetOnMessage(func(msg plugin.IncomingMessage) {
		a.mu.Lock()
		callback := a.onMessage
		a.mu.Unlock()
		if callback != nil {
			callback(msg)
		}
	})

	if err := a.whServer.Start(ctx); err != nil {
		return fmt.Errorf("qqbot: server start failed: %w", err)
	}

	a.logger.Info("qqbot: listening", "addr", addr)
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
	a.logger.Info("qqbot: stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	a.logger.Info("qqbot: send", "channel", channelID, "text_len", len(msg.Text))
	return nil
}

func (a *Adapter) handleWebhook(ctx context.Context, body []byte, headers http.Header) (*plugin.IncomingMessage, error) {
	a.logger.Info("qqbot: received webhook", "body_len", len(body))
	return nil, nil
}
