package agent

import (
	"context"
	"fmt"

	"github.com/covoyage/covonaut/agentcore"
)

// customModeToolGateBeforeHook enforces the allow/deny tool lists defined
// for a custom mode. When the agent is running in a custom mode that has
// tool restrictions, this hook blocks tools that are in the deny list or
// not in the allow list (if an allow list is specified).
func (ca *CovoAgent) customModeToolGateBeforeHook() agentcore.BeforeHook {
	return func(ctx context.Context, hc *agentcore.HookContext) error {
		if !ca.mode.IsCustom() {
			return nil
		}

		def := GetCustomMode(string(ca.mode))
		if def == nil {
			return nil
		}

		// If neither allow nor deny is specified, allow everything.
		if len(def.AllowTools) == 0 && len(def.DenyTools) == 0 {
			return nil
		}

		toolName := hc.ToolName

		// Check deny list first — explicit deny always blocks.
		for _, denied := range def.DenyTools {
			if matchToolName(toolName, denied) {
				return fmt.Errorf(
					"tool %q is blocked in %q mode (denied by mode configuration)",
					toolName, ca.mode,
				)
			}
		}

		// If an allow list is specified, only allow tools in that list.
		if len(def.AllowTools) > 0 {
			for _, allowed := range def.AllowTools {
				if matchToolName(toolName, allowed) {
					return nil
				}
			}
			return fmt.Errorf(
				"tool %q is not available in %q mode (not in allow list)",
				toolName, ca.mode,
			)
		}

		return nil
	}
}

// matchToolName checks if a tool name matches a pattern. Supports exact
// matches and group aliases (edit, bash, net, git, read) via the same
// expansion used by the approval policy system.
func matchToolName(toolName, pattern string) bool {
	if toolName == pattern {
		return true
	}
	// Check tool group aliases
	if tools, ok := toolGroupAliases[pattern]; ok {
		for _, t := range tools {
			if t == toolName {
				return true
			}
		}
	}
	return false
}

// toolGroupAliases mirrors the group definitions from approval/policy.go
// for use in custom mode tool matching.
var toolGroupAliases = map[string][]string{
	"edit": {"write_file", "write", "edit_block", "edit", "apply_patch", "patch", "move", "delete_file", "str_replace_editor"},
	"bash": {"bash", "process"},
	"net":  {"web_fetch", "web_search", "browser"},
	"git":  {"git_commit", "git_push"},
	"read": {"read", "read_file", "glob", "grep", "ls"},
}
