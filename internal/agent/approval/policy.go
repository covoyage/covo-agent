package approval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/covoyage/covo-agent/internal/cli"
	"gopkg.in/yaml.v3"
)

type RuleAction string

const (
	RuleAllow RuleAction = "allow"
	RuleDeny  RuleAction = "deny"
)

type rule struct {
	action  RuleAction
	pattern string
}

type PolicyEngine struct {
	mu    sync.RWMutex
	rules []rule
}

type policyFile struct {
	Allow []string `yaml:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty"`
	Tools *toolPolicyFile `yaml:"tools,omitempty"`
	Paths *pathPolicyFile `yaml:"paths,omitempty"`
}

// toolPolicyFile defines tool-level allow/deny rules using group aliases.
// Group aliases (e.g. "edit") expand to all concrete tool names in that group.
// Individual tool names (e.g. "edit_block") also work.
//
// Example YAML:
//
//	tools:
//	  deny:
//	    - edit      # blocks all file-mutation tools
//	    - bash      # blocks bash/process
//	  allow:
//	    - read      # explicitly allows read-only tools
type toolPolicyFile struct {
	Allow []string `yaml:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty"`
}

// toolGroupRules holds compiled tool-level rules after group expansion.
type toolGroupRules struct {
	allow map[string]bool // expanded tool names that are explicitly allowed
	deny  map[string]bool // expanded tool names that are explicitly denied
}

// toolGroups maps a group alias to the concrete tool names it covers.
// This lets users write "edit" in policy.yaml instead of listing every
// file-mutation tool variant.
var toolGroups = map[string][]string{
	"edit": {"write_file", "write", "edit_block", "edit", "apply_patch", "patch", "move", "delete_file", "str_replace_editor"},
	"bash": {"bash", "process"},
	"net":  {"web_fetch", "web_search", "browser"},
	"git":  {"git_commit", "git_push"},
	"read": {"read", "read_file", "glob", "grep", "ls"},
}

var globalPolicy = &PolicyEngine{}

// globalToolPolicy holds the compiled tool-level rules loaded from policy.yaml.
var globalToolPolicy = &toolPolicyRulesStore{}

type toolPolicyRulesStore struct {
	mu    sync.RWMutex
	rules toolGroupRules
}

func LoadPolicy(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read policy: %w", err)
	}
	var pf policyFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return fmt.Errorf("parse policy: %w", err)
	}
	var rules []rule
	for _, p := range pf.Allow {
		rules = append(rules, rule{action: RuleAllow, pattern: p})
	}
	for _, p := range pf.Deny {
		rules = append(rules, rule{action: RuleDeny, pattern: p})
	}
	globalPolicy.mu.Lock()
	globalPolicy.rules = rules
	globalPolicy.mu.Unlock()

	// Compile tool-level rules from the tools section.
	if pf.Tools != nil {
		compiled := toolGroupRules{
			allow: expandToolNames(pf.Tools.Allow),
			deny:  expandToolNames(pf.Tools.Deny),
		}
		globalToolPolicy.mu.Lock()
		globalToolPolicy.rules = compiled
		globalToolPolicy.mu.Unlock()
	}

	// Compile path-based rules from the paths section.
	// This adds glob-based permission matching: Edit(**/*.rs), Read(src/**), etc.
	loadPathRules(pf.Paths)
	return nil
}

// expandToolNames takes a list of group aliases and/or individual tool names
// and returns the set of concrete tool names they cover.
func expandToolNames(entries []string) map[string]bool {
	result := make(map[string]bool)
	for _, entry := range entries {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		// If it's a known group alias, expand it.
		if members, ok := toolGroups[entry]; ok {
			for _, m := range members {
				result[m] = true
			}
		} else {
			// Treat as an individual tool name.
			result[entry] = true
		}
	}
	return result
}

