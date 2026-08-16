package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/agent/safety"
	"github.com/covoyage/covo-agent/internal/diff"
	"github.com/covoyage/covo-agent/internal/doomloop"
	"github.com/covoyage/covo-agent/internal/lsp"
	"github.com/covoyage/covonaut/agentcore"
)

// extractArgsAsMap extracts tool call arguments from raw JSON into a
// map[string]string, dropping any non-string values.
func extractArgsAsMap(raw json.RawMessage) map[string]string {
	if raw == nil {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func (ca *CovoAgent) toolGuardrailBeforeHook() agentcore.BeforeHook {
	return func(ctx context.Context, hc *agentcore.HookContext) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("guardrail panic: %v", r)
			}
		}()

		// --- Runtime threat detection (runs before guardrail check) ---
		args := extractArgsAsMap(hc.Arguments)
		if result := safety.DetectToolThreat(hc.ToolName, args); result != nil {
			switch result.Category {
			case safety.ThreatDangerous, safety.ThreatCritical:
				return fmt.Errorf("tool blocked by threat detection: %s", result.Description)
			case safety.ThreatSuspicious:
				// Internal tracking only — do NOT surface to user
				ca.threatWarnings = append(ca.threatWarnings, result.Description)
			}
		}
		// Sequence threat detection — skip in YOLO mode.
		if !ca.isYolo() {
			if history := ca.toolGuardrail.History(); len(history) > 0 {
				recent := make([]string, len(history))
				for i, h := range history {
					recent[i] = h.Name
				}
				if result := safety.DetectSequenceThreat(recent); result != nil {
					switch result.Category {
					case safety.ThreatDangerous, safety.ThreatCritical:
						return fmt.Errorf("tool blocked by threat detection: %s", result.Description)
					case safety.ThreatSuspicious:
						ca.threatWarnings = append(ca.threatWarnings, result.Description)
					}
				}
			}
		}
		// --- end threat detection ---

		decision := ca.toolGuardrail.Check(hc.ToolName, hc.Arguments)
		switch decision {
		case GuardrailBlock:
			return fmt.Errorf("tool %q blocked: detected repeated identical calls", hc.ToolName)
		case GuardrailHalt:
			return fmt.Errorf("tool %q halted: too many consecutive calls of the same tool", hc.ToolName)
		case GuardrailWarn:
			// warn is informational only — the warning text is queued internally;
			// the after-hook will drain it and append to the tool result
		}
		ca.toolGuardrail.Record(hc.ToolName, hc.Arguments)
		return nil
	}
}

// guardrailHaltBeforeToolCall blocks subsequent tool calls after a halt decision.
// When the guardrail returns halt, the before-hook blocks that one call, and this
// BeforeToolCall check blocks all remaining tools in the same turn.
func (ca *CovoAgent) guardrailHaltBeforeToolCall() func(
	ctx context.Context, tc agentcore.ToolCall,
) *agentcore.ToolCallOverride {
	return func(ctx context.Context, tc agentcore.ToolCall) (ov *agentcore.ToolCallOverride) {
		defer func() {
			if r := recover(); r != nil {
				ov = &agentcore.ToolCallOverride{
					Block:   true,
					Result:  fmt.Sprintf("guardrail halt panic: %v", r),
					IsError: true,
				}
			}
		}()

		if ca.toolGuardrail.IsHalted() {
			return &agentcore.ToolCallOverride{
				Block:   true,
				Result:  fmt.Sprintf("tool call blocked: turn halted due to repeated tool %q calls", tc.Name),
				IsError: true,
			}
		}
		return nil
	}
}

// toolGuardrailAfterHook drains pending guardrail warnings (from Check + CheckAfterCall)
// and appends them to the tool result. Also runs no-progress detection on the result.
func (ca *CovoAgent) toolGuardrailAfterHook() agentcore.AfterHook {
	return func(ctx context.Context, hc *agentcore.HookContext, result string, err error) {
		// Only check after successful executions
		if err == nil && result != "" {
			ca.toolGuardrail.CheckAfterCall(hc.ToolName, hc.Arguments, result)
		}
	}
}

