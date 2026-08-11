package subagent

import "strings"

// CapabilityMode is a coarse-grained tool-access filter for spawned subagents.
// It provides a simpler alternative to specifying individual toolsets:
//
//   - read-only:  filesystem (read), search, git (read), web — no mutations
//   - read-write: read-only + file editing, write — no shell execution
//   - execute:    read-write + shell/bash — full power except delegation
//   - all:        everything the parent has (including delegation if orchestrator)
type CapabilityMode string

const (
	CapModeReadOnly  CapabilityMode = "read-only"
	CapModeReadWrite CapabilityMode = "read-write"
	CapModeExecute   CapabilityMode = "execute"
	CapModeAll       CapabilityMode = "all"
)

// ValidCapabilityModes is the ordered list of supported modes.
var ValidCapabilityModes = []CapabilityMode{
	CapModeReadOnly,
	CapModeReadWrite,
	CapModeExecute,
	CapModeAll,
}

// capabilityToolsets maps each mode to the toolset names it grants.
// These are intersected with the parent's available toolsets at spawn time.
var capabilityToolsets = map[CapabilityMode][]string{
	CapModeReadOnly: {
		"filesystem", // read_file, ls, glob — the read-only subset
		"search",     // grep, codebase_search
		"git",        // git_log, git_diff, git_status (read-only git ops)
		"web",        // web_search, web_fetch
	},
	CapModeReadWrite: {
		"filesystem",
		"editing",    // write_file, edit, str_replace
		"search",
		"git",
		"web",
	},
	CapModeExecute: {
		"filesystem",
		"editing",
		"shell",      // bash, process management
		"search",
		"git",
		"web",
	},
	CapModeAll: {}, // empty = inherit everything from parent
}

// ParseCapabilityMode parses a string into a CapabilityMode.
// Returns false if the string is not a valid mode.
func ParseCapabilityMode(s string) (CapabilityMode, bool) {
	mode := CapabilityMode(strings.ToLower(strings.TrimSpace(s)))
	for _, m := range ValidCapabilityModes {
		if m == mode {
			return mode, true
		}
	}
	return "", false
}

// ToolsetsForMode returns the toolset names granted by the given capability mode.
// For CapModeAll, returns nil (meaning "inherit all from parent").
func ToolsetsForMode(mode CapabilityMode) []string {
	ts, ok := capabilityToolsets[mode]
	if !ok {
		return nil
	}
	return append([]string(nil), ts...)
}

// ResolveCapabilityMode resolves a capability_mode parameter into concrete
// toolsets. If capabilityMode is empty or "all", returns nil (inherit parent).
// Otherwise returns the toolsets for that mode, which will be intersected
// with the parent's toolsets by the caller.
func ResolveCapabilityMode(capabilityMode string, explicitToolsets []string) []string {
	// Explicit toolsets always take precedence over capability_mode.
	if len(explicitToolsets) > 0 {
		return explicitToolsets
	}

	if capabilityMode == "" {
		return nil // not specified, inherit parent
	}

	mode, ok := ParseCapabilityMode(capabilityMode)
	if !ok {
		return nil // invalid mode, inherit parent
	}

	return ToolsetsForMode(mode)
}
