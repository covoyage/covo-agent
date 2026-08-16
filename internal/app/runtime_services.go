package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/audit"
	"github.com/covoyage/covo-agent/internal/lifecycle"
	"github.com/covoyage/covo-agent/internal/syspower"
	"github.com/covoyage/covo-agent/internal/telemetry"
	sessiontools "github.com/covoyage/covo-agent/internal/tools/sessions"
)

const runtimeSidecarContributor = "runtime-session-sidecar"

type sessionLifecycleEvent struct {
	Timestamp  time.Time      `json:"timestamp"`
	Event      string         `json:"event"`
	Turn       int            `json:"turn,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	ToolInput  string         `json:"tool_input,omitempty"`
	ToolResult string         `json:"tool_result,omitempty"`
	Error      string         `json:"error,omitempty"`
	Extra      map[string]any `json:"extra,omitempty"`
}

// RuntimeServices owns optional process-wide integrations that survive agent replacements.
type RuntimeServices struct {
	homeDir    string
	logger     *slog.Logger
	agents     *AgentRuntime
	telemetry  *telemetry.Exporter
	auditStore *audit.Store
	sidecar    *sessiontools.AtomicSessionWriter
	power      *syspower.Listener
	ctx        context.Context
	cancel     context.CancelFunc
	startOnce  sync.Once
	stopOnce   sync.Once
}

func NewRuntimeServices(homeDir string, logger *slog.Logger, agents *AgentRuntime) *RuntimeServices {
	if logger == nil {
		logger = slog.Default()
	}
	services := &RuntimeServices{
		homeDir: homeDir,
		logger:  logger,
		agents:  agents,
		sidecar: sessiontools.NewAtomicSessionWriter(filepath.Join(homeDir, "sessions", "events")),
	}
	telemetryConfig := telemetry.ConfigFromEnv()
	// Initialize the OpenTelemetry tracing pipeline (model/tool spans). No-op
	// unless COVO_OTEL_ENDPOINT is set. Shut down in Stop().
	telemetry.InitOtel(context.Background(), logger)
	if telemetryConfig.Enabled {
		store, err := audit.NewStore(homeDir)
		if err != nil {
			logger.Warn("telemetry audit store unavailable", "err", err)
		} else {
			services.auditStore = store
			services.telemetry = telemetry.New(telemetryConfig, store)
			services.telemetry.SetLogger(logger)
			services.telemetry.SetRedactor(agent.RedactSensitiveTextForce)
		}
	}
	if systemPowerEnabled() {
		services.power = syspower.NewListener()
	}
	return services
}

func (services *RuntimeServices) Start(parent context.Context) {
	services.startOnce.Do(func() {
		if parent == nil {
			parent = context.Background()
		}
		services.ctx, services.cancel = context.WithCancel(parent)
		lifecycle.RegisterFunc(runtimeSidecarContributor, services.recordLifecycleEvent)
		if services.telemetry != nil {
			services.telemetry.Start(services.ctx)
		}
		if services.power != nil {
			services.power.OnPowerEvent(services.handlePowerEvent)
			if !services.power.Start() {
				services.logger.Debug("system power notifications unavailable")
			}
		}
	})
}

func (services *RuntimeServices) Stop() {
	services.stopOnce.Do(func() {
		lifecycle.Global().Unregister(runtimeSidecarContributor)
		if services.power != nil {
			services.power.Stop()
		}
		if services.cancel != nil {
			services.cancel()
		}
		if services.telemetry != nil {
			services.telemetry.Stop()
		}
		if services.auditStore != nil {
			_ = services.auditStore.Close()
		}
		// Flush buffered OTel spans (batch processor) before process exit.
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		telemetry.ShutdownOtel(flushCtx)
	})
}

func (services *RuntimeServices) recordLifecycleEvent(event lifecycle.Event, hookContext *lifecycle.HookContext) {
	if services.sidecar == nil || hookContext == nil || hookContext.SessionID == "" {
		return
	}
	record := sessionLifecycleEvent{
		Timestamp:  time.Now(),
		Event:      event.String(),
		Turn:       hookContext.Turn,
		ToolName:   hookContext.ToolName,
		ToolInput:  agent.RedactSensitiveTextForce(hookContext.ToolInput),
		ToolResult: agent.RedactSensitiveTextForce(hookContext.ToolResult),
		Extra:      hookContext.Extra,
	}
	if hookContext.Error != nil {
		record.Error = agent.RedactSensitiveTextForce(hookContext.Error.Error())
	}
	if err := services.sidecar.Append(hookContext.SessionID, record); err != nil {
		services.logger.Warn("write session lifecycle sidecar", "session", hookContext.SessionID, "err", err)
	}
}

func (services *RuntimeServices) handlePowerEvent(event syspower.Event) {
	switch event {
	case syspower.EventWillSleep:
		if services.telemetry != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := services.telemetry.ExportNow(ctx); err != nil {
				services.logger.Warn("flush telemetry before sleep", "err", err)
			}
		}
	case syspower.EventDidWake:
		if services.agents != nil {
			if current := services.agents.Current(); current != nil {
				if pool := current.CredentialPool(); pool != nil {
					if reset := pool.ResetExhausted(); reset > 0 {
						services.logger.Info("re-enabled exhausted credentials after wake", "count", reset)
					}
				}
			}
		}
	}
}

func systemPowerEnabled() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv("COVO_SYSPOWER_ENABLED")))
	if value != "" {
		return value != "0" && value != "false" && value != "off" && value != "no"
	}
	return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
}
