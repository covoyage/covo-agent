package approval

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/covoyage/covo-agent/internal/cli"
)

// PathRuleAction defines the action for a path-based rule.
// This implements a three-tier permission model: deny > ask > allow.
type PathRuleAction string

const (
	PathRuleDeny  PathRuleAction = "deny"
	PathRuleAsk   PathRuleAction = "ask"
	PathRuleAllow PathRuleAction = "allow"
)

// pathRule is a compiled path-based permission rule.
// It matches a tool name (with group expansion) against a file path glob.
// Example: Edit(**/*.pem) matches any edit tool operating on .pem files.
type pathRule struct {
	action    PathRuleAction
	toolGroup string // e.g. "edit", "read", "bash", or a specific tool name
	pathGlob  string // e.g. "**/*.pem", "src/**", or "" for no path constraint
}

// pathRuleSet holds compiled path rules loaded from policy.yaml.
type pathRuleSet struct {
	mu    sync.RWMutex
	rules []pathRule
}

var globalPathRules = &pathRuleSet{}

// pathPolicyFile defines the YAML structure for path-based rules.
// Rules use the format "ToolName(glob)" or "ToolName" (no path constraint).
// Group aliases (edit, read, bash, net, git) are expanded automatically.
//
// Example YAML:
//
//	paths:
//	  deny:
//	    - "Edit(**/*.pem)"       # deny editing PEM files
//	    - "Read(.env)"           # deny reading .env files
//	    - "Edit(src/secrets/**)" # deny editing secrets directory
//	  ask:
//	    - "Edit(**/*.lock)"      # ask before editing lock files
//	  allow:
//	    - "Read(src/**)"         # allow reading src directory
//	    - "Edit(src/**)"         # allow editing src directory
//	    - "MCPTool(server__*)"   # allow all MCP tools from "server"
type pathPolicyFile struct {
	Deny  []string `yaml:"deny,omitempty"`
	Ask   []string `yaml:"ask,omitempty"`
	Allow []string `yaml:"allow,omitempty"`
}

// loadPathRules compiles path-based rules from the policy file.
func loadPathRules(pf *pathPolicyFile) {
	if pf == nil {
		return
	}
	var compiled []pathRule

	for _, p := range pf.Deny {
		if r, ok := compilePathRule(p, PathRuleDeny); ok {
			compiled = append(compiled, r)
		}
	}
	for _, p := range pf.Ask {
		if r, ok := compilePathRule(p, PathRuleAsk); ok {
			compiled = append(compiled, r)
		}
	}
	for _, p := range pf.Allow {
		if r, ok := compilePathRule(p, PathRuleAllow); ok {
			compiled = append(compiled, r)
		}
	}

	globalPathRules.mu.Lock()
	globalPathRules.rules = compiled
	globalPathRules.mu.Unlock()
}

// compilePathRule parses a rule string like "Edit(**/*.pem)" into a pathRule.
// Format: ToolName(glob) or just ToolName (matches any path for that tool).
// Also supports "MCPTool(server__*)" for MCP tool matching.
func compilePathRule(s string, action PathRuleAction) (pathRule, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return pathRule{}, false
	}

	// Check for ToolName(glob) format.
	if idx := strings.IndexByte(s, '('); idx > 0 && strings.HasSuffix(s, ")") {
		toolPart := s[:idx]
		globPart := s[idx+1 : len(s)-1]
		toolPart = strings.ToLower(strings.TrimSpace(toolPart))
		globPart = strings.TrimSpace(globPart)
		return pathRule{
			action:    action,
			toolGroup: toolPart,
			pathGlob:  globPart,
		}, true
	}

	// No glob — just a tool name (or group alias).
	return pathRule{
		action:    action,
		toolGroup: strings.ToLower(s),
		pathGlob:  "",
	}, true
}

// CheckPathPolicy evaluates path-based rules for a tool call.
// toolName is the name of the tool being invoked.
// arguments is the raw JSON arguments of the tool call.
// Returns a Decision if a rule matches, or nil if no path rules apply.
//
// Evaluation order: deny > ask > allow. The first matching deny rule wins,
// then the first matching ask rule, then the first matching allow rule.
func (s *System) CheckPathPolicy(toolName string, arguments json.RawMessage) *Decision {
	if !cli.IsEnabled("exec-policy") {
		return nil
	}
	globalPathRules.mu.RLock()
	rules := globalPathRules.rules
	globalPathRules.mu.RUnlock()

	if len(rules) == 0 {
		return nil
	}

	toolName = strings.ToLower(toolName)

	// Extract file path from tool arguments.
	toolPath := extractToolPath(toolName, arguments)
	mcpToolName := extractMCPToolName(arguments)

	// Check in precedence order: deny > ask > allow.
	for _, action := range []PathRuleAction{PathRuleDeny, PathRuleAsk, PathRuleAllow} {
		for _, r := range rules {
			if r.action != action {
				continue
			}
			if !matchToolWithRule(toolName, toolPath, mcpToolName, r) {
				continue
			}
			switch action {
			case PathRuleDeny:
				return &Decision{
					Approved:    false,
					Description: fmt.Sprintf("BLOCKED by path policy: %s(%s)", r.toolGroup, r.pathGlob),
					Message:     fmt.Sprintf("BLOCKED by path policy: %s(%s)", r.toolGroup, r.pathGlob),
				}
			case PathRuleAllow:
				return &Decision{Approved: true}
			case PathRuleAsk:
				// "ask" means the tool should go through the TUI permission gate.
				// Return nil to fall through to the permission checker.
				return nil
			}
		}
	}

	return nil
}

