package agent

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covonaut/agentcore"
)

// heartbeatIntervalEnv is the env var that enables periodic heartbeat injection.
// Set this to a duration like "30m", "1h", etc.
const heartbeatIntervalEnv = "COVO_HEARTBEAT_INTERVAL"

// HeartbeatHook is a lifecycle hook that periodically injects a proactive
// status-check prompt into the agent's message state during long runs.
// When COVO_HEARTBEAT_INTERVAL is set (e.g. "30m"), the hook starts a
// goroutine that appends a system message every N minutes reminding the
// agent to review its progress and check on pending tasks.
type HeartbeatHook struct {
	agentcore.BaseLifecycleHook
	interval time.Duration
	logger   *slog.Logger
	mu       sync.Mutex
	cancel   context.CancelFunc
}

// NewHeartbeatHookFromEnv creates a HeartbeatHook if the environment variable
// is set. Returns nil if the env var is unset, empty, or invalid.
func NewHeartbeatHookFromEnv(logger *slog.Logger) *HeartbeatHook {
	val := os.Getenv(heartbeatIntervalEnv)
	if val == "" {
		return nil
	}
	d, err := time.ParseDuration(val)
	if err != nil || d <= 0 {
		logger.Warn("invalid heartbeat interval, disabling", "env", heartbeatIntervalEnv, "value", val)
		return nil
	}
	return &HeartbeatHook{
		interval: d,
		logger:   logger,
	}
}

// NewHeartbeatHook creates a heartbeat hook with a specific interval.
func NewHeartbeatHook(interval time.Duration, logger *slog.Logger) *HeartbeatHook {
	return &HeartbeatHook{
		interval: interval,
		logger:   logger,
	}
}

// IntervalMinutes returns the interval in minutes as a string for display.
func (h *HeartbeatHook) IntervalMinutes() string {
	if h == nil {
		return "off"
	}
	return strconv.Itoa(int(h.interval.Minutes()))
}

// BeforeAgentRun starts the heartbeat goroutine that periodically injects
// system messages into the agent's message state.
func (h *HeartbeatHook) BeforeAgentRun(ctx context.Context, arc *agentcore.AgentRunContext) error {
	if h == nil || h.interval <= 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	if h.cancel != nil {
		h.cancel()
	}
	h.cancel = cancel
	h.mu.Unlock()

	safego.SafeGo(func() {
		ticker := time.NewTicker(h.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.logger.Debug("heartbeat: injecting periodic check")
				arc.Agent.State().AddMessage(agentcore.Message{
					Role:    agentcore.RoleSystem,
					Content: "HEARTBEAT — This is a periodic check-in. Review your progress so far: check pending standing orders, pending commitments, running sub-agents, and any active goals. If there are actionable items, address them. If everything is on track, acknowledge briefly.",
				})
			}
		}
	}, h.logger)

	return nil
}

// AfterAgentRun stops the heartbeat goroutine.
func (h *HeartbeatHook) AfterAgentRun(ctx context.Context, arc *agentcore.AgentRunContext, output string, err error) {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
	h.mu.Unlock()
}

// NewHeartbeatLifecycleHook creates a HeartbeatHook from env var if set.
// Returns nil if the env var is not configured.
func NewHeartbeatLifecycleHook(logger *slog.Logger) agentcore.LifecycleHook {
	return NewHeartbeatHookFromEnv(logger)
}