var fileWriteToolNames = map[string]string{
	"write_file": "path",
	"edit_block": "path",
	"edit":       "file_path",
	"move":       "path",
	"patch":      "path",
}

// diffApprovalTools is the subset of file-mutating tools for which we can
// compute a proposed diff before execution. The value is the argument key
// that holds the file path. Tools not listed here (e.g. patch, apply_patch,
// move) are excluded because their effect is too complex to simulate.
var diffApprovalTools = map[string]string{
	"write_file":  "path",
	"write":       "path",
	"edit_block":  "path",
	"edit":        "file_path", // also tries "path" at runtime
	"append_file": "file_path",
}

// diffApprovalBeforeHook intercepts file-mutating tools and, when a
// preEditDiffChecker is installed, computes a proposed diff and asks the
// user for approval before the tool is allowed to run.
func (ca *CovoAgent) diffApprovalBeforeHook() agentcore.BeforeHook {
	return func(ctx context.Context, hc *agentcore.HookContext) error {
		if ca.preEditDiffChecker == nil {
			return nil
		}

		// Skip diff approval in YOLO mode — the user has opted into
		// auto-approval, so intercepting would create a contradictory UX.
		if ca.isYolo() {
			return nil
		}

		paramKey, ok := diffApprovalTools[hc.ToolName]
		if !ok {
			return nil
		}

		var args map[string]interface{}
		if err := json.Unmarshal(hc.Arguments, &args); err != nil {
			return nil
		}

		path, _ := args[paramKey].(string)
		// The "edit" tool may use either "file_path" or "path".
		if path == "" && hc.ToolName == "edit" {
			path, _ = args["path"].(string)
		}
		if path == "" {
			return nil
		}

		// Resolve to absolute path.
		fullPath := path
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(ca.workDir, path)
		}

		// Read current file content (may not exist for new files).
		oldContent, err := os.ReadFile(fullPath)
		if err != nil && !os.IsNotExist(err) {
			return nil // can't read, skip diff approval
		}

		// Compute proposed new content.
		newContent, ok := computeProposedContent(hc.ToolName, args, string(oldContent))
		if !ok {
			return nil // can't compute, skip
		}

		// Generate a simple unified diff.
		diffText := generateSimpleDiff(string(oldContent), newContent, filepath.Base(path))
		if diffText == "" {
			return nil // no changes
		}

		if !ca.preEditDiffChecker(ctx, hc.ToolName, path, diffText) {
			return fmt.Errorf("edit to %s rejected by user (diff approval)", path)
		}
		return nil
	}
}

// computeProposedContent simulates the effect of a file-mutating tool to
// determine what the new file content would be. Returns false if the tool
// type is not supported or the computation fails.
func computeProposedContent(toolName string, args map[string]interface{}, oldContent string) (string, bool) {
	switch toolName {
	case "write_file", "write":
		content, _ := args["content"].(string)
		return content, true

	case "edit_block":
		// edit_block supports both single old/new and an edits array.
		var oldText, newText string
		if o, ok := args["old"].(string); ok {
			oldText = o
		}
		if n, ok := args["new"].(string); ok {
			newText = n
		}

		result := oldContent

		// Apply single old→new if present.
		if oldText != "" {
			result = strings.Replace(result, oldText, newText, 1)
		}

		// Apply edits array if present.
		if editsRaw, ok := args["edits"].([]interface{}); ok {
			for _, eRaw := range editsRaw {
				e, ok := eRaw.(map[string]interface{})
				if !ok {
					continue
				}
				ot, _ := e["oldText"].(string)
				nt, _ := e["newText"].(string)
				if ot != "" {
					result = strings.Replace(result, ot, nt, 1)
				}
			}
		}

		return result, true

	case "edit":
		// edit uses the same pattern as edit_block but with different arg names
		var oldText, newText string
		if o, ok := args["old_text"].(string); ok {
			oldText = o
		} else if o, ok := args["oldText"].(string); ok {
			oldText = o
		}
		if n, ok := args["new_text"].(string); ok {
			newText = n
		} else if n, ok := args["newText"].(string); ok {
			newText = n
		}

		if oldText != "" {
			return strings.Replace(oldContent, oldText, newText, 1), true
		}
		return "", false

	case "append_file":
		content, _ := args["content"].(string)
		if content == "" {
			return "", false
		}
		return oldContent + content, true

	default:
		return "", false
	}
}

