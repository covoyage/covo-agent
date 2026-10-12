package toolset

import (
	"context"
	"log/slog"
	"sort"

	"github.com/covoyage/covonaut/agentcore"
)

// ToolsetFilter is a LifecycleHook that filters tool definitions sent to the LLM
// based on active toolsets and tool availability. It intercepts BeforeModelCall
// to replace the full tool list with only the tools allowed by the current toolsets.
type ToolsetFilter struct {
	agentcore.BaseLifecycleHook

	platform        *PlatformToolsets
	availability    *ToolAvailability
	filter          *CachedFilter
	toolNames       func() []string   // returns all registered tool names from the registry
	platforms       func() string     // returns current platform name
	planModeChecker func() bool       // returns true when agent is in Plan mode
	planModeAllowed map[string]bool   // tools allowed in Plan mode
	nameFilter      func(string) bool // optional per-tool-name predicate (e.g. headless --tools)
	contextFilter   func(context.Context, string) bool
	leanMode        bool // hide long-tail schemas; keep them via tool_search
	leanCyber       bool // keep scan/web schemas in lean cyber mode
	cyberMode       bool // expose scan/finding tools only in cyber mode
	logger          *slog.Logger
}

// ToolsetFilterConfig configures a ToolsetFilter.
type ToolsetFilterConfig struct {
	Platform        *PlatformToolsets
	Availability    *ToolAvailability
	Filter          *CachedFilter
	ToolNames       func() []string
	PlatformName    func() string
	PlanModeChecker func() bool                        // returns true when agent is in Plan mode
	PlanModeAllowed map[string]bool                    // tools allowed in Plan mode (overrides filtering)
	NameFilter      func(string) bool                  // optional per-tool-name predicate; nil = no extra filtering
	ContextFilter   func(context.Context, string) bool // optional role/run-aware predicate
	LeanMode        bool                               // hide long-tail schemas; keep them via tool_search
	LeanCyber       bool                               // keep scan/web schemas when lean + cyber
	CyberMode       bool                               // expose scan/finding tools only in cyber mode
	Logger          *slog.Logger
}

