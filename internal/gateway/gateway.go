package gateway

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covo-agent/internal/tools/media"
)

var (
	DefaultAgentCacheSize = 128
	DefaultAgentIdleTTL   = 30 * time.Minute
	DefaultTickInterval   = 30 * time.Second
)

type Config struct {
	AgentCacheSize int
	AgentIdleTTL   time.Duration
	TickInterval   time.Duration
	Platforms      []plugin.PlatformProvider
	AgentFactory   AgentFactory

	// FooterEnabled appends a runtime metadata footer (model, context %, cwd)
	// to final gateway replies. Default: false.
	FooterEnabled bool

	// FooterModel and FooterProvider are used in the footer text.
	FooterModel    string
	FooterProvider string

	// PairingStore controls DM access. When non-nil, only approved users
	// may interact with the gateway. Unapproved senders receive a pairing
	// code and must be approved by an admin before their messages are
	// processed. Nil disables pairing (all messages accepted).
	PairingStore *PairingStore

	// VisibilityPolicy controls cross-channel session visibility.
	// When nil, full visibility is allowed (backward compatible default).
	VisibilityPolicy *VisibilityPolicy

	// SuspendStore tracks sessions that are temporarily suspended
	// (e.g. rate-limited) with automatic TTL-based resume.
	// Nil disables session suspension.
	SuspendStore *SessionSuspendStore
}

type Gateway struct {
	mu           sync.Mutex
	cfg          Config
	cache        *AgentCache
	pairing      *PairingStore
	suspendStore *SessionSuspendStore
	platforms    []plugin.PlatformProvider
	msgChs       []chan plugin.IncomingMessage
	ctx          context.Context
	cancel       context.CancelFunc
	running      bool
	done         chan struct{}
}

func New(cfg Config) *Gateway {
	if cfg.AgentCacheSize <= 0 {
		cfg.AgentCacheSize = DefaultAgentCacheSize
	}
	if cfg.AgentIdleTTL <= 0 {
		cfg.AgentIdleTTL = DefaultAgentIdleTTL
	}
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = DefaultTickInterval
	}

	return newGatewayFromConfig(cfg)

}

func (g *Gateway) Start(ctx context.Context) error {
	g.mu.Lock()
	if g.running {
		g.mu.Unlock()
		return fmt.Errorf("gateway already running")
	}
	g.ctx, g.cancel = context.WithCancel(ctx)
	g.done = make(chan struct{})
	g.mu.Unlock()

	for _, p := range g.configuredPlatforms() {
		ch := make(chan plugin.IncomingMessage, 64)
		g.msgChs = append(g.msgChs, ch)
		p.OnMessage(func(msg plugin.IncomingMessage) {
			select {
			case ch <- msg:
			default:
				slog.Warn("gateway: dropping message, channel full",
					"platform", msg.Platform, "user", msg.UserName)
			}
		})
	}

	var wg sync.WaitGroup

	for i, p := range g.configuredPlatforms() {
		ch := g.msgChs[i]
		wg.Add(1)
		plat, msgCh := p, ch
		safego.SafeGo(func() {
			defer wg.Done()
			slog.Info("gateway: starting platform", "name", plat.Name())
			if err := plat.Start(g.ctx); err != nil {
				slog.Error("gateway: platform start failed", "name", plat.Name(), "error", err)
				return
			}
			g.dispatchPlatform(g.ctx, plat, msgCh)
		}, nil)
	}

	wg.Add(1)
	safego.SafeGo(func() {
		defer wg.Done()
		ticker := time.NewTicker(g.cfg.TickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-g.ctx.Done():
				return
			case <-ticker.C:
				g.cache.evictStale()
			}
		}
	}, nil)

	// Pairing cleanup: periodically remove expired codes and lockouts.
	if g.pairing != nil {
		wg.Add(1)
		safego.SafeGo(func() {
			defer wg.Done()
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-g.ctx.Done():
					return
				case <-ticker.C:
					g.pairing.Cleanup()
				}
			}
		}, nil)
	}

	// Suspend store cleanup: periodically remove expired suspensions.
	if g.suspendStore != nil {
		wg.Add(1)
		safego.SafeGo(func() {
			defer wg.Done()
			ticker := time.NewTicker(1 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-g.ctx.Done():
					return
				case <-ticker.C:
					g.suspendStore.Cleanup()
				}
			}
		}, nil)
	}

	g.mu.Lock()
	g.running = true
	g.mu.Unlock()

	safego.SafeGo(func() {
		wg.Wait()
		close(g.done)
	}, nil)

	return nil
}

