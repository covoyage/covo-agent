package subagent

// DelegationToolNames enumerates every tool that can spawn, steer, or
// coordinate child agents. Leaf children must not receive any of them:
// recursive delegation is blocked physically (by removing the tools from the
// child's toolset override) rather than by prompt-level rules.
// role='orchestrator' children keep them, subject to MaxSpawnDepth.
var DelegationToolNames = []string{
	"sessions_spawn",
	"sessions_spawn_batch",
	"sessions_wait",
	"sessions_fork",
	"sessions_send",
	"send_input",
	"close_agent",
	"interrupt_agent",
	"subagents",
	"advanced_workflow",
	"swarm",
	"swarm_orchestrate",
}

// StripDelegationTools removes the delegation family from a tool-name list.
// Spawn passes the child's toolset override as TOOL NAMES (the parent's
// ToolNames()), so the legacy filter that stripped the toolset NAME
// "delegation" never matched anything — the whole family used to leak into
// leaf children. This strips both the tool names and the legacy "delegation"
// toolset name, so it is correct for either list form.
func StripDelegationTools(names []string) []string {
	blocked := make(map[string]bool, len(DelegationToolNames)+1)
	for _, n := range DelegationToolNames {
		blocked[n] = true
	}
	blocked["delegation"] = true // legacy toolset-name form
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !blocked[n] {
			out = append(out, n)
		}
	}
	return out
}
