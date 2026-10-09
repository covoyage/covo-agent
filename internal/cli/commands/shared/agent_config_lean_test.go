package shared

import (
	"testing"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli"
)

func TestLeanToolsEnabled(t *testing.T) {
	t.Run("auto on with multiple mcp servers", func(t *testing.T) {
		t.Setenv("COVO_TOOLS_LEAN", "")
		cfg := &cli.Config{
			MCPServers: map[string]cli.MCPServerConfig{
				"a": {Command: "npx"},
				"b": {Command: "uvx"},
			},
		}
		if !LeanToolsEnabled(cfg) {
			t.Fatal("expected lean on when two MCP servers are configured")
		}
	})

	t.Run("auto off with one mcp server", func(t *testing.T) {
		t.Setenv("COVO_TOOLS_LEAN", "")
		cfg := &cli.Config{
			MCPServers: map[string]cli.MCPServerConfig{
				"a": {Command: "npx"},
			},
		}
		if LeanToolsEnabled(cfg) {
			t.Fatal("expected lean off with a single MCP server")
		}
	})

	t.Run("auto on in code mode", func(t *testing.T) {
		t.Setenv("COVO_TOOLS_LEAN", "")
		cfg := &cli.Config{Mode: "code"}
		if !LeanToolsEnabled(cfg) {
			t.Fatal("expected lean on in code mode")
		}
	})

	t.Run("auto on in cyber mode", func(t *testing.T) {
		t.Setenv("COVO_TOOLS_LEAN", "")
		cfg := &cli.Config{Mode: "cyber"}
		if !LeanToolsEnabled(cfg) {
			t.Fatal("expected lean on in cyber mode")
		}
	})

	t.Run("runtime mode overrides config mode", func(t *testing.T) {
		t.Setenv("COVO_TOOLS_LEAN", "")
		cfg := &cli.Config{Mode: "general"}
		if LeanToolsEnabled(cfg) {
			t.Fatal("general config should stay lean-off")
		}
		if !LeanToolsEnabledFor(cfg, "cyber") {
			t.Fatal("runtime cyber should enable lean")
		}
	})

	t.Run("explicit on", func(t *testing.T) {
		t.Setenv("COVO_TOOLS_LEAN", "")
		cfg := &cli.Config{Tools: &cli.ToolsConfig{Lean: "on"}}
		if !LeanToolsEnabled(cfg) {
			t.Fatal("expected lean on")
		}
	})

	t.Run("env overrides off", func(t *testing.T) {
		t.Setenv("COVO_TOOLS_LEAN", "off")
		cfg := &cli.Config{
			Tools: &cli.ToolsConfig{Lean: "on"},
			MCPServers: map[string]cli.MCPServerConfig{
				"a": {Command: "npx"},
				"b": {Command: "uvx"},
			},
		}
		if LeanToolsEnabled(cfg) {
			t.Fatal("COVO_TOOLS_LEAN=off should win")
		}
	})
}

func TestRegisterCustomModes(t *testing.T) {
	t.Cleanup(agent.ClearCustomModes)
	agent.ClearCustomModes()
	RegisterCustomModes(nil)
	RegisterCustomModes(&cli.Config{
		CustomModes: []cli.CustomModeConfig{
			{Name: "reviewer", Description: "PR reviewer", SystemPrompt: "review code"},
		},
	})
	m, err := agent.RequireMode("reviewer")
	if err != nil || m != agent.AgentMode("reviewer") {
		t.Fatalf("custom mode = %q err=%v", m, err)
	}
	if def := agent.GetCustomMode("reviewer"); def == nil || def.SystemPrompt != "review code" {
		t.Fatal("custom mode definition was not registered")
	}
}
