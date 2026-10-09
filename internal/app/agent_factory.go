package app

import (
	"os"
	"strings"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/agent"
	agenttools "github.com/covoyage/covo-agent/internal/tools"
)

// AgentRequest contains the values that may change when rebuilding an agent.
type AgentRequest struct {
	Mode           agent.AgentMode
	Provider       agentcore.Provider
	ProviderName   string
	Model          string
	ContextLength  int64
	ToolNameFilter func(string) bool
	// SystemPromptSuffix is appended to the agent's system prompt for this
	// request only (e.g. cron jobs instructing the agent to load skills).
	SystemPromptSuffix string
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
	config.ToolNameFilter = request.ToolNameFilter
	if request.SystemPromptSuffix != "" {
		config.AppendSystemPrompt = strings.TrimSpace(
			config.AppendSystemPrompt + "\n\n" + request.SystemPromptSuffix)
	}
	if factory.state != nil {
		config.ToolProfile = factory.state.ActiveProfile()
		if roots := factory.state.ExtraWorkspaceRoots(); roots != nil {
			config.ExtraWorkspaceRoots = append([]string(nil), roots...)
		}
	}
	config.LeanTools = leanToolsForMode(config, request.Mode)
	return config
}

func leanToolsForMode(config agent.CovoAgentConfig, mode agent.AgentMode) bool {
	env := strings.ToLower(strings.TrimSpace(os.Getenv("COVO_TOOLS_LEAN")))
	switch env {
	case "on", "true", "1", "yes":
		return true
	case "off", "false", "0", "no":
		return false
	}
	if mode.LeanDefault() {
		return true
	}
	return len(config.MCPServers) > 1
}

func (factory *AgentFactory) New(request AgentRequest) (*agent.CovoAgent, error) {
	return agent.NewCovoAgent(factory.Config(request))
}

func cloneAgentConfig(config agent.CovoAgentConfig) agent.CovoAgentConfig {
	cloned := config
	cloned.LifecycleHooks = append([]agentcore.LifecycleHook(nil), config.LifecycleHooks...)
	cloned.ProviderMiddlewares = append([]agent.ProviderMiddleware(nil), config.ProviderMiddlewares...)
	cloned.SkillURLs = append([]string(nil), config.SkillURLs...)
	cloned.SkillExtraDirs = append([]string(nil), config.SkillExtraDirs...)
	cloned.SkillDisabled = append([]string(nil), config.SkillDisabled...)
	cloned.ToolsetOverride = append([]string(nil), config.ToolsetOverride...)
	cloned.ExtraWorkspaceRoots = append([]string(nil), config.ExtraWorkspaceRoots...)
	if config.Remote != nil {
		remote := *config.Remote
		cloned.Remote = &remote
	}
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