// generateSimpleDiff produces a unified diff between old and new text using
// the LCS-based diff algorithm for correct handling of insertions/deletions.
func generateSimpleDiff(oldText, newText, filename string) string {
	return diff.Unified(oldText, newText, filename, 3, 60)
}

func (ca *CovoAgent) fileSafetyBeforeHook() agentcore.BeforeHook {
	return func(ctx context.Context, hc *agentcore.HookContext) error {
		paramKey, ok := fileWriteToolNames[hc.ToolName]
		if !ok {
			return nil
		}

		var args map[string]interface{}
		if err := json.Unmarshal(hc.Arguments, &args); err != nil {
			return nil
		}

		path, _ := args[paramKey].(string)
		if path == "" {
			return nil
		}

		if err := ca.CheckWriteSafe(path); err != nil {
			return fmt.Errorf("file write blocked by safety check: %w", err)
		}

		if ca.readOnlyMgr != nil {
			if err := ca.readOnlyMgr.CheckReadOnly(path); err != nil {
				return err
			}
		}
		return nil
	}
}

func (ca *CovoAgent) shellHookBeforeHook() agentcore.BeforeHook {
	return func(ctx context.Context, hc *agentcore.HookContext) error {
		if ca.shellHooks == nil {
			return nil
		}
		var args map[string]any
		json.Unmarshal(hc.Arguments, &args)
		result := ca.shellHooks.Invoke("pre_tool_call", &HookEvent{
			EventName: "pre_tool_call",
			ToolName:  hc.ToolName,
			ToolInput: args,
			SessionID: ca.sessionMgr.CurrentID(),
			Cwd:       ca.homeDir,
		})
		if result != nil && result.Blocked {
			return fmt.Errorf("shell hook blocked: %s", result.Reason)
		}
		return nil
	}
}

func (ca *CovoAgent) trajectoryBeforeHook() agentcore.BeforeHook {
	return func(ctx context.Context, hc *agentcore.HookContext) error {
		if ca.trajectory == nil {
			return nil
		}
		switch hc.ToolName {
		case "system":
			var content string
			json.Unmarshal(hc.Arguments, &content)
			ca.trajectory.RecordSystem(content)
		case "user":
			var content string
			json.Unmarshal(hc.Arguments, &content)
			ca.trajectory.RecordUser(content)
		}
		return nil
	}
}

