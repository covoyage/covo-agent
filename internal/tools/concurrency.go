package tools

import (
	"github.com/covoyage/covonaut/agentcore"
)

// toolConcurrencyDeclarations declares each tool's concurrency safety for
// mixed execution mode (agentcore.ModeMixed): true joins the rolling
// parallel pool, false runs behind a serial barrier. Names absent from this
// map stay undeclared and fail closed — they execute exclusively.
//
// The map is applied by intersecting with the live registry, so stale
// entries are harmless; the real risk is a missing entry, which costs
// parallelism but never safety. When adding a read-only tool, declare it
// here; anything with side effects must stay out.
var toolConcurrencyDeclarations = map[string]bool{
	// --- covonaut builtin layer: filesystem & search (read-only) ---
	"read":       true,
	"ls":         true,
	"glob":       true,
	"grep":       true,
	"find":       true,
	"view":       true,
	"glob_files": true,

	// --- covonaut builtin layer: git (read-only subcommands) ---
	"git_status": true,
	"git_diff":   true,
	"git_log":    true,

	// --- covonaut builtin layer: web & vision (read-only) ---
	"web_search":     true,
	"web_fetch":      true,
	"vision_analyze": true,

	// --- covonaut builtin layer: persistent terminal (read-only views) ---
	"terminal_list": true,
	"terminal_read": true,

	// --- covo extension layer: search & introspection (read-only) ---
	"search_files":       true,
	"code_graph":         true,
	"session_search":     true,
	"sessions_list":      true,
	"sessions_history":   true,
	"audit_read":         true,
	"audit_query":        true,
	"get_goal":           true,
	"turn_history":       true,
	"skill_instructions": true,
	"tool_search":        true,
	"tool_describe":      true,
	"tool_descriptions":  true,
	"memory_recall":      true,
	"memory_context":     true,
	"memory_dream_stats": true,
}

// DeclareConcurrencySafe marks the named tool as concurrency-safe.
func DeclareConcurrencySafe(agent *agentcore.Agent, name string, safe bool) {
	if agent == nil {
		return
	}
	if tool, ok := agent.GetTool(name); ok {
		tool.ConcurrencySafe = &safe
	}
}

// ApplyConcurrencyDeclarations walks the agent's registry and stamps the
// declared concurrency safety onto every matching tool. Tools not present in
// the map are left undeclared so mixed mode treats them as exclusive
// (fail-closed). Safe to call multiple times; later calls overwrite.
func ApplyConcurrencyDeclarations(agent *agentcore.Agent) {
	if agent == nil {
		return
	}
	for _, name := range agent.ToolNames() {
		safe, declared := toolConcurrencyDeclarations[name]
		if !declared {
			continue
		}
		if tool, ok := agent.GetTool(name); ok {
			tool.ConcurrencySafe = &safe
		}
	}
}
