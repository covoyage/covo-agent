package toolset

import (
	"fmt"
	"sort"
	"sync"
)

// ToolsetDef defines a named group of tools.
type ToolsetDef struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tools       []string `json:"tools"`
	Includes    []string `json:"includes,omitempty"` // composite: references to other toolsets
}

// Toolsets is the master registry of all toolset definitions.
// Composite toolsets reference others via the Includes field.
var Toolsets = map[string]ToolsetDef{
	// --- Leaf toolsets ---
	"filesystem": {
		Name:        "filesystem",
		Description: "File and directory operations",
		Tools:       []string{"read", "edit", "write_file", "append_file", "ls", "view", "delete", "move", "glob", "edit_block"},
	},
	"editing": {
		Name:        "editing",
		Description: "File editing tools",
		Tools:       []string{"edit", "write_file", "append_file"},
	},
	"search": {
		Name:        "search",
		Description: "Codebase search tools",
		Tools:       []string{"grep", "find", "code_navigate", "search_files"},
	},
	"shell": {
		Name:        "shell",
		Description: "Shell command execution",
		Tools:       []string{"bash", "process", "monitor"},
	},
	"git": {
		Name:        "git",
		Description: "Git version control",
		Tools:       []string{"git_status", "git_diff", "git_log"},
	},
	"web": {
		Name:        "web",
		Description: "Web search and content extraction",
		Tools:       []string{"web_fetch"},
	},
	"vision": {
		Name:        "vision",
		Description: "Image analysis and vision tools",
		Tools:       []string{"vision_analyze"},
	},
	"patch": {
		Name:        "patch",
		Description: "Patch application",
		Tools:       []string{"patch", "apply_patch"},
	},
	"memory": {
		Name:        "memory",
		Description: "Persistent memory management",
		Tools:       []string{"memory"},
	},
	"skills": {
		Name:        "skills",
		Description: "Skill loading, bundles, config, scripts, proposals, and management",
		Tools:       []string{"skill", "skill_manage", "skill_bundle", "skill_config", "skill_script", "skill_workshop"},
	},
	"productivity": {
		Name:        "productivity",
		Description: "Session search, task tracking, structured planning, scheduling, LLM task, and desktop notifications",
		Tools:       []string{"session_search", "todo", "clarify", "cronjob", "update_plan", "exit_plan_mode", "send_message", "human_handoff", "llm_task"},
	},
	"media": {
		Name:        "media",
		Description: "Text-to-speech, image/video/music generation",
		Tools:       []string{"tts", "image_generate", "video_generate", "music_generate"},
	},
	"documents": {
		Name:        "documents",
		Description: "Document analysis and extraction",
		Tools:       []string{"pdf"},
	},
	"review": {
		Name:        "review",
		Description: "Code review and diff tools",
		Tools:       []string{"diffs"},
	},
	"delegation": {
		Name:        "delegation",
		Description: "Sub-agent spawning and delegation",
		Tools:       []string{"sessions_spawn"},
	},
	"browser": {
		Name:        "browser",
		Description: "Browser automation for specific URLs, interactive pages, login flows, and JavaScript-heavy pages. Not a replacement for web search.",
		Tools:       []string{"browser"},
	},
	"code_execution": {
		Name:        "code_execution",
		Description: "Execute Python code in a sandboxed environment for data processing, analysis, and computation",
		Tools:       []string{"execute_code"},
	},
	"computer_use": {
		Name:        "computer_use",
		Description: "macOS desktop control: screenshots, clicks, typing, and app management (macOS only)",
		Tools:       []string{"computer_use"},
	},
	"mcp": {
		Name:        "mcp",
		Description: "MCP (Model Context Protocol) server management and tool calling",
		Tools:       []string{"mcp"},
	},
	"discovery": {
		Name:        "discovery",
		Description: "Dynamic tool discovery: search, describe, and call tools by name",
		Tools:       []string{"tool_search", "tool_describe", "tool_call"},
	},

	// --- Composite toolsets ---
	"coding": {
		Name:        "coding",
		Description: "Full coding toolkit (filesystem + search + shell + git + patch + documents + review + code_execution)",
		Tools:       []string{},
		Includes:    []string{"filesystem", "search", "shell", "git", "patch", "documents", "review", "code_execution"},
	},
	"creative": {
		Name:        "creative",
		Description: "Creative toolkit (media + web + vision)",
		Tools:       []string{},
		Includes:    []string{"media", "web", "vision"},
	},
	"full": {
		Name:        "full",
		Description: "All available tools",
		Tools:       []string{},
		Includes:    []string{"coding", "creative", "memory", "skills", "productivity", "delegation", "browser", "computer_use", "mcp", "discovery"},
	},
}

