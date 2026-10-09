package acp

import (
	"testing"

	"github.com/covoyage/covo-agent/internal/agent"
)

func TestAvailableModesIncludesCyber(t *testing.T) {
	modes := (&covoAgentFactory{}).AvailableModes()
	found := map[string]bool{}
	for _, mode := range modes {
		found[mode.ID] = true
	}
	for _, id := range []string{"general", "code", "cyber"} {
		if !found[id] {
			t.Errorf("ACP available modes missing %q", id)
		}
	}
}

func TestAvailableModesIncludesCustom(t *testing.T) {
	t.Cleanup(agent.ClearCustomModes)
	agent.RegisterCustomMode(&agent.CustomModeDefinition{
		Name:        "reviewer",
		Description: "PR reviewer",
	})
	modes := (&covoAgentFactory{}).AvailableModes()
	for _, mode := range modes {
		if mode.ID == "reviewer" {
			if mode.Description != "PR reviewer" {
				t.Fatalf("custom mode description = %q", mode.Description)
			}
			return
		}
	}
	t.Fatal("ACP available modes missing custom reviewer")
}
