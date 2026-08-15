package apiserver

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

const PlatformName = "api_server"

type Adapter struct {
	listenAddr string
	apiKey     string
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

	addr := strings.TrimSpace(os.Getenv("API_SERVER_LISTEN_ADDR"))
	if addr == "" {
		addr = ":8100"
	}

	return &Adapter{
		listenAddr: addr,
		apiKey:     strings.TrimSpace(os.Getenv("API_SERVER_KEY")),
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
		return fmt.Errorf("api_server: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	a.whServer = plugin.NewWebhookServer(a.listenAddr, a.logger)
	a.whServer.RegisterRoute("/api/v1/message", a.handleMessage)
	a.whServer.RegisterRoute("/api/v1/health", a.handleHealth)
	a.whServer.SetOnMessage(func(msg plugin.IncomingMessage) {
		a.mu.Lock()
		callback := a.onMessage
		a.mu.Unlock()
		if callback != nil {
			callback(msg)
		}
	})

	if err := a.whServer.Start(ctx); err != nil {
		return fmt.Errorf("api_server: server start failed: %w", err)
	}

	a.logger.Info("api_server: listening", "addr", a.listenAddr)
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
	a.logger.Info("api_server: stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	a.logger.Info("api_server: send", "channel", channelID, "text_len", len(msg.Text))
	return nil
}

func (a *Adapter) handleMessage(ctx context.Context, body []byte, headers http.Header) (*plugin.IncomingMessage, error) {
	if a.apiKey != "" {
		auth := headers.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != a.apiKey {
			return nil, fmt.Errorf("unauthorized")
		}
	}

	var payload struct {
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

	if payload.ChannelID == "" {
		payload.ChannelID = "api"
	}
	if payload.UserID == "" {
		payload.UserID = "api"
	}

	return &plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: payload.ChannelID,
		UserID:    payload.UserID,
		UserName:  payload.UserName,
		Text:      payload.Text,
		Timestamp: time.Now(),
		Raw:       body,
	}, nil
}

func (a *Adapter) handleHealth(ctx context.Context, body []byte, headers http.Header) (*plugin.IncomingMessage, error) {
	return nil, nil
}