// PlatformToolsets maps deployment platforms to their default toolset lists.
type PlatformToolsets struct {
	mu        sync.RWMutex
	platform  string              // current platform (e.g. "cli", "tui", "api")
	overrides map[string][]string // user overrides: platform -> toolset names
}

// NewPlatformToolsets creates a platform toolset resolver.
func NewPlatformToolsets(platform string) *PlatformToolsets {
	return &PlatformToolsets{
		platform:  platform,
		overrides: make(map[string][]string),
	}
}

// SetOverride sets user-defined toolsets for a platform.
func (p *PlatformToolsets) SetOverride(platform string, names []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.overrides[platform] = names
}

// ToolsetNames returns the active toolset names for the given platform.
// Priority: user override > platform default > "full".
func (p *PlatformToolsets) ToolsetNames(platform string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if override, ok := p.overrides[platform]; ok {
		return override
	}
	return PlatformDefaults(platform)
}

// PlatformDefaults returns the default toolset names for a platform.
func PlatformDefaults(platform string) []string {
	switch platform {
	case "code":
		return []string{"coding", "web", "memory", "skills", "productivity"}
	case "minimal":
		return []string{"filesystem", "search", "shell"}
	default:
		return []string{"full"}
	}
}

// --- Resolver ---

// ResolveToolsets expands toolset names recursively, handling composite includes
// with cycle detection and diamond-dependency dedup.
func ResolveToolsets(names []string) ([]string, error) {
	seen := make(map[string]bool)            // cycle detection
	toolsetResolved := make(map[string]bool) // tracks which toolsets have been expanded
	toolSeen := make(map[string]bool)        // tracks which tools have been added (dedup)
	var order []string                       // preserve insertion order

	var resolve func(name string) error
	resolve = func(name string) error {
		if toolsetResolved[name] {
			return nil
		}
		if seen[name] {
			return fmt.Errorf("cycle detected in toolset includes: %s", name)
		}
		seen[name] = true

		def, ok := Toolsets[name]
		if !ok {
			return fmt.Errorf("unknown toolset: %s", name)
		}

		// Resolve includes first (depth-first)
		for _, inc := range def.Includes {
			if err := resolve(inc); err != nil {
				return err
			}
		}

		// Add this toolset's own tools
		for _, t := range def.Tools {
			if !toolSeen[t] {
				toolSeen[t] = true
				order = append(order, t)
			}
		}

		delete(seen, name)
		toolsetResolved[name] = true
		return nil
	}

	for _, name := range names {
		if err := resolve(name); err != nil {
			return nil, err
		}
	}

	return order, nil
}

// ResolveToolsetsForMode is a convenience wrapper for mode-based resolution.
func ResolveToolsetsForMode(mode string) ([]string, error) {
	return ResolveToolsets(PlatformDefaults(mode))
}

// --- Filtering ---

// CheckFn is a function that checks whether a tool is currently available.
// Returns ("", true) if available, or (reason, false) if unavailable.
type CheckFn func() (reason string, available bool)

// ToolAvailability tracks the availability status of tools.
type ToolAvailability struct {
	mu       sync.RWMutex
	checks   map[string]CheckFn // tool name -> check function
	cache    map[string]bool    // cached results
	cacheAge map[string]int64   // generation counter for TTL
	gen      int64              // global generation counter
}

// NewToolAvailability creates a new availability tracker.
func NewToolAvailability() *ToolAvailability {
	return &ToolAvailability{
		checks:   make(map[string]CheckFn),
		cache:    make(map[string]bool),
		cacheAge: make(map[string]int64),
	}
}

// RegisterCheck registers a check function for a tool.
func (ta *ToolAvailability) RegisterCheck(name string, fn CheckFn) {
	ta.mu.Lock()
	defer ta.mu.Unlock()
	ta.checks[name] = fn
	delete(ta.cache, name) // invalidate cache for this tool
}

// IsAvailable checks if a tool is available, using cache with TTL.
func (ta *ToolAvailability) IsAvailable(name string) (string, bool) {
	ta.mu.RLock()
	fn, hasCheck := ta.checks[name]
	if cached, ok := ta.cache[name]; ok {
		if ta.gen-ta.cacheAge[name] < 50 { // TTL: 50 calls
			ta.mu.RUnlock()
			if cached {
				return "", true
			}
			return "tool unavailable", false
		}
	}
	ta.mu.RUnlock()

	if !hasCheck {
		return "", true // no check = always available
	}

	reason, available := fn()

	ta.mu.Lock()
	ta.cache[name] = available
	ta.cacheAge[name] = ta.gen
	ta.gen++
	ta.mu.Unlock()

	if available {
		return "", true
	}
	if reason == "" {
		reason = "tool unavailable"
	}
	return reason, false
}