func (ca *CovoAgent) permissionGateBeforeHook() agentcore.BeforeHook {
	return func(ctx context.Context, hc *agentcore.HookContext) error {
		// Step 1: Approval system pattern check (for bash/process commands)
		if hc.ToolName == "bash" || hc.ToolName == "process" {
			var args struct {
				Command string `json:"command"`
			}
			json.Unmarshal(hc.Arguments, &args)
			if args.Command != "" && ca.approvalSystem != nil {
				decision := ca.approvalSystem.CheckCommand(ctx, args.Command, "")
				if !decision.Approved {
					if decision.Hardline {
						return fmt.Errorf("hardline block: %s", decision.Message)
					}
					// Store pattern info for TUI display
					ca.pendingPattern = decision.Description
					// Fire pre-approval hook
					ca.approvalSystem.FirePreApproval(args.Command, decision.PatternKey, decision.Description)
					// Fall through to TUI gate
				} else {
					// Approved by pattern check (safe, allowlisted, or YOLO)
					return nil
				}
			}
		}

		// Step 1b: Tool-level policy check (for all tools, including file edits).
		// This catches tools that don't go through CheckCommand (e.g. edit_block,
		// write_file, apply_patch) and applies group-alias rules from policy.yaml.
		if ca.approvalSystem != nil {
			if d := ca.approvalSystem.CheckToolPolicy(hc.ToolName); d != nil {
				if !d.Approved {
					return fmt.Errorf("%s", d.Message)
				}
				// Tool explicitly allowed by policy — skip TUI gate.
				return nil
			}
		}

		// Step 1c: Path-based policy check (glob-based permission matching).
		// Evaluates rules like Edit(**/*.pem), Read(.env), MCPTool(server__*)
		// against the file path or MCP tool name in the tool arguments.
		// Deny rules block the call; allow rules skip the TUI gate; ask rules
		// fall through to the TUI permission gate.
		if ca.approvalSystem != nil {
			if d := ca.approvalSystem.CheckPathPolicy(hc.ToolName, hc.Arguments); d != nil {
				if !d.Approved {
					return fmt.Errorf("%s", d.Message)
				}
				// Path explicitly allowed by policy — skip TUI gate.
				return nil
			}
		}

		// Step 2: TUI permission gate (for all dangerous tools)
		if ca.permissionChecker == nil {
			return nil
		}
		if !ca.permissionChecker(ctx, hc.ToolName, hc.Arguments) {
			return fmt.Errorf("tool %q rejected by user", hc.ToolName)
		}
		return nil
	}
}

func (ca *CovoAgent) redactAfterHook() agentcore.AfterHook {
	return func(ctx context.Context, hc *agentcore.HookContext, result string, err error) {
		if result != "" {
			_ = RedactSensitiveText(result, false, false)
		}
	}
}

func (ca *CovoAgent) trajectoryAfterHook() agentcore.AfterHook {
	return func(ctx context.Context, hc *agentcore.HookContext, result string, err error) {
		if ca.trajectory == nil {
			return
		}
		ca.trajectory.RecordToolResult(hc.ToolName, hc.ToolName, result)
	}
}

func (ca *CovoAgent) classifyAfterHook() agentcore.AfterHook {
	return func(ctx context.Context, hc *agentcore.HookContext, result string, err error) {
		if result != "" {
			_ = FileMutationResultLanded(hc.ToolName, result)
		}
	}
}

// doomloopAfterHook records tool calls and checks for doom loops (repeated
// identical calls, cyclical patterns, consecutive failures). When a doom
// loop is detected, the recovery nudge is injected as a system message
// to steer the agent away from the loop. Recovery budget is consumed on
// each detection; when exhausted, a terminal message is injected and
// doomloopBudgetExhausted is set to block further tool calls.
func (ca *CovoAgent) doomloopAfterHook() agentcore.AfterHook {
	return func(ctx context.Context, hc *agentcore.HookContext, result string, err error) {
		if ca.doomloopDetector == nil {
			return
		}
		argsHash := doomloop.HashArgs(string(hc.Arguments))
		call := doomloop.ToolCall{
			Name:       hc.ToolName,
			ArgsHash:   argsHash,
			ResultHash: doomloop.HashResult(result),
			Turn:       ca.doomloopTurn,
			Success:    err == nil,
		}
		if err != nil {
			call.Error = err.Error()
		}
		if det := ca.doomloopDetector.RecordToolCall(call); det != nil {
			ca.doomloopDetected = true
			nudge := det.GetRecoveryNudge()
			if nudge == "" {
				return
			}

			// Try to consume recovery budget
			if ca.doomloopDetector.UseRecovery() {
				remaining := ca.doomloopDetector.RemainingBudget()
				slog.Warn("doom loop detected",
					"type", det.Type,
					"tool", hc.ToolName,
					"turn", ca.doomloopTurn,
					"recovery_remaining", remaining,
					"nudge", nudge)
				if ca.Core() != nil && ca.Core().State() != nil {
					ca.Core().State().AddMessage(agentcore.Message{
						Role:    agentcore.RoleSystem,
						Content: fmt.Sprintf("[doom loop recovery] %s (recovery attempts remaining: %d)", nudge, remaining),
					})
				}
			} else {
				// Budget exhausted — inject terminal message and block
				slog.Error("doom loop recovery budget exhausted",
					"type", det.Type,
					"tool", hc.ToolName,
					"turn", ca.doomloopTurn)
				if ca.Core() != nil && ca.Core().State() != nil {
					ca.Core().State().AddMessage(agentcore.Message{
						Role:    agentcore.RoleSystem,
						Content: "[doom loop] Recovery budget exhausted. You are stuck in a repeating pattern. Stop using tools and respond to the user with what you have accomplished so far.",
					})
				}
				ca.doomloopBudgetExhausted = true
			}
		}
	}
}

