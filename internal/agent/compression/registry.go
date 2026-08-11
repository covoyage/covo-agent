package compression

import (
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

// ContextEngineFactory creates a ContextEngine given the base engine.
type ContextEngineFactory func(inner agentcore.ContextEngine) agentcore.ContextEngine

var (
	engineFactoriesMu sync.RWMutex
	engineFactories   = map[string]ContextEngineFactory{
		"enhanced": newEnhancedContextEngine,
	}
)

func RegisterContextEngine(name string, factory ContextEngineFactory) {
	engineFactoriesMu.Lock()
	engineFactories[name] = factory
	engineFactoriesMu.Unlock()
}

func GetContextEngineFactory(name string) ContextEngineFactory {
	engineFactoriesMu.RLock()
	defer engineFactoriesMu.RUnlock()
	return engineFactories[name]
}

func ContextEngineNames() []string {
	engineFactoriesMu.RLock()
	defer engineFactoriesMu.RUnlock()
	var names []string
	for n := range engineFactories {
		names = append(names, n)
	}
	return names
}

func newEnhancedContextEngine(inner agentcore.ContextEngine) agentcore.ContextEngine {
	return &EnhancedContextEngine{inner: inner}
}