// NewToolsetFilter creates a new toolset filter lifecycle hook.
func NewToolsetFilter(cfg ToolsetFilterConfig) *ToolsetFilter {
	if cfg.Filter == nil {
		cfg.Filter = NewCachedFilter()
	}
	if cfg.Availability == nil {
		cfg.Availability = NewToolAvailability()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	// Default Plan mode allowed tools if not provided
	planModeAllowed := cfg.PlanModeAllowed
	if planModeAllowed == nil {
		planModeAllowed = DefaultPlanModeAllowedTools
	}

	return &ToolsetFilter{
		platform:        cfg.Platform,
		availability:    cfg.Availability,
		filter:          cfg.Filter,
		toolNames:       cfg.ToolNames,
		platforms:       cfg.PlatformName,
		planModeChecker: cfg.PlanModeChecker,
		planModeAllowed: planModeAllowed,
		nameFilter:      cfg.NameFilter,
		contextFilter:   cfg.ContextFilter,
		leanCyber:       cfg.LeanCyber,
		leanMode:        cfg.LeanMode,
		cyberMode:       cfg.CyberMode,
		logger:          cfg.Logger,
	}
}

// Availability returns the tool availability tracker for external registration.
func (tf *ToolsetFilter) Availability() *ToolAvailability {
	return tf.availability
}

// Filter returns the cached filter for external invalidation.
func (tf *ToolsetFilter) FilterRef() *CachedFilter {
	return tf.filter
}

// Platform returns the platform toolsets resolver.
func (tf *ToolsetFilter) PlatformRef() *PlatformToolsets {
	return tf.platform
}

// BeforeModelCall filters the tool definitions in the request based on active toolsets.
func (tf *ToolsetFilter) BeforeModelCall(ctx context.Context, _ *agentcore.AgentRunContext, mcc *agentcore.ModelCallContext) error {
	if mcc.Request == nil || tf.toolNames == nil {
		return nil
	}

	platform := "cli"
	if tf.platforms != nil {
		platform = tf.platforms()
	}

	toolsetNames := tf.platform.ToolsetNames(platform)

	result, err := tf.filter.Filter(toolsetNames, tf.availability, tf.toolNames())
	if err != nil {
		tf.logger.Warn("toolset filter error, retaining policy filters", "error", err)
		result = &FilterResult{Tools: tf.toolNames()}
	}

	// Build filtered tool definitions
	allowedSet := make(map[string]bool)
	for _, name := range result.Tools {
		allowedSet[name] = true
	}

	// In Plan mode, additionally filter out mutating tools.
	// Only tools in the planModeAllowed set pass through.
	inPlanMode := tf.planModeChecker != nil && tf.planModeChecker()

	var filtered []agentcore.ToolDefinition
	for _, def := range mcc.Request.Tools {
		if !allowedSet[def.Name] {
			continue
		}
		if inPlanMode && !tf.planModeAllowed[def.Name] {
			continue
		}
		if tf.nameFilter != nil && !tf.nameFilter(def.Name) {
			continue
		}
		if tf.contextFilter != nil && !tf.contextFilter(ctx, def.Name) {
			continue
		}
		if tf.leanMode && !tf.leanVisible(def.Name) {
			continue
		}
		if CyberOnlyTools[def.Name] && !tf.cyberMode {
			continue
		}
		filtered = append(filtered, def)
	}

	// Log exclusion summary
	if len(result.Excluded) > 0 {
		reasons := make(map[string]int)
		for _, reason := range result.Excluded {
			reasons[reason]++
		}
		tf.logger.Debug("tools filtered",
			"platform", platform,
			"toolsets", toolsetNames,
			"kept", len(filtered),
			"excluded", len(result.Excluded),
			"reasons", reasons,
		)
	}

	mcc.Request.Tools = filtered
	return nil
}

// InvalidateTools should be called when the tool registry changes.
func (tf *ToolsetFilter) InvalidateTools() {
	tf.filter.InvalidateTools()
}

// InvalidateAvailability should be called when tool availability changes.
func (tf *ToolsetFilter) InvalidateAvailability() {
	tf.filter.InvalidateAvailability()
}

// ToolPreview is a read-only snapshot of what the model would see.
type ToolPreview struct {
	Kept     []string
	Excluded map[string]string // tool name → reason
	Toolsets []string
	Platform string
	PlanMode bool
	LeanMode bool
}

// Preview applies the same filters as BeforeModelCall without mutating a request.
func (tf *ToolsetFilter) Preview(allToolNames []string) ToolPreview {
	out := ToolPreview{
		Excluded: make(map[string]string),
		LeanMode: tf.leanMode,
	}
	if tf.platforms != nil {
		out.Platform = tf.platforms()
	}
	if out.Platform == "" {
		out.Platform = "cli"
	}
	if tf.platform != nil {
		out.Toolsets = append([]string(nil), tf.platform.ToolsetNames(out.Platform)...)
	}
	out.PlanMode = tf.planModeChecker != nil && tf.planModeChecker()

	if tf.filter == nil || len(allToolNames) == 0 {
		out.Kept = append([]string(nil), allToolNames...)
		sort.Strings(out.Kept)
		return out
	}

	result, err := tf.filter.Filter(out.Toolsets, tf.availability, allToolNames)
	if err != nil {
		result = &FilterResult{Tools: allToolNames}
	}
	for name, reason := range result.Excluded {
		out.Excluded[name] = reason
	}

	allowed := make(map[string]bool, len(result.Tools))
	for _, name := range result.Tools {
		allowed[name] = true
	}
	for _, name := range allToolNames {
		if !allowed[name] {
			if _, ok := out.Excluded[name]; !ok {
				out.Excluded[name] = "not in active toolsets"
			}
			continue
		}
		if out.PlanMode && !tf.planModeAllowed[name] {
			out.Excluded[name] = "plan mode"
			continue
		}
		if tf.nameFilter != nil && !tf.nameFilter(name) {
			out.Excluded[name] = "name filter"
			continue
		}
		if tf.contextFilter != nil && !tf.contextFilter(context.Background(), name) {
			out.Excluded[name] = "role policy"
			continue
		}
		if tf.leanMode && !tf.leanVisible(name) {
			out.Excluded[name] = "lean"
			continue
		}
		if CyberOnlyTools[name] && !tf.cyberMode {
			out.Excluded[name] = "cyber mode"
			continue
		}
		out.Kept = append(out.Kept, name)
	}
	sort.Strings(out.Kept)
	return out
}

func (tf *ToolsetFilter) LeanMode() bool { return tf.leanMode }

// --- Delegation Scoping ---

// DelegateToolsets computes the toolset names for a child agent based on the
// parent's current toolsets. This prevents child agents from accessing tools
// the parent doesn't have.
//
// Always stripped from child toolsets:
//   - "delegation" (prevents infinite recursion)
//   - "clarify" (child cannot ask user directly)
//
// If restrictToParent is true, the child's toolsets are intersected with the
// parent's resolved tools.
func DelegateToolsets(parentToolsets []string, childRequested []string, restrictToParent bool) []string {
	if !restrictToParent {
		// Still strip dangerous toolsets
		return stripDangerous(childRequested)
	}

	// Resolve parent toolsets to tool names
	parentTools, err := ResolveToolsets(parentToolsets)
	if err != nil {
		return stripDangerous(childRequested)
	}

	parentSet := make(map[string]bool)
	for _, t := range parentTools {
		parentSet[t] = true
	}

	// Resolve child toolsets to tool names
	childTools, err := ResolveToolsets(childRequested)
	if err != nil {
		return stripDangerous(childRequested)
	}

	// Intersect: only tools the parent has
	var scoped []string
	for _, t := range childTools {
		if parentSet[t] {
			scoped = append(scoped, t)
		}
	}

	// Remove dangerous tools from the result
	return stripDangerousFromList(scoped)
}

// DelegateToolNames computes the allowed tool names for a child agent by
// intersecting with the parent's available tools.
func DelegateToolNames(parentTools []string, childTools []string) []string {
	parentSet := make(map[string]bool)
	for _, t := range parentTools {
		parentSet[t] = true
	}

	// Tools always stripped from delegation
	blocked := map[string]bool{
		"clarify": true,
	}

	var result []string
	for _, t := range childTools {
		if parentSet[t] && !blocked[t] {
			result = append(result, t)
		}
	}

	sort.Strings(result)
	return result
}

var dangerousToolsets = map[string]bool{
	"delegation": true,
}

var dangerousTools = map[string]bool{
	"clarify": true,
}

func stripDangerous(names []string) []string {
	var result []string
	for _, n := range names {
		if !dangerousToolsets[n] {
			result = append(result, n)
		}
	}
	return result
}

func stripDangerousFromList(tools []string) []string {
	var result []string
	for _, t := range tools {
		if !dangerousTools[t] {
			result = append(result, t)
		}
	}
	return result
}

// DefaultPlanModeAllowedTools defines the set of tools allowed in Plan mode.
// This mirrors agent.PlanModeAllowedTools but is duplicated here to avoid
// a circular dependency (toolset → agent).
var DefaultPlanModeAllowedTools = map[string]bool{
	// Read-only file/search tools
	"read": true, "grep": true, "glob": true, "ls": true,
	"search_files": true,
	"web_search":   true, "web_fetch": true, "open_url": true,
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

// LeanDirectTools are the schemas always sent to the model when lean mode is
// on. Everything else stays in the registry and is reachable through
// tool_search / tool_describe / tool_call (including MCP).
var LeanDirectTools = map[string]bool{
	"read": true, "edit": true, "write_file": true, "ls": true,
	"search_files": true, "glob": true, "grep": true,
	"bash": true, "git_status": true, "git_diff": true, "git_log": true,
	"todo": true, "update_plan": true, "clarify": true, "ask_user": true,
	"exit_plan_mode": true, "diffs": true, "apply_patch": true, "edit_block": true,
	"sessions_spawn": true, "sessions_wait": true,
	"tool_search": true, "tool_describe": true, "tool_call": true,
}

// CyberOnlyTools are never shown or callable outside ModeCyber.
var CyberOnlyTools = map[string]bool{
	"scan":              true,
	"finding_upsert":    true,
	"finding_retest":    true,
	"cyber_storage":     true,
	"oast":              true,
	"finding_triage":    true,
	"asset_upsert":      true,
	"coverage_upsert":   true,
	"exchange_list":     true,
	"exchange_get":      true,
	"web_context":       true,
	"web_access_matrix": true,
	"web_workflow":      true,
	"web_test_execute":  true,
	"csrf_retest":       true,
	"exchange_label":    true,
	"exchange_search":   true,
	"exchange_replay":   true,
	"exchange_compare":  true,
	"web_param_probe":   true,
	"request_intake":    true,
	"assessment":        true,
	"remote_nodes":      true,
	"remote_node_exec":  true,
	"threat_model":      true,
	"diff_scope":        true,
	"scan_profile":      true,
	"finish_scan":       true,
}

// LeanCyberTools stay visible in lean mode so cyber recon does not have to
// round-trip through tool_search.
var LeanCyberTools = map[string]bool{
	"web_fetch": true, "web_search": true, "open_url": true,
	"scan": true, "finding_upsert": true, "finding_triage": true, "asset_upsert": true,
	"finding_retest":  true,
	"oast":            true,
	"coverage_upsert": true, "exchange_list": true, "exchange_get": true,
	"web_context":       true,
	"web_access_matrix": true,
	"web_workflow":      true,
	"web_test_execute":  true,
	"csrf_retest":       true,
	"exchange_label":    true, "exchange_search": true, "exchange_replay": true,
	"exchange_compare": true,
	"web_param_probe":  true,
	"request_intake":   true, "assessment": true, "remote_nodes": true,
	"remote_node_exec": true,
	"threat_model":     true, "diff_scope": true,
	"scan_profile": true, "finish_scan": true,
}

func (tf *ToolsetFilter) leanVisible(name string) bool {
	if LeanDirectTools[name] {
		return true
	}
	return tf != nil && tf.leanCyber && LeanCyberTools[name]
}

// SetPlanModeChecker sets the function used to check if the agent is in
// Plan mode. This is used for late binding after the ToolsetFilter is
// constructed, since the CovoAgent may not exist yet at construction time.
func (tf *ToolsetFilter) SetPlanModeChecker(fn func() bool) {
	tf.planModeChecker = fn
}
