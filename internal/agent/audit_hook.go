package agent

import (
	"context"
	"fmt"

	agenttools "github.com/covoyage/covo-agent/internal/tools"

	"github.com/covoyage/covonaut/agentcore"
)

// auditHook is the built-in audit logger. It is the reference implementation
// of the "safety/audit logic plugin-ification" pattern: it observes every
// tool call via the BeforeHook/AfterHook chain and persists a record to the
// SQLite audit log.
//
// Plugins can implement plugin.LifecycleHook to intercept (safety) or
// observe (audit) tool calls at the same boundaries.
type auditHook struct {
	ca *CovoAgent
}

func (ca *CovoAgent) auditBeforeHook() agentcore.BeforeHook {
	h := &auditHook{ca: ca}
	return h.before
}

func (ca *CovoAgent) auditAfterHook() agentcore.AfterHook {
	h := &auditHook{ca: ca}
	return h.after
}

func (h *auditHook) sessionID() string {
	if h.ca.sessionMgr == nil {
		return ""
	}
	return h.ca.sessionMgr.CurrentID()
}

func (h *auditHook) before(ctx context.Context, hc *agentcore.HookContext) error {
	sessionID := h.sessionID()
	agentID := h.ca.agentID()

	// Persist to audit log (built-in reference audit logger).
	if ext := h.ca.agentTools; ext != nil {
		if store := ext.AuditStore(); store != nil {
			data := auditToolData{
				ArgsSize: len(hc.Arguments),
			}
			// Include a small args preview (first 256 bytes) for debugging.
			// Persisted records are force-redacted so secret-shaped tokens
			// never land on disk (same policy as the telemetry store).
			preview := string(hc.Arguments)
			if len(hc.Arguments) > 256 {
				preview = string(hc.Arguments[:256]) + "..."
			}
			data.ArgsPreview = RedactSensitiveTextForce(preview)
			_ = store.Log("tool:start", sessionID, hc.ToolName, agentID, data)
		}
	}
	return nil
}

func (h *auditHook) after(ctx context.Context, hc *agentcore.HookContext, result string, err error) {
	sessionID := h.sessionID()
	agentID := h.ca.agentID()

	status := "success"
	errMsg := ""
	if err != nil {
		status = "error"
		errMsg = err.Error()
	}

	// Persist to audit log. Previews and error strings are force-redacted so
	// secret-shaped tokens never land on disk.
	if ext := h.ca.agentTools; ext != nil {
		if store := ext.AuditStore(); store != nil {
			data := auditToolData{
				Status:        status,
				ResultSize:    len(result),
				Error:         RedactSensitiveTextForce(errMsg),
				SourceSummary: agenttools.ReadSummaryFromResult(result),
			}
			preview := result
			if len(result) > 256 {
				preview = result[:256] + "..."
			}
			data.ResultPreview = RedactSensitiveTextForce(preview)
			_ = store.Log("tool:end", sessionID, hc.ToolName, agentID, data)
		}
	}
}

// auditToolData is the JSON payload stored alongside audit log entries.
type auditToolData struct {
	ArgsSize      int                     `json:"args_size,omitempty"`
	ArgsPreview   string                  `json:"args_preview,omitempty"`
	Status        string                  `json:"status,omitempty"`
	ResultSize    int                     `json:"result_size,omitempty"`
	ResultPreview string                  `json:"result_preview,omitempty"`
	Error         string                  `json:"error,omitempty"`
	DurationMs    int64                   `json:"duration_ms,omitempty"`
	SourceSummary *agenttools.ReadSummary `json:"source_summary,omitempty"`
}

// agentID returns a stable identifier for the current agent instance.
// Uses the working directory + mode as a composite key when available.
func (ca *CovoAgent) agentID() string {
	if ca.workDir != "" {
		return fmt.Sprintf("%s/%s", ca.mode, ca.workDir)
	}
	return string(ca.mode)
}
