package nextcloudtalk

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

const PlatformName = "nextcloud-talk"

type Adapter struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
	logger     *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	return &Adapter{
		baseURL:    strings.TrimSpace(os.Getenv("NEXTCLOUD_TALK_BASE_URL")),
		username:   strings.TrimSpace(os.Getenv("NEXTCLOUD_TALK_USERNAME")),
		password:   strings.TrimSpace(os.Getenv("NEXTCLOUD_TALK_PASSWORD")),
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
	if a.baseURL == "" {
		return fmt.Errorf("NEXTCLOUD_TALK_BASE_URL not set in environment")
	}
	if a.username == "" {
		return fmt.Errorf("NEXTCLOUD_TALK_USERNAME not set in environment")
	}
	if a.password == "" {
		return fmt.Errorf("NEXTCLOUD_TALK_PASSWORD not set in environment")
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
		return fmt.Errorf("nextcloud-talk: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("nextcloud-talk: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	a.logger.Info("nextcloud-talk: adapter started", "url", a.baseURL)
	<-ctx.Done()
	return nil
}

func (a *Adapter) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.running {
		return nil
	}
	a.running = false
	if a.cancel != nil {
		a.cancel()
	}
	a.logger.Info("nextcloud-talk: adapter stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if a.baseURL == "" {
		return fmt.Errorf("nextcloud-talk: base URL not configured")
	}
	a.logger.Info("nextcloud-talk: send message", "channel", channelID)
	return nil
}
