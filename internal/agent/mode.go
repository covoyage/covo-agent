package agent

import (
	"sort"
	"strings"
	"sync"
)

type AgentMode string

const (
	ModeGeneral AgentMode = "general"
	ModeCode    AgentMode = "code"
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
	names := []string{string(ModeGeneral), string(ModeCode)}
	for _, def := range ListCustomModes() {
		names = append(names, def.Name)
	}
	return names
}

func (m AgentMode) Valid() bool {
	if m == ModeGeneral || m == ModeCode {
		return true
	}
	return GetCustomMode(string(m)) != nil
}

func (m AgentMode) String() string {
	return string(m)
}

func (m AgentMode) IsCustom() bool {
	return m != ModeGeneral && m != ModeCode && m.Valid()
}

func ParseMode(s string) (AgentMode, bool) {
	m := AgentMode(strings.ToLower(s))
	return m, m.Valid()
}

// ExecutionPhase represents the Plan/Act execution phase.
// This is orthogonal to AgentMode (general/code) — both modes can enter
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