func extractFilePathFromArgs(toolName string, args []byte) string {
	switch toolName {
	case "edit_block", "write_file", "move":
		var v struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(args, &v) == nil && v.Path != "" {
			return v.Path
		}
	case "apply_patch":
		var v struct {
			Ops []struct {
				Path string `json:"path"`
			} `json:"ops"`
		}
		if json.Unmarshal(args, &v) == nil && len(v.Ops) > 0 {
			return v.Ops[0].Path
		}
	case "edit":
		var v struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal(args, &v) == nil && v.FilePath != "" {
			return v.FilePath
		}
	case "patch":
		var v struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(args, &v) == nil && v.Path != "" {
			return v.Path
		}
	}
	return ""
}

// lspEditTools is the set of tools that modify files and should trigger
// LSP baseline snapshot (before) and diagnostics feedback (after).
var lspEditTools = map[string]bool{
	"edit": true, "write_file": true, "edit_block": true,
	"apply_patch": true, "move": true, "patch": true,
}

// lspBeforeHook captures the current LSP diagnostics for the target file
// before a file-mutating tool runs. This establishes a baseline so that
// lspAfterToolCall's GetNewDiagnostics can return only the newly-introduced
// diagnostics (or newly-resolved ones) rather than the full set.
//
// Without this hook, SnapshotBaseline is never called and GetNewDiagnostics
// degrades to returning all diagnostics every time — functioning identically
// to a simpler "return all errors" approach but losing the delta
// filtering that the covo-agent LSP manager was designed to provide.
//
// The hook is a no-op when:
//   - LSP is inactive or unavailable
//   - the tool is not a file-mutating tool
//   - no file path can be extracted from the arguments
//   - no LSP client is running for the file's workspace (first edit)
//
// First-edit behavior: when no client exists yet, SnapshotBaseline returns
// without recording a baseline. The subsequent GetNewDiagnostics call will
// spawn the client and return all diagnostics — which is correct, since
// on first edit all diagnostics are "new".
func (ca *CovoAgent) lspBeforeHook() agentcore.BeforeHook {
	return func(ctx context.Context, hc *agentcore.HookContext) error {
		if ca.lspManager == nil || !ca.lspManager.IsActive() {
			return nil
		}
		if !lspEditTools[hc.ToolName] {
			return nil
		}
		filePath := extractFilePathFromArgs(hc.ToolName, hc.Arguments)
		if filePath == "" {
			return nil
		}
		ca.lspManager.SnapshotBaseline(filePath)
		return nil
	}
}

func (ca *CovoAgent) lspAfterToolCall() func(ctx context.Context, tc agentcore.ToolCall, result *agentcore.ToolResult) *agentcore.ToolResult {
	return func(ctx context.Context, tc agentcore.ToolCall, result *agentcore.ToolResult) *agentcore.ToolResult {
		if ca.lspManager == nil || !ca.lspManager.IsActive() {
			return nil
		}
		if result.Err != nil {
			return nil
		}
		if !lspEditTools[tc.Name] {
			return nil
		}
		filePath := extractFilePathFromArgs(tc.Name, []byte(tc.Arguments))
		if filePath == "" {
			return nil
		}
		diags, lspErr := ca.lspManager.GetNewDiagnostics(filePath, 3*time.Second)
		if lspErr != nil || len(diags) == 0 {
			return nil
		}
		report := lsp.ReportForFile(filePath, diags, nil)
		if report == "" {
			return nil
		}
		// Explicit prompt directing the agent to fix the detected errors,
		// phrased as "LSP errors detected in this file, please fix:".
		modified := *result
		modified.Result = result.Result + "\n\nLSP errors detected after edit, please fix:\n" + report
		return &modified
	}
}

