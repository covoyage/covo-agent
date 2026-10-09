package agent

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type AgentMode string

const (
	ModeGeneral AgentMode = "general"
	ModeCode    AgentMode = "code"
	ModeCyber   AgentMode = "cyber"
)

// CustomModeDefinition holds the runtime representation of a custom mode.
type CustomModeDefinition struct {
	Name         string
	Description  string
	SystemPrompt string
	AllowTools   []string
	DenyTools    []string
}

// customModeRegistry holds user-defined modes registered at startup.
var (
	customModeMu       sync.RWMutex
	customModeRegistry = make(map[string]*CustomModeDefinition)
)

// RegisterCustomMode adds a user-defined mode to the global registry.
// This should be called during agent initialization before any mode
// validation occurs.
func RegisterCustomMode(def *CustomModeDefinition) {
	if def == nil || def.Name == "" {
		return
	}
	customModeMu.Lock()
	defer customModeMu.Unlock()
	customModeRegistry[strings.ToLower(def.Name)] = def
}

// ClearCustomModes removes all registered custom modes (used in tests).
func ClearCustomModes() {
	customModeMu.Lock()
	defer customModeMu.Unlock()
	customModeRegistry = make(map[string]*CustomModeDefinition)
}

// GetCustomMode returns the custom mode definition for the given name,
// or nil if no such custom mode is registered.
func GetCustomMode(name string) *CustomModeDefinition {
	customModeMu.RLock()
	defer customModeMu.RUnlock()
	return customModeRegistry[strings.ToLower(name)]
}

// ListCustomModes returns all registered custom mode definitions sorted by name.
func ListCustomModes() []*CustomModeDefinition {
	customModeMu.RLock()
	defer customModeMu.RUnlock()
	result := make([]*CustomModeDefinition, 0, len(customModeRegistry))
	for _, def := range customModeRegistry {
		result = append(result, def)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// AllModeNames returns built-in plus custom mode names.
func AllModeNames() []string {
	names := []string{string(ModeGeneral), string(ModeCode), string(ModeCyber)}
	for _, def := range ListCustomModes() {
		names = append(names, def.Name)
	}
	return names
}

func (m AgentMode) Valid() bool {
	if m == ModeGeneral || m == ModeCode || m == ModeCyber {
		return true
	}
	return GetCustomMode(string(m)) != nil
}

func (m AgentMode) String() string {
	return string(m)
}

func (m AgentMode) IsCustom() bool {
	return m != ModeGeneral && m != ModeCode && m != ModeCyber && m.Valid()
}

func ParseMode(s string) (AgentMode, bool) {
	m := AgentMode(strings.ToLower(strings.TrimSpace(s)))
	return m, m.Valid()
}

// RequireMode parses a mode name. Empty values default to general; unknown
// names fail instead of silently falling back.
func RequireMode(s string) (AgentMode, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ModeGeneral, nil
	}
	m, ok := ParseMode(s)
	if !ok {
		return "", fmt.Errorf("invalid mode %q: use %s", s, strings.Join(AllModeNames(), ", "))
	}
	return m, nil
}

// LeanDefault reports whether lean tool schemas should be on for this mode
// when tools.lean / COVO_TOOLS_LEAN is unset.
func (m AgentMode) LeanDefault() bool {
	return m == ModeCode || m.IsCyberFamily()
}

// IsCyberFamily reports whether the mode runs the cyber pipeline: the
// built-in cyber mode or a registered cyber scenario preset.
func (m AgentMode) IsCyberFamily() bool {
	return m == ModeCyber
}

// ExecutionPhase represents the Plan/Act execution phase.
// This is orthogonal to AgentMode (general/code/cyber) — all modes can enter
// Plan or Act phase. In Plan mode, mutating tools are blocked by
// planModeGateBeforeHook and filtered from the LLM's tool list by the
// toolset filter. The only way to exit Plan mode is through the
// exit_plan_mode tool (which requires user approval) or the /act command.
type ExecutionPhase string

const (
	// PhasePlan restricts the agent to read-only tools. The agent can
	// inspect the codebase, search files, and present a plan, but cannot
	// write files, run bash commands, or perform any mutating operations.
	PhasePlan ExecutionPhase = "plan"

	// PhaseAct is the default execution phase where all tools are available.
	// The agent can read, write, execute commands, and perform any operation
	// permitted by its AgentMode and toolset configuration.
	PhaseAct ExecutionPhase = "act"
)

func (p ExecutionPhase) Valid() bool {
	return p == PhasePlan || p == PhaseAct
}

func (p ExecutionPhase) String() string {
	return string(p)
}
