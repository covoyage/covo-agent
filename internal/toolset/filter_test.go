package toolset

import (
	"context"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestToolsetFilter_NameFilter(t *testing.T) {
	platform := NewPlatformToolsets("cli")
	platform.SetOverride("cli", []string{"read", "bash", "web_fetch"})

	tf := NewToolsetFilter(ToolsetFilterConfig{
		Platform: platform,
		ToolNames: func() []string {
			return []string{"read", "bash", "web_fetch"}
		},
		PlatformName: func() string { return "cli" },
		NameFilter:   func(name string) bool { return name != "bash" },
	})

	mcc := &agentcore.ModelCallContext{
		Request: &agentcore.ProviderRequest{
			Tools: []agentcore.ToolDefinition{
				{Name: "read"},
				{Name: "bash"},
				{Name: "web_fetch"},
			},
		},
	}
	if err := tf.BeforeModelCall(context.Background(), &agentcore.AgentRunContext{}, mcc); err != nil {
		t.Fatalf("BeforeModelCall: %v", err)
	}

	kept := make(map[string]bool)
	for _, def := range mcc.Request.Tools {
		kept[def.Name] = true
	}
	if kept["bash"] {
		t.Error("expected bash to be removed by NameFilter")
	}
	if !kept["read"] || !kept["web_fetch"] {
		t.Errorf("expected read and web_fetch to survive the NameFilter, kept=%v", kept)
	}
}

func TestToolsetFilter_NilNameFilterKeepsAll(t *testing.T) {
	platform := NewPlatformToolsets("cli")
	platform.SetOverride("cli", []string{"read", "bash", "web_fetch"})

	tf := NewToolsetFilter(ToolsetFilterConfig{
		Platform: platform,
		ToolNames: func() []string {
			return []string{"read", "bash", "web_fetch"}
		},
		PlatformName: func() string { return "cli" },
	})

	mcc := &agentcore.ModelCallContext{
		Request: &agentcore.ProviderRequest{
			Tools: []agentcore.ToolDefinition{
				{Name: "read"},
				{Name: "bash"},
				{Name: "web_fetch"},
			},
		},
	}
	if err := tf.BeforeModelCall(context.Background(), &agentcore.AgentRunContext{}, mcc); err != nil {
		t.Fatalf("BeforeModelCall: %v", err)
	}
	if len(mcc.Request.Tools) != 3 {
		t.Errorf("expected all 3 tools kept with nil NameFilter, got %d", len(mcc.Request.Tools))
	}
}
