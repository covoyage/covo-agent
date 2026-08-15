package email

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

const PlatformName = "email"

type Adapter struct {
	smtpHost   string
	smtpPort   string
	username   string
	password   string
	imapServer string
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
		smtpHost:   strings.TrimSpace(os.Getenv("EMAIL_SMTP_HOST")),
		smtpPort:   strings.TrimSpace(os.Getenv("EMAIL_SMTP_PORT")),
		username:   strings.TrimSpace(os.Getenv("EMAIL_USERNAME")),
		password:   strings.TrimSpace(os.Getenv("EMAIL_PASSWORD")),
		imapServer: strings.TrimSpace(os.Getenv("EMAIL_IMAP_SERVER")),
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
	if a.smtpHost == "" || a.username == "" {
		return fmt.Errorf("EMAIL_SMTP_HOST and EMAIL_USERNAME must be set in environment")
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
		return fmt.Errorf("email: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("email: already running")
	}
	a.running = true
	_, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	a.logger.Info("email: started", "smtp_host", a.smtpHost)
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
	a.running = false
	a.logger.Info("email: stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	a.logger.Info("email: send", "to", channelID, "text_len", len(msg.Text))
	return nil
}