// guardrailWarningAfterToolCall drains pending guardrail warnings and appends
// them to the tool result, providing real-time loop guidance to the model.
func (ca *CovoAgent) guardrailWarningAfterToolCall() func(
	ctx context.Context, tc agentcore.ToolCall, result *agentcore.ToolResult,
) *agentcore.ToolResult {
	return func(ctx context.Context, tc agentcore.ToolCall, result *agentcore.ToolResult) *agentcore.ToolResult {
		warnings := ca.toolGuardrail.DrainWarnings()
		// Include threat detection warnings
		if len(ca.threatWarnings) > 0 {
			tw := ca.threatWarnings
			ca.threatWarnings = nil
			if warnings != "" {
				warnings += "\n\n" + strings.Join(tw, "\n")
			} else {
				warnings = strings.Join(tw, "\n")
			}
		}
		if warnings == "" {
			return nil
		}
		// Return as silent — visible to LLM, hidden from user
		return &agentcore.ToolResult{Result: warnings, Silent: true}
	}
}

// chainAfterToolCall composes multiple AfterToolCall handlers.
// Each handler receives the potentially-modified result from the previous handler.
// The chain stops when a handler returns nil (no modification).
func chainAfterToolCall(
	handlers ...func(context.Context, agentcore.ToolCall, *agentcore.ToolResult) *agentcore.ToolResult,
) func(context.Context, agentcore.ToolCall, *agentcore.ToolResult) *agentcore.ToolResult {
	return func(ctx context.Context, tc agentcore.ToolCall, result *agentcore.ToolResult) *agentcore.ToolResult {
		current := result
		for _, h := range handlers {
			if modified := h(ctx, tc, current); modified != nil {
				current = modified
			}
		}
		// Only return a new pointer if something actually changed
		if current != result {
			return current
		}
		return nil
	}
}

// turnBudgetPostProcess enforces the per-turn output budget by persisting
// the largest results to disk when total chars exceed the configured limit.
func (ca *CovoAgent) turnBudgetPostProcess() func(
	ctx context.Context, calls []agentcore.ToolCall, results []agentcore.ToolResult,
) []agentcore.ToolResult {
	cfg := DefaultResultPersistenceConfig(ca.homeDir)
	return func(ctx context.Context, calls []agentcore.ToolCall, results []agentcore.ToolResult) []agentcore.ToolResult {
		return enforceTurnBudget(results, calls, cfg)
	}
}

func (ca *CovoAgent) chainAfterToolCall() func(
	ctx context.Context, tc agentcore.ToolCall, result *agentcore.ToolResult,
) *agentcore.ToolResult {
	return chainAfterToolCall(
		ca.lspAfterToolCall(),
		ca.autoFormatAfterToolCall(),
		ca.guardrailWarningAfterToolCall(),
		ca.persistLargeResultAfterToolCall(),
		ca.untrustedWrapAfterToolCall(),
		ca.claudeHooksPostToolUse(),
	)
}

// hookPermissionMode maps covo-agent's permission posture onto the Codex
// permission_mode vocabulary: plan mode → "plan", COVO_ACCEPT_HOOKS=true →
// "bypassPermissions", otherwise "acceptEdits".
func hookPermissionMode(planMode bool) string {
	switch {
	case planMode:
		return "plan"
	case envBool("COVO_ACCEPT_HOOKS", false):
		return "bypassPermissions"
	default:
		return "acceptEdits"
	}
}

