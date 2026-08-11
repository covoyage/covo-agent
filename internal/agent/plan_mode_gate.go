package agent

import (
	"context"
	"fmt"

	"github.com/covoyage/covonaut/agentcore"
)

// PlanModeAllowedTools are tools explicitly permitted in Plan mode.
// This set includes read-only inspection tools, planning workflow tools
// (todo, update_plan, clarify), and the exit_plan_mode escape hatch.
// Mutating tools (write_file, edit, bash, etc.) are NOT in this set and
// will be blocked by planModeGateBeforeHook.
var PlanModeAllowedTools = map[string]bool{
	// Read-only file/search tools
	"read": true, "grep": true, "glob": true, "ls": true,
	"search_files": true,
	"web_search": true, "web_fetch": true,
	"session_search": true, "diffs": true,
	"git_status": true, "git_log": true, "git_diff": true,
	// Planning workflow tools (do not mutate files)
	"todo": true, "update_plan": true, "clarify": true,
	"exit_plan_mode": true,
	// Memory read-only
	"memory_recall": true,
	// Tool discovery (read-only)
	"tool_search": true, "tool_describe": true,
}

// planModeGateBeforeHook enforces a strict allowlist when the agent is in
// Plan execution phase. Only tools in PlanModeAllowedTools pass through;
// everything else is blocked — including unknown tools.
//
// This deny-by-default approach is critical for security: tools like
// delete_file, write, apply_patch, or future MCP tools that are NOT in
// PlanModeAllowedTools but also not in MutatingToolNames would otherwise
// slip through if we only blocked known mutating tools.
//
// The toolset filter (BeforeModelCall) additionally hides non-allowlisted
// tools from the LLM's view entirely, so the LLM should never attempt to
// call them. This hook is the code-level backstop.
//
// The only way to exit Plan mode is through:
//   - The exit_plan_mode tool (which triggers user approval)
//   - The /act slash command (direct user action)
//   - The /plan slash command to re-enter Plan mode
//
// This hook is inserted at the FRONT of the GlobalBefore chain so that
// blocked tools never reach downstream hooks (guardrail, audit, etc.).
func (ca *CovoAgent) planModeGateBeforeHook() agentcore.BeforeHook {
	return func(ctx context.Context, hc *agentcore.HookContext) error {
		if !ca.IsPlanMode() {
			return nil
		}

		// Allow tools explicitly permitted in Plan mode.
		if PlanModeAllowedTools[hc.ToolName] {
			return nil
		}

		// Block everything else — deny by default.
		return fmt.Errorf(
			"tool %q is blocked in Plan mode — only read-only tools are allowed. "+
				"Use exit_plan_mode to transition to Act mode after your plan is approved, "+
				"or use /act to switch manually.",
			hc.ToolName,
		)
	}
}

// --- ExecutionPhase state management ---
//
// These methods are used by the slashcmd layer (/plan, /act) and the
// exit_plan_mode tool approval flow to transition between phases.
// The phaseMu and executionPhase fields are on CovoAgent (see agent.go).

// IsPlanMode returns true when the agent is in Plan execution phase.
func (ca *CovoAgent) IsPlanMode() bool {
	ca.phaseMu.RLock()
	defer ca.phaseMu.RUnlock()
	return ca.executionPhase == PhasePlan
}

// ExecutionPhase returns the current execution phase.
func (ca *CovoAgent) ExecutionPhase() ExecutionPhase {
	ca.phaseMu.RLock()
	defer ca.phaseMu.RUnlock()
	return ca.executionPhase
}

// SetExecutionPhase transitions between Plan and Act modes.
// This is called when:
//   - exit_plan_mode is approved by the user (Plan → Act)
//   - /plan slash command is used (Act → Plan)
//   - /act slash command is used (Plan → Act)
//
// After transitioning, the toolset filter cache is invalidated so that
// the LLM sees the updated tool list on the next model call.
func (ca *CovoAgent) SetExecutionPhase(phase ExecutionPhase) {
	ca.phaseMu.Lock()
	ca.executionPhase = phase
	ca.phaseMu.Unlock()

	// Invalidate toolset filter cache so tools are re-evaluated.
	if ca.toolsetFilter != nil {
		ca.toolsetFilter.InvalidateAvailability()
	}
}