// matchToolWithRule checks if a tool call matches a path rule.
func matchToolWithRule(toolName, toolPath, mcpToolName string, r pathRule) bool {
	// Handle MCPTool rules.
	if r.toolGroup == "mcp" || r.toolGroup == "mcptool" || r.toolGroup == "mcp_tool" {
		if mcpToolName == "" {
			return false
		}
		if r.pathGlob == "" {
			return true // matches any MCP tool
		}
		return matchDoubleStar(mcpToolName, r.pathGlob)
	}

	// Check if the tool matches the rule's tool group.
	if !toolMatchesGroup(toolName, r.toolGroup) {
		return false
	}

	// If the rule has no path glob, it matches any path for that tool group.
	if r.pathGlob == "" {
		return true
	}

	// If we couldn't extract a path, the rule can't match.
	if toolPath == "" {
		return false
	}

	return matchDoubleStar(toolPath, r.pathGlob)
}

// toolMatchesGroup checks if a tool name belongs to a group alias or matches directly.
func toolMatchesGroup(toolName, group string) bool {
	if toolName == group {
		return true
	}
	if members, ok := toolGroups[group]; ok {
		for _, m := range members {
			if m == toolName {
				return true
			}
		}
	}
	return false
}

// extractToolPath extracts the file path from common tool argument formats.
func extractToolPath(toolName string, args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}

	// Common field names for file paths across different tools.
	pathFields := []string{"path", "file_path", "filename", "file", "target_file", "filePath"}

	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}

	for _, field := range pathFields {
		if v, ok := m[field]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}

	// For edit/str_replace tools that use "command" with file paths.
	if cmd, ok := m["command"].(string); ok {
		// Try to extract path from commands like "str_replace --file path"
		for _, field := range []string{"--file", "-f", "--path"} {
			if idx := strings.Index(cmd, field); idx >= 0 {
				rest := strings.TrimSpace(cmd[idx+len(field):])
				if rest != "" {
					// Take the first token as the path.
					parts := strings.Fields(rest)
					if len(parts) > 0 {
						return parts[0]
					}
				}
			}
		}
	}

	return ""
}

// extractMCPToolName extracts the MCP tool name from arguments.
// MCP tools typically have a "name" or "tool_name" field.
func extractMCPToolName(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}

	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}

	for _, field := range []string{"tool_name", "name", "mcp_tool"} {
		if v, ok := m[field]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// matchDoubleStar matches a path against a glob pattern that may include
// the "**" wildcard (matching any number of path components).
// Falls back to filepath.Match for patterns without "**".
func matchDoubleStar(path, pattern string) bool {
	// Clean both path and pattern.
	path = filepath.Clean(path)
	pattern = filepath.Clean(pattern)

	// If pattern contains "**", use custom matching.
	if strings.Contains(pattern, "**") {
		return matchGlobRecursive(path, pattern)
	}

	// Standard glob match.
	matched, err := filepath.Match(pattern, path)
	if err != nil {
		return false
	}
	if matched {
		return true
	}

	// Also try matching just the basename (for patterns like "*.pem").
	matched, err = filepath.Match(pattern, filepath.Base(path))
	return err == nil && matched
}

// matchGlobRecursive implements "**" glob matching.
// "**" matches any number of path components (including zero).
func matchGlobRecursive(path, pattern string) bool {
	pathParts := strings.Split(path, string(filepath.Separator))
	patternParts := strings.Split(pattern, string(filepath.Separator))

	return matchParts(pathParts, patternParts)
}

func matchParts(pathParts, patternParts []string) bool {
	if len(patternParts) == 0 {
		return len(pathParts) == 0
	}

	if patternParts[0] == "**" {
		// "**" matches zero or more path components.
		// Try matching the rest of the pattern at every position.
		for i := 0; i <= len(pathParts); i++ {
			if matchParts(pathParts[i:], patternParts[1:]) {
				return true
			}
		}
		return false
	}

	if len(pathParts) == 0 {
		return false
	}

	// Match the current component.
	matched, err := filepath.Match(patternParts[0], pathParts[0])
	if err != nil || !matched {
		return false
	}

	return matchParts(pathParts[1:], patternParts[1:])
}

// resetPathRules clears all path rules (for testing).
func resetPathRules() {
	globalPathRules.mu.Lock()
	globalPathRules.rules = nil
	globalPathRules.mu.Unlock()
}
