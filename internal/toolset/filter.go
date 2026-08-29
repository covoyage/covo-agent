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
	toolNames       func() []string // returns all registered tool names from the registry
	platforms       func() string   // returns current platform name
	planModeChecker func() bool     // returns true when agent is in Plan mode
	planModeAllowed map[string]bool // tools allowed in Plan mode
	logger          *slog.Logger
}

// ToolsetFilterConfig configures a ToolsetFilter.
type ToolsetFilterConfig struct {
	Platform        *PlatformToolsets
	Availability    *ToolAvailability
	Filter          *CachedFilter
	ToolNames       func() []string
	PlatformName    func() string
	PlanModeChecker func() bool     // returns true when agent is in Plan mode
	PlanModeAllowed map[string]bool // tools allowed in Plan mode (overrides filtering)
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
func (tf *ToolsetFilter) BeforeModelCall(_ context.Context, _ *agentcore.AgentRunContext, mcc *agentcore.ModelCallContext) error {
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
		tf.logger.Warn("toolset filter error, using all tools", "error", err)
		return nil // don't block the call, just use all tools
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

// SetPlanModeChecker sets the function used to check if the agent is in
// Plan mode. This is used for late binding after the ToolsetFilter is
// constructed, since the CovoAgent may not exist yet at construction time.
func (tf *ToolsetFilter) SetPlanModeChecker(fn func() bool) {
	tf.planModeChecker = fn
}
