package commands

import (
	"fmt"
	"log"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/agent"
	runtimeapp "github.com/covoyage/covo-agent/internal/app"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
)

// prepareAgent applies session-wide wiring (permission checking, YOLO mode)
// to a freshly created agent. It runs both for the initial agent and for
// every replacement produced by agentRuntime.
func (s *interactiveSession) prepareAgent(newCA *agent.CovoAgent) {
	if newCA == nil {
		return
	}
	if s.permissionGate != nil {
		newCA.SetPermissionChecker(s.permissionGate.Checker())
	}
	if shared.RuntimeState.SessionYolo() {
		if approvalSys := newCA.ApprovalSystem(); approvalSys != nil {
			approvalSys.EnableSessionYolo("cli")
		}
	}
}

func (s *interactiveSession) requestFor(m agent.AgentMode, provider agentcore.Provider, providerName, modelName string) runtimeapp.AgentRequest {
	return runtimeapp.AgentRequest{
		Mode:          m,
		Provider:      provider,
		ProviderName:  providerName,
		Model:         modelName,
		ContextLength: shared.ResolveModelContextLength(s.cfg, providerName, modelName),
	}
}

func (s *interactiveSession) createAgent(m agent.AgentMode) *agent.CovoAgent {
	newCA, err := s.agentFactory.New(s.requestFor(m, s.llm, s.providerType, s.model))
	if err != nil {
		log.Printf("create agent: %v", err)
		return nil
	}
	s.prepareAgent(newCA)
	return newCA
}

func (s *interactiveSession) replaceAgent(request runtimeapp.AgentRequest, preserveState bool) (*agent.CovoAgent, error) {
	replacement, err := s.agentRuntime.Replace(request, preserveState)
	if err != nil {
		return nil, err
	}
	return replacement.Agent, nil
}

func (s *interactiveSession) switchToMode(newMode agent.AgentMode) {
	_, err := s.replaceAgent(s.requestFor(newMode, s.llm, s.providerType, s.model), true)
	if err != nil {
		log.Printf("replace agent: %v", err)
		return
	}
	s.mode = newMode
	if s.stickyFooter != nil {
		s.stickyFooter.SetMode(string(newMode))
	}
}

func (s *interactiveSession) switchModel(newModel string) {
	_, err := s.replaceAgent(s.requestFor(s.mode, s.llm, s.providerType, newModel), true)
	if err != nil {
		log.Printf("replace agent: %v", err)
		return
	}
	s.model = newModel
	s.cfg.Model = newModel
	if err := cli.SaveConfig(s.cfg); err != nil {
		log.Printf("save config: %v", err)
	}
	if s.app != nil {
		loadUIBus().UpdateStatusBar(s.providerType, newModel, string(s.mode))
	}
}

func (s *interactiveSession) switchProviderModel(newProvider, newModel string) error {
	if err := cli.ValidateProvider(newProvider); err != nil {
		return err
	}
	if !cli.HasProviderConfiguredFor(newProvider) {
		env := cli.ProviderAPIKeyEnv(newProvider)
		return fmt.Errorf("%s is not set", env)
	}

	newLLM, err := cli.BuildProvider(newProvider)
	if err != nil {
		return fmt.Errorf("build provider %s: %w", newProvider, err)
	}

	normalizedProvider := cli.ProviderName(newProvider)
	_, err = s.replaceAgent(s.requestFor(s.mode, newLLM, normalizedProvider, newModel), true)
	if err != nil {
		return fmt.Errorf("create agent for provider %s: %w", normalizedProvider, err)
	}
	s.providerType = normalizedProvider
	s.model = newModel
	s.cfg.Provider = s.providerType
	s.cfg.Model = s.model
	if err := cli.SaveConfig(s.cfg); err != nil {
		log.Printf("save config: %v", err)
	}
	s.llm = newLLM
	if s.app != nil {
		loadUIBus().UpdateStatusBar(newProvider, newModel, string(s.mode))
	}
	return nil
}

func (s *interactiveSession) switchProvider(newProvider string) error {
	return s.switchProviderModel(newProvider, cli.DefaultModel(newProvider))
}
