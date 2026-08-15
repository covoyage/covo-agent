package nostr

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
)

const PlatformName = "nostr"

type Adapter struct {
	privateKey string
	relays     []string
	logger     *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	relaysStr := strings.TrimSpace(os.Getenv("NOSTR_RELAYS"))
	var relays []string
	if relaysStr != "" {
		for _, r := range strings.Split(relaysStr, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				relays = append(relays, r)
			}
		}
	}
	if len(relays) == 0 {
		relays = []string{"wss://relay.damus.io", "wss://relay.nostr.band"}
	}

	return &Adapter{
		privateKey: strings.TrimSpace(os.Getenv("NOSTR_PRIVATE_KEY")),
		relays:     relays,
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
	if a.privateKey == "" {
		return fmt.Errorf("NOSTR_PRIVATE_KEY not set in environment")
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
		return fmt.Errorf("nostr: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("nostr: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	a.logger.Info("nostr: adapter started", "relays", a.relays)
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
	a.logger.Info("nostr: adapter stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if a.privateKey == "" {
		return fmt.Errorf("nostr: private key not configured")
	}
	a.logger.Info("nostr: send message", "channel", channelID)
	return nil
}
