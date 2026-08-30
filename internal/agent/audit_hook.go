package agent

import (
	"context"
	"fmt"

	"github.com/covoyage/covo-agent/internal/tools/hook"
	"github.com/covoyage/covonaut/agentcore"
)

// auditHook is the built-in audit logger. It is the reference implementation
// of the "safety/audit logic plugin-ification" pattern: it observes every
// tool call via the BeforeHook/AfterHook chain and persists a record to the
// SQLite audit log, while also publishing events to the EventBus so that
// other plugins (Lua, shell, or Go plugins via plugin.LifecycleHook) can
// subscribe and implement their own audit/safety logic.
//
// Audit is not a separate module but flows through the event bus + hook
// system: any plugin can subscribe to observe (audit) or register a hook to
// intercept (safety).
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

	// Publish to EventBus for subscriber plugins (audit/safety).
	if ext := h.ca.agentTools; ext != nil {
		ext.PublishEvent(hook.EventToolCallStart, map[string]any{
			"session_id": sessionID,
			"tool_name":  hc.ToolName,
			"agent_id":   agentID,
			"args_size":  len(hc.Arguments),
		})
	}

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

	// Publish to EventBus.
	if ext := h.ca.agentTools; ext != nil {
		ext.PublishEvent(hook.EventToolCallEnd, map[string]any{
			"session_id":  sessionID,
			"tool_name":   hc.ToolName,
			"agent_id":    agentID,
			"status":      status,
			"result_size": len(result),
			"error":       errMsg,
			"duration_ms": 0, // populated below if we tracked start time
		})
	}

	// Persist to audit log. Previews and error strings are force-redacted so
	// secret-shaped tokens never land on disk.
	if ext := h.ca.agentTools; ext != nil {
		if store := ext.AuditStore(); store != nil {
			data := auditToolData{
				Status:     status,
				ResultSize: len(result),
				Error:      RedactSensitiveTextForce(errMsg),
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
	ArgsSize      int    `json:"args_size,omitempty"`
	ArgsPreview   string `json:"args_preview,omitempty"`
	Status        string `json:"status,omitempty"`
	ResultSize    int    `json:"result_size,omitempty"`
	ResultPreview string `json:"result_preview,omitempty"`
	Error         string `json:"error,omitempty"`
	DurationMs    int64  `json:"duration_ms,omitempty"`
}

// agentID returns a stable identifier for the current agent instance.
// Uses the working directory + mode as a composite key when available.
func (ca *CovoAgent) agentID() string {
	if ca.workDir != "" {
		return fmt.Sprintf("%s/%s", ca.mode, ca.workDir)
	}
	return string(ca.mode)
}
