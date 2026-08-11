package app

import (
	"sync"
	"sync/atomic"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/agent"
)

// AgentReplacement describes one completed agent lifecycle replacement.
type AgentReplacement struct {
	Agent    *agent.CovoAgent
	Core     *agentcore.Agent
	Snapshot *agentcore.StateSnapshot
}

// AgentRuntime owns the current CovoAgent and serializes lifecycle replacement.
type AgentRuntime struct {
	factory *AgentFactory
	mu      sync.Mutex
	core    atomic.Pointer[agentcore.Agent]
	agent   atomic.Pointer[agent.CovoAgent]
	prepare func(*agent.CovoAgent)
	hooks   []func(AgentReplacement)
}

func NewAgentRuntime(factory *AgentFactory, initial *agent.CovoAgent) *AgentRuntime {
	runtime := &AgentRuntime{factory: factory}
	if initial != nil {
		runtime.agent.Store(initial)
		runtime.core.Store(initial.Core())
	}
	return runtime
}

func (runtime *AgentRuntime) Current() *agent.CovoAgent { return runtime.agent.Load() }
func (runtime *AgentRuntime) Core() *agentcore.Agent    { return runtime.core.Load() }

// AgentPointer and CorePointer provide compatibility for read-heavy slash handlers.
func (runtime *AgentRuntime) AgentPointer() *atomic.Pointer[agent.CovoAgent] { return &runtime.agent }
func (runtime *AgentRuntime) CorePointer() *atomic.Pointer[agentcore.Agent]  { return &runtime.core }

func (runtime *AgentRuntime) SetPrepare(prepare func(*agent.CovoAgent)) {
	runtime.mu.Lock()
	runtime.prepare = prepare
	runtime.mu.Unlock()
}

func (runtime *AgentRuntime) OnReplace(hook func(AgentReplacement)) {
	if hook == nil {
		return
	}
	runtime.mu.Lock()
	runtime.hooks = append(runtime.hooks, hook)
	runtime.mu.Unlock()
}

// Replace creates and installs an agent. When preserveState is true, the
// current core snapshot is restored into the replacement before publication.
func (runtime *AgentRuntime) Replace(request AgentRequest, preserveState bool) (AgentReplacement, error) {
	runtime.mu.Lock()

	var snapshot *agentcore.StateSnapshot
	if preserveState {
		if current := runtime.core.Load(); current != nil {
			captured := current.State().Snapshot()
			snapshot = &captured
		}
	}

	replacement, err := runtime.factory.New(request)
	if err != nil {
		runtime.mu.Unlock()
		return AgentReplacement{}, err
	}
	if runtime.prepare != nil {
		runtime.prepare(replacement)
	}
	newCore := replacement.Core()
	if snapshot != nil {
		newCore.State().Restore(*snapshot)
	}

	old := runtime.agent.Swap(replacement)
	runtime.core.Store(newCore)
	if old != nil && old != replacement {
		old.Close()
	}

	event := AgentReplacement{Agent: replacement, Core: newCore, Snapshot: snapshot}
	hooks := append([]func(AgentReplacement){}, runtime.hooks...)
	runtime.mu.Unlock()
	for _, hook := range hooks {
		hook(event)
	}
	return event, nil
}

func (runtime *AgentRuntime) Close() {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.core.Store(nil)
	if current := runtime.agent.Swap(nil); current != nil {
		current.Close()
	}
}