// hookEventBase builds a HookEvent with the common Claude Code/Codex payload
// fields (hook_event_name, session_id, cwd, model, permission_mode, source).
// Callers extend the returned event with event-specific fields (tool_name,
// prompt, ...).
func (ca *CovoAgent) hookEventBase(event string) *HookEvent {
	var sessionID string
	if sm := ca.SessionManager(); sm != nil {
		sessionID = sm.CurrentID()
	}
	return &HookEvent{
		EventName:      event,
		SessionID:      sessionID,
		Cwd:            ca.workDir,
		Model:          ca.model,
		PermissionMode: hookPermissionMode(ca.IsPlanMode()),
		Source:         "covo-agent",
	}
}

// claudeHooksCheckUserPrompt runs Claude Code "UserPromptSubmit" hooks for a
// user-supplied input. A "deny"/"block" decision returns an error that aborts
// the run. Shared by Run and RunDirect so headless/oneshot inputs get the same
// protection as interactive ones.
func (ca *CovoAgent) claudeHooksCheckUserPrompt(input string) error {
	if strings.TrimSpace(input) == "" || ca.shellHooks == nil || !ca.shellHooks.HasEvent("UserPromptSubmit") {
		return nil
	}
	ev := ca.hookEventBase("UserPromptSubmit")
	ev.Prompt = input
	if res := ca.shellHooks.Invoke("UserPromptSubmit", ev); res != nil && res.Blocked {
		return fmt.Errorf("input blocked by UserPromptSubmit hook: %s", res.Reason)
	}
	return nil
}

// claudeHooksPreToolUse runs Claude Code "PreToolUse" hooks registered via
// .claude/hooks.json before a tool executes. A "deny"/"block" decision blocks
// the tool call with the hook's reason surfaced to the model.
func (ca *CovoAgent) claudeHooksPreToolUse() func(
	ctx context.Context, tc agentcore.ToolCall,
) *agentcore.ToolCallOverride {
	return func(ctx context.Context, tc agentcore.ToolCall) *agentcore.ToolCallOverride {
		if ca.shellHooks == nil || !ca.shellHooks.HasEvent("PreToolUse") {
			return nil
		}
		var input map[string]any
		if tc.Arguments != "" {
			_ = json.Unmarshal([]byte(tc.Arguments), &input)
		}
		ev := ca.hookEventBase("PreToolUse")
		ev.ToolName = tc.Name
		ev.ToolInput = input
		res := ca.shellHooks.Invoke("PreToolUse", ev)
		if res != nil && res.Blocked {
			return &agentcore.ToolCallOverride{
				Block:   true,
				Result:  fmt.Sprintf("tool call blocked by PreToolUse hook: %s", res.Reason),
				IsError: true,
			}
		}
		return nil
	}
}

// claudeHooksPostToolUse runs Claude Code "PostToolUse" hooks after a tool
// returns. Post-execution hooks are notification/audit oriented, so their
// decision is not applied — the original result is returned unchanged.
func (ca *CovoAgent) claudeHooksPostToolUse() func(
	ctx context.Context, tc agentcore.ToolCall, result *agentcore.ToolResult,
) *agentcore.ToolResult {
	return func(ctx context.Context, tc agentcore.ToolCall, result *agentcore.ToolResult) *agentcore.ToolResult {
		if ca.shellHooks == nil || !ca.shellHooks.HasEvent("PostToolUse") {
			return result
		}
		var input map[string]any
		if tc.Arguments != "" {
			_ = json.Unmarshal([]byte(tc.Arguments), &input)
		}
		var output string
		if result != nil {
			output = result.Result
		}
		ev := ca.hookEventBase("PostToolUse")
		ev.ToolName = tc.Name
		ev.ToolInput = input
		ev.ToolResponse = output
		ca.shellHooks.Invoke("PostToolUse", ev)
		return result
	}
}