func (g *Gateway) Stop() error {
	g.mu.Lock()
	if !g.running {
		g.mu.Unlock()
		return nil
	}
	g.mu.Unlock()

	g.cancel()

	for _, p := range g.configuredPlatforms() {
		if err := p.Stop(); err != nil {
			slog.Warn("gateway: platform stop error", "name", p.Name(), "error", err)
		}
	}

	g.cache.Close()

	select {
	case <-g.done:
	case <-time.After(10 * time.Second):
		slog.Warn("gateway: timeout waiting for platforms to stop")
	}

	g.mu.Lock()
	g.running = false
	g.mu.Unlock()

	return nil
}

func (g *Gateway) IsRunning() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.running
}

func (g *Gateway) Status() GatewayStatus {
	g.mu.Lock()
	defer g.mu.Unlock()

	status := GatewayStatus{
		Running:   g.running,
		Agents:    g.cache.Size(),
		Platforms: make([]PlatformStatus, 0, len(g.configuredPlatforms())),
	}

	for _, p := range g.configuredPlatforms() {
		ps := PlatformStatus{
			Name:    p.Name(),
			Enabled: true,
		}
		if err := p.Validate(); err != nil {
			ps.Error = err.Error()
		}
		status.Platforms = append(status.Platforms, ps)
	}

	return status
}

func (g *Gateway) dispatchPlatform(ctx context.Context, plat plugin.PlatformProvider, msgCh <-chan plugin.IncomingMessage) {
	for {
		select {
		case <-g.ctx.Done():
			return
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			g.handleMessage(ctx, plat, msg)
		}
	}
}

func (g *Gateway) handleMessage(ctx context.Context, plat plugin.PlatformProvider, msg plugin.IncomingMessage) {
	msg.Text = g.applyAutoTranscription(ctx, msg)
	if strings.TrimSpace(msg.Text) == "" {
		return
	}

	// DM pairing gate: if a PairingStore is configured, only approved
	// users may interact. Unapproved senders receive a pairing code
	// that an admin must approve via "covo-agent pairing approve <code>".
	if g.pairing != nil {
		platformName := plat.Name()
		if g.pairing.IsLockedOut(platformName, msg.UserID) {
			return
		}
		if !g.pairing.IsApproved(platformName, msg.UserID) {
			if g.pairing.IsRateLimited(platformName, msg.UserID) {
				return
			}
			code, err := g.pairing.RequestCode(platformName, msg.UserID, msg.UserName)
			if err != nil {
				slog.Warn("gateway: pairing request failed",
					"platform", platformName, "user", msg.UserName, "error", err)
				return
			}
			hint := i18n.T("pairing.access_denied", "code", code)
			if err := plat.Send(ctx, msg.ChannelID, hint); err != nil {
				slog.Warn("gateway: send pairing code failed",
					"platform", platformName, "user", msg.UserName, "error", err)
			}
			return
		}
	}

	cacheKey := plat.Name() + ":" + msg.ChannelID

	// Session suspension gate: if the session is suspended (e.g. rate-limited),
	// reject the message with a reason until the TTL expires.
	if g.suspendStore != nil {
		if suspended, reason := g.suspendStore.IsSuspended(cacheKey); suspended {
			slog.Info("gateway: session suspended",
				"key", cacheKey, "reason", reason)
			if err := plat.Send(ctx, msg.ChannelID,
				i18n.T("gateway.suspended", "reason", reason)); err != nil {
				slog.Warn("gateway: send suspend notice failed", "error", err)
			}
			return
		}
	}

	// Inject visibility policy into context so session tools can filter
	// cross-channel access based on the configured visibility mode.
	if g.cfg.VisibilityPolicy != nil {
		vp := &VisibilityPolicy{
			Mode:         g.cfg.VisibilityPolicy.Mode,
			CurrentKey:   cacheKey,
			AllowedPeers: g.cfg.VisibilityPolicy.AllowedPeers,
		}
		ctx = WithVisibilityPolicy(ctx, vp)
	}

	agent, err := g.cache.GetOrCreate(ctx, cacheKey)
	if err != nil {
		slog.Error("gateway: create agent failed",
			"platform", msg.Platform,
			"user", msg.UserName,
			"error", err,
		)
		return
	}

	response, err := agent.Run(ctx, msg.Text)
	if err != nil {
		slog.Warn("gateway: agent run failed",
			"platform", msg.Platform,
			"user", msg.UserName,
			"error", err,
		)
		return
	}

	if response == "" {
		return
	}

	// Append runtime metadata footer if enabled
	if g.cfg.FooterEnabled {
		footer := "\n\n──\n"
		if g.cfg.FooterProvider != "" && g.cfg.FooterModel != "" {
			footer += fmt.Sprintf("model=%s/%s", g.cfg.FooterProvider, g.cfg.FooterModel)
		}
		response += footer
	}

	if err := plat.Send(ctx, msg.ChannelID, response); err != nil {
		slog.Warn("gateway: send response failed",
			"platform", msg.Platform,
			"channel", msg.ChannelID,
			"error", err,
		)
	}
}