// InvalidateCache clears the availability cache.
func (ta *ToolAvailability) InvalidateCache() {
	ta.mu.Lock()
	defer ta.mu.Unlock()
	ta.cache = make(map[string]bool)
	ta.gen++
}

// --- Filtered Definitions ---

// FilterResult contains the result of filtering tools through toolsets.
type FilterResult struct {
	Tools    []string          // tool names that passed all filters
	Excluded map[string]string // tool name -> reason for exclusion
	Resolved []string          // all resolved tool names before availability check
}

// FilterTools resolves toolsets, applies platform filtering, and checks availability.
// Tool names may be mixed in with toolset names — they are passed through directly.
func FilterTools(
	toolsetNames []string,
	availability *ToolAvailability,
	allToolNames []string,
) (*FilterResult, error) {
	// Step 0: Separate known toolsets from raw tool names
	var tsNames []string
	var directTools []string
	allToolSet := make(map[string]bool, len(allToolNames))
	for _, t := range allToolNames {
		allToolSet[t] = true
	}
	for _, name := range toolsetNames {
		if _, isToolset := Toolsets[name]; isToolset {
			tsNames = append(tsNames, name)
		} else if allToolSet[name] {
			directTools = append(directTools, name)
		}
		// Unknown names (neither toolset nor tool) are silently dropped
	}

	// Step 1: Resolve composite toolsets
	resolved, err := ResolveToolsets(tsNames)
	if err != nil {
		return nil, fmt.Errorf("resolve toolsets: %w", err)
	}
	resolved = append(resolved, directTools...)

	// Build set of resolved tools
	resolvedSet := make(map[string]bool)
	for _, t := range resolved {
		resolvedSet[t] = true
	}

	// Step 2: Intersect with actually registered tools
	var filtered []string
	excluded := make(map[string]string)

	for _, name := range allToolNames {
		if !resolvedSet[name] {
			excluded[name] = "not in active toolsets"
			continue
		}

		// Step 3: Check availability
		if availability != nil {
			if reason, ok := availability.IsAvailable(name); !ok {
				excluded[name] = reason
				continue
			}
		}

		filtered = append(filtered, name)
	}

	sort.Strings(filtered)

	return &FilterResult{
		Tools:    filtered,
		Excluded: excluded,
		Resolved: resolved,
	}, nil
}

// --- Cached Filter ---

// CachedFilter caches FilterResults keyed by toolset names + generation.
type CachedFilter struct {
	mu        sync.RWMutex
	cache     map[string]*FilterResult
	toolGen   int64 // incremented when tools are registered/unregistered
	filterGen int64 // incremented when availability changes
}

// NewCachedFilter creates a new cached filter.
func NewCachedFilter() *CachedFilter {
	return &CachedFilter{
		cache: make(map[string]*FilterResult),
	}
}

// InvalidateTools signals that the tool registry has changed.
func (cf *CachedFilter) InvalidateTools() {
	cf.mu.Lock()
	defer cf.mu.Unlock()
	cf.toolGen++
	cf.cache = make(map[string]*FilterResult) // clear all cache
}

// InvalidateAvailability signals that tool availability has changed.
func (cf *CachedFilter) InvalidateAvailability() {
	cf.mu.Lock()
	defer cf.mu.Unlock()
	cf.filterGen++
	cf.cache = make(map[string]*FilterResult)
}

// Filter returns a cached or freshly computed filter result.
func (cf *CachedFilter) Filter(
	toolsetNames []string,
	availability *ToolAvailability,
	allToolNames []string,
) (*FilterResult, error) {
	key := cacheKey(toolsetNames, cf.toolGen, cf.filterGen)

	cf.mu.RLock()
	if cached, ok := cf.cache[key]; ok {
		cf.mu.RUnlock()
		return cached, nil
	}
	cf.mu.RUnlock()

	result, err := FilterTools(toolsetNames, availability, allToolNames)
	if err != nil {
		return nil, err
	}

	cf.mu.Lock()
	cf.cache[key] = result
	cf.mu.Unlock()

	return result, nil
}

func cacheKey(names []string, toolGen, filterGen int64) string {
	sort.Strings(names)
	return fmt.Sprintf("%v|%d|%d", names, toolGen, filterGen)
}
