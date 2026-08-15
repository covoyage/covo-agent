package msgraph

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

const PlatformName = "msgraph"

type Adapter struct {
	tenantID     string
	clientID     string
	clientSecret string
	userID       string
	httpClient   *http.Client
	logger       *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	whServer  *plugin.WebhookServer
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	return &Adapter{
		tenantID:     strings.TrimSpace(os.Getenv("MSGRAPH_TENANT_ID")),
		clientID:     strings.TrimSpace(os.Getenv("MSGRAPH_CLIENT_ID")),
		clientSecret: strings.TrimSpace(os.Getenv("MSGRAPH_CLIENT_SECRET")),
		userID:       strings.TrimSpace(os.Getenv("MSGRAPH_USER_ID")),
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
	if a.tenantID == "" || a.clientID == "" || a.clientSecret == "" {
		return fmt.Errorf("MSGRAPH_TENANT_ID, MSGRAPH_CLIENT_ID and MSGRAPH_CLIENT_SECRET must be set in environment")
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
		return fmt.Errorf("msgraph: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("msgraph: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	addr := strings.TrimSpace(os.Getenv("MSGRAPH_WEBHOOK_LISTEN_ADDR"))
	if addr == "" {
		addr = ":8095"
	}

	a.whServer = plugin.NewWebhookServer(addr, a.logger)
	a.whServer.RegisterRoute("/msgraph/webhook", a.handleWebhook)
	a.whServer.SetOnMessage(func(msg plugin.IncomingMessage) {
		a.mu.Lock()
		callback := a.onMessage
		a.mu.Unlock()
		if callback != nil {
			callback(msg)
		}
	})

	if err := a.whServer.Start(ctx); err != nil {
		return fmt.Errorf("msgraph: server start failed: %w", err)
	}

	a.logger.Info("msgraph: listening", "addr", addr)
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
	a.logger.Info("msgraph: stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	a.logger.Info("msgraph: send", "channel", channelID, "text_len", len(msg.Text))
	return nil
}

func (a *Adapter) handleWebhook(ctx context.Context, body []byte, headers http.Header) (*plugin.IncomingMessage, error) {
	a.logger.Info("msgraph: received webhook", "body_len", len(body))
	return nil, nil
}