// applyAutoTranscription looks for an audio attachment on the incoming
// message and, if present, transcribes it to text via the shared whisper
// backend (internal/tools.TranscribeFile), returning the effective text to
// hand to the agent. Any existing msg.Text (e.g. a caption) is preserved
// alongside the transcript. Transcription failures are logged and fall
// back to the original text so a platform hiccup never silently drops a
// message.
func (g *Gateway) applyAutoTranscription(ctx context.Context, msg plugin.IncomingMessage) string {
	var audio *plugin.Attachment
	for i := range msg.Attachments {
		if msg.Attachments[i].Type == plugin.AttachmentTypeAudio {
			audio = &msg.Attachments[i]
			break
		}
	}
	if audio == nil {
		return msg.Text
	}

	path := audio.LocalPath
	if path == "" && audio.URL != "" {
		downloaded, err := downloadToTemp(ctx, audio.URL, audio.FileName)
		if err != nil {
			slog.Warn("gateway: download audio attachment failed",
				"platform", msg.Platform, "user", msg.UserName, "error", err)
			return msg.Text
		}
		defer os.Remove(downloaded)
		path = downloaded
	}
	if path == "" {
		return msg.Text
	}

	transcript, err := media.TranscribeFile(ctx, path, "", "")
	if err != nil {
		slog.Warn("gateway: transcribe audio attachment failed",
			"platform", msg.Platform, "user", msg.UserName, "error", err)
		return msg.Text
	}
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return msg.Text
	}

	if strings.TrimSpace(msg.Text) == "" {
		return "[voice message transcribed]: " + transcript
	}
	return msg.Text + "\n\n[voice message transcribed]: " + transcript
}

// downloadToTemp fetches url and writes it to a temp file, returning the
// file's path. The caller is responsible for removing it.
func downloadToTemp(ctx context.Context, url, fileName string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch attachment: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch attachment: status %d", resp.StatusCode)
	}

	ext := filepath.Ext(fileName)
	if ext == "" {
		ext = filepath.Ext(url)
	}
	f, err := os.CreateTemp("", "covo-attachment-*"+ext)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("save attachment: %w", err)
	}
	return f.Name(), nil
}

type GatewayStatus struct {
	Running   bool             `json:"running"`
	Agents    int              `json:"agents"`
	Platforms []PlatformStatus `json:"platforms"`
}

type PlatformStatus struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Error   string `json:"error,omitempty"`
}

func (s GatewayStatus) String() string {
	out := fmt.Sprintf("  Gateway: %s\n", statusStr(s.Running))
	out += fmt.Sprintf("  Agents:  %d\n", s.Agents)
	out += fmt.Sprintf("  Platforms (%d):\n", len(s.Platforms))
	for _, p := range s.Platforms {
		mark := "✓"
		detail := ""
		if p.Error != "" {
			mark = "✗"
			detail = ": " + p.Error
		}
		out += fmt.Sprintf("    %s %s%s\n", mark, p.Name, detail)
	}
	return out
}

func statusStr(running bool) string {
	if running {
		return "running"
	}
	return "stopped"
}