func LoadPolicyFromDirs(dirs ...string) {
	if !cli.IsEnabled("exec-policy") {
		return
	}
	for _, dir := range dirs {
		path := filepath.Join(dir, "policy.yaml")
		if _, err := os.Stat(path); err == nil {
			LoadPolicy(path)
			return
		}
		path = filepath.Join(dir, ".covo-agent-policy.yaml")
		if _, err := os.Stat(path); err == nil {
			LoadPolicy(path)
			return
		}
	}
}

func (pe *PolicyEngine) check(command string) (RuleAction, string, bool) {
	pe.mu.RLock()
	defer pe.mu.RUnlock()
	if len(pe.rules) == 0 {
		return "", "", false
	}
	cmd := strings.TrimSpace(command)
	// Deny takes precedence: scan all deny rules first, then allow rules.
	// This ensures that a command matching both an allow and a deny pattern
	// is always denied.
	for _, r := range pe.rules {
		if r.action == RuleDeny && matchPolicy(cmd, r.pattern) {
			return r.action, r.pattern, true
		}
	}
	for _, r := range pe.rules {
		if r.action == RuleAllow && matchPolicy(cmd, r.pattern) {
			return r.action, r.pattern, true
		}
	}
	return "", "", false
}

func matchPolicy(command, pattern string) bool {
	if strings.HasPrefix(pattern, "/") {
		return matchPathGlob(command, pattern)
	}
	if strings.Contains(pattern, " ") {
		return matchGlob(command, pattern)
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	firstWord := fields[0]
	return matchGlob(firstWord, pattern)
}

func matchGlob(text, pattern string) bool {
	matched, err := filepath.Match(pattern, text)
	if err != nil {
		return false
	}
	return matched
}

func matchPathGlob(command, pattern string) bool {
	for _, field := range strings.Fields(command) {
		if matchGlob(field, pattern) {
			return true
		}
	}
	return false
}

func (s *System) CheckPolicy(command string) *Decision {
	if !cli.IsEnabled("exec-policy") {
		return nil
	}
	action, pattern, matched := globalPolicy.check(command)
	if !matched {
		return nil
	}
	switch action {
	case RuleDeny:
		s.logger.Warn("approval: policy denied", "pattern", pattern, "command_preview", truncate(command, 200))
		return &Decision{
			Approved:    false,
			Description: fmt.Sprintf("BLOCKED by policy rule: %s %s", action, pattern),
			Message:     fmt.Sprintf("BLOCKED by policy rule: %s %s", action, pattern),
		}
	case RuleAllow:
		s.logger.Debug("approval: policy allowed", "pattern", pattern, "command_preview", truncate(command, 200))
		return &Decision{
			Approved: true,
		}
	}
	return nil
}

// CheckToolPolicy evaluates a tool-level policy for the given tool name.
// It resolves the tool name against group aliases (e.g. "edit" covers
// edit_block, write_file, apply_patch, etc.) and returns a Decision if a
// deny or allow rule matches. Returns nil if no tool-level policy is
// configured or the tool doesn't match any rule.
//
// Deny takes precedence over allow — if a tool is in both lists, it's denied.
func (s *System) CheckToolPolicy(toolName string) *Decision {
	if !cli.IsEnabled("exec-policy") {
		return nil
	}
	toolName = strings.ToLower(toolName)
	globalToolPolicy.mu.RLock()
	rules := globalToolPolicy.rules
	globalToolPolicy.mu.RUnlock()

	if len(rules.deny) == 0 && len(rules.allow) == 0 {
		return nil
	}

	// Deny takes precedence.
	if rules.deny[toolName] {
		s.logger.Warn("approval: tool policy denied", "tool", toolName)
		return &Decision{
			Approved:    false,
			Description: fmt.Sprintf("BLOCKED by tool policy: %s is denied", toolName),
			Message:     fmt.Sprintf("BLOCKED by tool policy: %s is denied", toolName),
		}
	}
	if rules.allow[toolName] {
		s.logger.Debug("approval: tool policy allowed", "tool", toolName)
		return &Decision{Approved: true}
	}
	return nil
}
