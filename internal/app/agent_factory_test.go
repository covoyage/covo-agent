package app

import (
	"testing"

	"github.com/covoyage/covo-agent/internal/agent"
	agenttools "github.com/covoyage/covo-agent/internal/tools"
)

func TestAgentFactoryPreservesBaseAndAppliesRequest(t *testing.T) {
	t.Setenv("COVO_TOOLS_LEAN", "")
	state := NewRuntimeState()
	state.SetActiveProfile("coding")
	base := agent.CovoAgentConfig{
		Mode:            agent.ModeGeneral,
		ProviderName:    "old-provider",
		Model:           "old-model",
		WorkingDir:      "/workspace",
		HomeDir:         "/home",
		ContextEngine:   "structured",
		SkillURLs:       []string{"https://example.test/skill"},
		ToolProfile:     "full",
		SystemPrompt:    "system",
		WorkspaceOnly:   true,
		ToolsetOverride: []string{"filesystem"},
		MCPServers: map[string]agenttools.MCPConfig{
			"filesystem": {Command: "npx", Args: []string{"-y", "server"}, Env: []string{"TOKEN"}},
		},
	}
	factory := NewAgentFactory(base, state)

	config := factory.Config(AgentRequest{
		Mode:          agent.ModeCode,
		ProviderName:  "new-provider",
		Model:         "new-model",
		ContextLength: 128000,
	})
	if config.Mode != agent.ModeCode || config.ProviderName != "new-provider" || config.Model != "new-model" {
		t.Fatalf("dynamic fields were not applied: %+v", config)
	}
	if !config.LeanTools {
		t.Fatal("switching to code should enable lean tools")
	}
	if config.ModelContextLength != 128000 {
		t.Fatalf("ModelContextLength = %d, want 128000", config.ModelContextLength)
	}
	if config.WorkingDir != base.WorkingDir || config.HomeDir != base.HomeDir || config.ContextEngine != base.ContextEngine || config.SystemPrompt != base.SystemPrompt || !config.WorkspaceOnly {
		t.Fatalf("base fields drifted: %+v", config)
	}
	if config.ToolProfile != "coding" {
		t.Fatalf("ToolProfile = %q, want coding", config.ToolProfile)
	}
	if len(config.SkillURLs) != 1 || config.SkillURLs[0] != base.SkillURLs[0] {
		t.Fatalf("SkillURLs = %v", config.SkillURLs)
	}

	plain := NewAgentFactory(agent.CovoAgentConfig{WorkingDir: "/workspace"}, nil)
	general := plain.Config(AgentRequest{Mode: agent.ModeGeneral})
	if general.LeanTools {
		t.Fatal("switching back to general should not keep lean on")
	}
	cyber := plain.Config(AgentRequest{Mode: agent.ModeCyber})
	if !cyber.LeanTools {
		t.Fatal("switching to cyber should enable lean tools")
	}
}

func TestAgentFactoryReturnsIndependentCollections(t *testing.T) {
	base := agent.CovoAgentConfig{
		SkillURLs:       []string{"one"},
		ToolsetOverride: []string{"filesystem"},
		MCPServers: map[string]agenttools.MCPConfig{
			"server": {Args: []string{"one"}, Env: []string{"TOKEN"}},
		},
	}
	factory := NewAgentFactory(base, nil)
	first := factory.Config(AgentRequest{})
	first.SkillURLs[0] = "changed"
	first.ToolsetOverride[0] = "changed"
	server := first.MCPServers["server"]
	server.Args[0] = "changed"
	server.Env[0] = "changed"
	first.MCPServers["server"] = server

	second := factory.Config(AgentRequest{})
	if second.SkillURLs[0] != "one" || second.ToolsetOverride[0] != "filesystem" {
		t.Fatalf("factory slices were shared: %+v", second)
	}
	if second.MCPServers["server"].Args[0] != "one" || second.MCPServers["server"].Env[0] != "TOKEN" {
		t.Fatalf("factory MCP config was shared: %+v", second.MCPServers["server"])
	}
}

func TestAgentFactoryReadsCurrentProfileForEveryBuild(t *testing.T) {
	state := NewRuntimeState()
	factory := NewAgentFactory(agent.CovoAgentConfig{}, state)
	if got := factory.Config(AgentRequest{}).ToolProfile; got != DefaultToolProfile {
		t.Fatalf("initial ToolProfile = %q", got)
	}
	state.SetActiveProfile("minimal")
	if got := factory.Config(AgentRequest{}).ToolProfile; got != "minimal" {
		t.Fatalf("updated ToolProfile = %q", got)
	}
}

func TestAgentFactoryAppliesToolNameFilter(t *testing.T) {
	factory := NewAgentFactory(agent.CovoAgentConfig{}, nil)
	filter := func(name string) bool { return name == "read" }
	got := factory.Config(AgentRequest{ToolNameFilter: filter})
	if got.ToolNameFilter == nil || !got.ToolNameFilter("read") || got.ToolNameFilter("bash") {
		t.Fatal("ToolNameFilter was not applied")
	}
}

func TestAgentFactoryOverlaysExtraWorkspaceRoots(t *testing.T) {
	state := NewRuntimeState()
	state.SetExtraWorkspaceRoots([]string{"/tmp/extra-a"})
	factory := NewAgentFactory(agent.CovoAgentConfig{}, state)
	got := factory.Config(AgentRequest{})
	if len(got.ExtraWorkspaceRoots) != 1 || got.ExtraWorkspaceRoots[0] != "/tmp/extra-a" {
		t.Fatalf("ExtraWorkspaceRoots = %v", got.ExtraWorkspaceRoots)
	}
	state.SetExtraWorkspaceRoots([]string{"/tmp/extra-b"})
	got = factory.Config(AgentRequest{})
	if len(got.ExtraWorkspaceRoots) != 1 || got.ExtraWorkspaceRoots[0] != "/tmp/extra-b" {
		t.Fatalf("updated ExtraWorkspaceRoots = %v", got.ExtraWorkspaceRoots)
	}
}