func (ca *CovoAgent) rateLimitToolMiddleware() agentcore.Middleware {
	if ca.rateLimitState == nil {
		return nil
	}
	return func(next agentcore.ExecuteFunc) agentcore.ExecuteFunc {
		return func(ctx context.Context, tc agentcore.ToolCall) (string, error) {
			if ca.rateLimitState.ShouldBackoff() {
				snapshot := ca.rateLimitState.Snapshot()
				if snapshot.RequestsMin.Remaining == 0 || snapshot.TokensMin.Remaining == 0 {
					return "", fmt.Errorf("rate limit exhausted: requests=%d/%d tokens=%d/%d",
						snapshot.RequestsMin.Remaining, snapshot.RequestsMin.Limit,
						snapshot.TokensMin.Remaining, snapshot.TokensMin.Limit)
				}
			}
			return next(ctx, tc)
		}
	}
}

// doomloopBudgetBeforeHook blocks all tool calls when the doom loop recovery
// budget is exhausted. This forces the agent to stop trying and respond
// with what it has accomplished so far.
func (ca *CovoAgent) doomloopBudgetBeforeHook() func(
	ctx context.Context, tc agentcore.ToolCall,
) *agentcore.ToolCallOverride {
	return func(ctx context.Context, tc agentcore.ToolCall) *agentcore.ToolCallOverride {
		if ca.doomloopBudgetExhausted {
			return &agentcore.ToolCallOverride{
				Block:   true,
				Result:  "tool call blocked: doom loop recovery budget exhausted. Respond to the user with your progress so far.",
				IsError: true,
			}
		}
		return nil
	}
}

// combinedBeforeToolCall chains the guardrail halt check, doomloop budget
// check, and Claude Code PreToolUse hooks into a single BeforeToolCall handler.
func (ca *CovoAgent) combinedBeforeToolCall() func(
	ctx context.Context, tc agentcore.ToolCall,
) *agentcore.ToolCallOverride {
	guardrailCheck := ca.guardrailHaltBeforeToolCall()
	doomloopCheck := ca.doomloopBudgetBeforeHook()
	claudeHookCheck := ca.claudeHooksPreToolUse()
	return func(ctx context.Context, tc agentcore.ToolCall) *agentcore.ToolCallOverride {
		if ov := guardrailCheck(ctx, tc); ov != nil {
			return ov
		}
		if ov := doomloopCheck(ctx, tc); ov != nil {
			return ov
		}
		if ov := claudeHookCheck(ctx, tc); ov != nil {
			return ov
		}
		return nil
	}
}

// doomloopLifecycleHook increments the turn counter and manages the doom loop
// detector state across turns.
type doomloopLifecycleHook struct {
	agentcore.BaseLifecycleHook
	ca *CovoAgent
}

func newDoomloopLifecycleHook(ca *CovoAgent) *doomloopLifecycleHook {
	return &doomloopLifecycleHook{ca: ca}
}

func (h *doomloopLifecycleHook) BeforeAgentRun(_ context.Context, _ *agentcore.AgentRunContext) error {
	// Reset per-run state at the start of each Run() call. This ensures that
	// a budget-exhausted agent from a previous run gets a fresh start when the
	// user sends a new message.
	h.ca.doomloopBudgetExhausted = false
	h.ca.doomloopDetected = false
	h.ca.doomloopTurn = 0
	return nil
}

func (h *doomloopLifecycleHook) AfterTurn(_ context.Context, _ *agentcore.AgentRunContext, info agentcore.TurnInfo) {
	h.ca.doomloopTurn++
	if h.ca.doomloopDetector == nil {
		return
	}

	// If no doom loop was detected this turn, reset the detector completely
	// (including recovery budget) — the agent successfully navigated.
	if !h.ca.doomloopDetected {
		h.ca.doomloopDetector.Reset()
		h.ca.doomloopBudgetExhausted = false
	} else {
		// Preserve recovery budget across turns, but clear per-turn history
		h.ca.doomloopDetector.ResetForNewTurn()
	}
	h.ca.doomloopDetected = false
}
