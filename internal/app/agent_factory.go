package app

import (
	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/agent"
	agenttools "github.com/covoyage/covo-agent/internal/tools"
)

// AgentRequest contains the values that may change when rebuilding an agent.
type AgentRequest struct {
	Mode          agent.AgentMode
	Provider      agentcore.Provider
	ProviderName  string
	Model         string
	ContextLength int64
}

// AgentFactory builds agents from one stable base configuration.
type AgentFactory struct {
	base  agent.CovoAgentConfig
	state *RuntimeState
}

func NewAgentFactory(base agent.CovoAgentConfig, state *RuntimeState) *AgentFactory {
	return &AgentFactory{base: cloneAgentConfig(base), state: state}
}

// Config returns an independent config with request-specific values applied.
func (factory *AgentFactory) Config(request AgentRequest) agent.CovoAgentConfig {
	config := cloneAgentConfig(factory.base)
	config.Mode = request.Mode
	config.Provider = request.Provider
	config.ProviderName = request.ProviderName
	config.Model = request.Model
	config.ModelContextLength = request.ContextLength
	if factory.state != nil {
		config.ToolProfile = factory.state.ActiveProfile()
	}
	return config
}

func (factory *AgentFactory) New(request AgentRequest) (*agent.CovoAgent, error) {
	return agent.NewCovoAgent(factory.Config(request))
}

func cloneAgentConfig(config agent.CovoAgentConfig) agent.CovoAgentConfig {
	cloned := config
	cloned.LifecycleHooks = append([]agentcore.LifecycleHook(nil), config.LifecycleHooks...)
	cloned.ProviderMiddlewares = append([]agent.ProviderMiddleware(nil), config.ProviderMiddlewares...)
	cloned.SkillURLs = append([]string(nil), config.SkillURLs...)
	cloned.ToolsetOverride = append([]string(nil), config.ToolsetOverride...)
	if config.MCPServers != nil {
		cloned.MCPServers = make(map[string]agenttools.MCPConfig, len(config.MCPServers))
		for name, server := range config.MCPServers {
			server.Args = append([]string(nil), server.Args...)
			server.Env = append([]string(nil), server.Env...)
			cloned.MCPServers[name] = server
		}
	}
	return cloned
}
