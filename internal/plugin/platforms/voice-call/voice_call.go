package voicecall

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
)

const PlatformName = "voice-call"

type Adapter struct {
	provider   string
	fromNumber string
	httpClient *http.Client
	logger     *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	provider := strings.TrimSpace(os.Getenv("VOICE_CALL_PROVIDER"))
	if provider == "" {
		provider = "twilio"
	}

	return &Adapter{
		provider:   provider,
		fromNumber: strings.TrimSpace(os.Getenv("VOICE_CALL_FROM_NUMBER")),
		httpClient: &http.Client{Timeout: 30 * 1000 * 1000 * 1000},
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
	switch a.provider {
	case "twilio":
		if strings.TrimSpace(os.Getenv("TWILIO_ACCOUNT_SID")) == "" {
			return fmt.Errorf("TWILIO_ACCOUNT_SID not set")
		}
		if strings.TrimSpace(os.Getenv("TWILIO_AUTH_TOKEN")) == "" {
			return fmt.Errorf("TWILIO_AUTH_TOKEN not set")
		}
	case "telnyx":
		if strings.TrimSpace(os.Getenv("TELNYX_API_KEY")) == "" {
			return fmt.Errorf("TELNYX_API_KEY not set")
		}
	case "plivo":
		if strings.TrimSpace(os.Getenv("PLIVO_AUTH_ID")) == "" {
			return fmt.Errorf("PLIVO_AUTH_ID not set")
		}
		if strings.TrimSpace(os.Getenv("PLIVO_AUTH_TOKEN")) == "" {
			return fmt.Errorf("PLIVO_AUTH_TOKEN not set")
		}
	default:
		return fmt.Errorf("voice-call: unsupported provider %q (use twilio, telnyx, or plivo)", a.provider)
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
		return fmt.Errorf("voice-call: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("voice-call: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	a.logger.Info("voice-call: adapter started", "provider", a.provider)
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
	a.logger.Info("voice-call: adapter stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if a.provider == "" {
		return fmt.Errorf("voice-call: provider not configured")
	}
	a.logger.Info("voice-call: send message", "channel", channelID, "provider", a.provider)
	return nil
}
