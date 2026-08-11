package tools

import (
	"sync"
)

type toolMetrics struct {
	mu         sync.Mutex
	toolFails  map[string]int
	lastResult map[string]string
	exactFails map[string]int
}

func newToolMetrics() *toolMetrics {
	return &toolMetrics{
		toolFails:  make(map[string]int),
		lastResult: make(map[string]string),
		exactFails: make(map[string]int),
	}
}

type GuardrailResult int

const (
	GuardOK    GuardrailResult = iota
	GuardWarn  GuardrailResult = iota
	GuardBlock GuardrailResult = iota
)

func (r GuardrailResult) String() string {
	switch r {
	case GuardOK:
		return "ok"
	case GuardWarn:
		return "warn"
	case GuardBlock:
		return "block"
	default:
		return "unknown"
	}
}

type GuardrailConfig struct {
	ExactFailureWarnAfter  int
	ExactFailureBlockAfter int
	SameToolWarnAfter      int
	SameToolBlockAfter     int
	NoProgressWarnAfter    int
	NoProgressBlockAfter   int
}

func DefaultGuardrailConfig() GuardrailConfig {
	return GuardrailConfig{
		ExactFailureWarnAfter:  3,
		ExactFailureBlockAfter: 15,
		SameToolWarnAfter:      5,
		SameToolBlockAfter:     20,
		NoProgressWarnAfter:    3,
		NoProgressBlockAfter:   15,
	}
}

func (m *toolMetrics) RecordSuccess(toolName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.toolFails, toolName)
}

func (m *toolMetrics) RecordFailure(toolName, errorMsg string) (GuardrailResult, string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.toolFails[toolName]++
	failCount := m.toolFails[toolName]

	key := toolName + ":" + errorMsg
	m.exactFails[key]++
	exactCount := m.exactFails[key]

	cfg := DefaultGuardrailConfig()

	if exactCount >= cfg.ExactFailureBlockAfter {
		return GuardBlock, "repeated exact failure blocked"
	}
	if exactCount >= cfg.ExactFailureWarnAfter {
		return GuardWarn, "repeated exact failure warning"
	}
	if failCount >= cfg.SameToolBlockAfter {
		return GuardBlock, "tool failure limit reached"
	}
	if failCount >= cfg.SameToolWarnAfter {
		return GuardWarn, "consecutive tool failures"
	}
	return GuardOK, ""
}

func (m *toolMetrics) RecordNoProgress(toolName string) (GuardrailResult, string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.toolFails[toolName]++
	failCount := m.toolFails[toolName]
	cfg := DefaultGuardrailConfig()

	if failCount >= cfg.NoProgressBlockAfter {
		return GuardBlock, "no progress block limit reached"
	}
	if failCount >= cfg.NoProgressWarnAfter {
		return GuardWarn, "no progress detected"
	}
	return GuardOK, ""
}

func (m *toolMetrics) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolFails = make(map[string]int)
	m.lastResult = make(map[string]string)
	m.exactFails = make(map[string]int)
}
