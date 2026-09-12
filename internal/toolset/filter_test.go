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

func TestToolsetFilter_PreviewMatchesBeforeModelCall(t *testing.T) {
	platform := NewPlatformToolsets("cli")
	platform.SetOverride("cli", []string{"read", "mcp", "tool_search", "computer_use"})

	tf := NewToolsetFilter(ToolsetFilterConfig{
		Platform: platform,
		ToolNames: func() []string {
			return []string{"read", "mcp", "tool_search", "computer_use"}
		},
		PlatformName:    func() string { return "cli" },
		LeanMode:        true,
		PlanModeChecker: func() bool { return true },
	})

	names := []string{"read", "mcp", "tool_search", "computer_use"}
	preview := tf.Preview(names)
	if !containsAll(preview.Kept, "read", "tool_search") {
		t.Fatalf("preview kept=%v", preview.Kept)
	}
	if preview.Excluded["mcp"] != "lean" && preview.Excluded["mcp"] != "plan mode" {
		t.Fatalf("mcp reason=%q kept=%v excluded=%v", preview.Excluded["mcp"], preview.Kept, preview.Excluded)
	}
}

func containsAll(have []string, want ...string) bool {
	set := map[string]bool{}
	for _, n := range have {
		set[n] = true
	}
	for _, n := range want {
		if !set[n] {
			return false
		}
	}
	return true
}

func TestToolsetFilter_LeanModeHidesLongTail(t *testing.T) {
	platform := NewPlatformToolsets("cli")
	platform.SetOverride("cli", []string{"read", "mcp", "tool_search", "computer_use"})

	tf := NewToolsetFilter(ToolsetFilterConfig{
		Platform: platform,
		ToolNames: func() []string {
			return []string{"read", "mcp", "tool_search", "computer_use"}
		},
		PlatformName: func() string { return "cli" },
		LeanMode:     true,
	})

	mcc := &agentcore.ModelCallContext{
		Request: &agentcore.ProviderRequest{
			Tools: []agentcore.ToolDefinition{
				{Name: "read"},
				{Name: "mcp"},
				{Name: "tool_search"},
				{Name: "computer_use"},
			},
		},
	}
	if err := tf.BeforeModelCall(context.Background(), &agentcore.AgentRunContext{}, mcc); err != nil {
		t.Fatalf("BeforeModelCall: %v", err)
	}
	kept := map[string]bool{}
	for _, def := range mcc.Request.Tools {
		kept[def.Name] = true
	}
	if !kept["read"] || !kept["tool_search"] {
		t.Errorf("lean mode should keep core + discovery tools, kept=%v", kept)
	}
	if kept["mcp"] || kept["computer_use"] {
		t.Errorf("lean mode should hide long-tail schemas, kept=%v", kept)
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
