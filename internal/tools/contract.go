package tools

import "github.com/covoyage/covonaut/agentcore"

// SideEffectScope is the declared blast radius of a tool. Permission,
// sandbox, and plan-mode gates should read this instead of guessing from
// the tool name.
type SideEffectScope string

const (
	ScopeNone      SideEffectScope = "none"
	ScopeWorkspace SideEffectScope = "workspace"
	ScopeGit       SideEffectScope = "git"
	ScopeNetwork   SideEffectScope = "network"
	ScopeSystem    SideEffectScope = "system"
)

// ToolContract is the static declaration for a registered tool.
type ToolContract struct {
	ReadOnly        bool
	Destructive     bool
	ConcurrentSafe  bool
	SideEffectScope SideEffectScope
	RiskLevel       string // low, medium, high
	MaxOutputBytes  int    // 0 = use the global persist threshold
	TimeoutMs       int
}

var toolContracts = map[string]ToolContract{
	"read":               {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"ls":                 {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"glob":               {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"grep":               {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"find":               {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"view":               {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"glob_files":         {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"search_files":       {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"git_status":         {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeGit, RiskLevel: "low"},
	"git_diff":           {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeGit, RiskLevel: "low"},
	"git_log":            {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeGit, RiskLevel: "low"},
	"web_search":         {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNetwork, RiskLevel: "low", MaxOutputBytes: 32_000},
	"web_fetch":          {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNetwork, RiskLevel: "low", MaxOutputBytes: 20_000},
	"vision_analyze":     {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"terminal_list":      {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"terminal_read":      {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"code_graph":         {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"session_search":     {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"sessions_list":      {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"sessions_history":   {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"audit_read":         {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"audit_query":        {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"get_goal":           {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"turn_history":       {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"skill_instructions": {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"tool_search":        {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"tool_describe":      {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"tool_descriptions":  {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"memory_recall":      {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"memory_context":     {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"memory_dream_stats": {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"diffs":              {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"todo":               {ReadOnly: false, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"update_plan":        {ReadOnly: false, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"clarify":            {ReadOnly: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"ask_user":           {ReadOnly: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"exit_plan_mode":     {ReadOnly: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"edit":               {Destructive: true, SideEffectScope: ScopeWorkspace, RiskLevel: "medium"},
	"edit_block":         {Destructive: true, SideEffectScope: ScopeWorkspace, RiskLevel: "medium"},
	"write_file":         {Destructive: true, SideEffectScope: ScopeWorkspace, RiskLevel: "medium"},
	"append_file":        {Destructive: true, SideEffectScope: ScopeWorkspace, RiskLevel: "medium"},
	"apply_patch":        {Destructive: true, SideEffectScope: ScopeWorkspace, RiskLevel: "medium"},
	"delete":             {Destructive: true, SideEffectScope: ScopeWorkspace, RiskLevel: "high"},
	"bash":               {SideEffectScope: ScopeSystem, RiskLevel: "high", MaxOutputBytes: 32_000, TimeoutMs: 120_000},
	"execute_code":       {SideEffectScope: ScopeSystem, RiskLevel: "high", MaxOutputBytes: 32_000},
	"tool_call":          {SideEffectScope: ScopeSystem, RiskLevel: "high"},
	"script_workflow":    {SideEffectScope: ScopeWorkspace, RiskLevel: "medium"},
}

// LookupContract returns the declared contract for name.
func LookupContract(name string) (ToolContract, bool) {
	c, ok := toolContracts[name]
	return c, ok
}

// ContractReadOnly reports whether name is declared read-only.
func ContractReadOnly(name string) bool {
	c, ok := toolContracts[name]
	return ok && c.ReadOnly
}

// ApplyToolContracts stamps concurrency safety from contracts onto the
// live registry. Unknown tools stay undeclared (fail-closed in mixed mode).
func ApplyToolContracts(agent *agentcore.Agent) {
	if agent == nil {
		return
	}
	for _, name := range agent.ToolNames() {
		c, ok := toolContracts[name]
		if !ok || !c.ConcurrentSafe {
			continue
		}
		if tool, ok := agent.GetTool(name); ok {
			safe := true
			tool.ConcurrencySafe = &safe
		}
	}
}
