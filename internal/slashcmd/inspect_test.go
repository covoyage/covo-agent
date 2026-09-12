package slashcmd

import (
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/agent"
)

func TestFormatToolInspectExplainsFilters(t *testing.T) {
	out := formatToolInspect(agent.ToolInspect{
		Kept:     []string{"read", "tool_search"},
		Excluded: map[string]string{"bash": "plan mode", "mcp": "lean"},
		Toolsets: []string{"coding"},
		Platform: "cli",
		Profile:  "coding",
		PlanMode: true,
		LeanMode: true,
	})
	for _, want := range []string{
		"profile=coding",
		"phase=plan",
		"lean=on",
		"side=off",
		"active: read, tool_search",
		"plan mode:",
		"lean:",
		"hidden:",
		"plan mode (1): bash",
		"lean (1): mcp",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestFormatToolInspectSideFilter(t *testing.T) {
	out := formatToolInspect(agent.ToolInspect{
		Kept:       []string{"read", "grep"},
		Profile:    "full",
		SideFilter: true,
	})
	if !strings.Contains(out, "side=on") || !strings.Contains(out, "side filter:") {
		t.Fatalf("expected side filter explanation, got:\n%s", out)
	}
}

func TestFormatContextInspectModes(t *testing.T) {
	info := agent.ContextInspect{
		Mode:           "code",
		Engine:         "enhanced",
		ContextLength:  1000,
		Messages:       4,
		MessageTokens:  40,
		SystemTokens:   20,
		StableTokens:   10,
		ContextTokens:  6,
		VolatileTokens: 4,
		Layers: []agent.ContextLayer{
			{Name: "stable: identity + guidance", Tokens: 10, Present: true, Preview: "You are covo-agent\nRULES:"},
			{Name: "repo map", Present: false, Preview: "(not injected yet; built on first Run)"},
		},
	}

	list := formatContextInspect(info, "list")
	if !strings.Contains(list, "mode=code") || !strings.Contains(list, "history: 4 messages") {
		t.Fatalf("list header missing:\n%s", list)
	}
	if strings.Contains(list, "You are covo-agent") {
		t.Fatalf("list mode should not dump previews:\n%s", list)
	}
	if !strings.Contains(list, "stable: identity + guidance") {
		t.Fatalf("list should name layers:\n%s", list)
	}

	mapped := formatContextInspect(info, "map")
	if !strings.Contains(mapped, "You are covo-agent") {
		t.Fatalf("map should include first preview line:\n%s", mapped)
	}
	if strings.Contains(mapped, "RULES:") {
		t.Fatalf("map should stay to the first preview line:\n%s", mapped)
	}

	detail := formatContextInspect(info, "detail")
	if !strings.Contains(detail, "RULES:") {
		t.Fatalf("detail should keep the preview body:\n%s", detail)
	}
}

func TestFormatSkillInspectShowsSourceAndGate(t *testing.T) {
	out := formatSkillInspect([]agent.SkillCatalogEntry{
		{Name: "keep", Source: "workspace", Kept: true, Reason: "won collision"},
		{Name: "gone", Source: "managed", Kept: false, Reason: "disabled"},
	})
	for _, want := range []string{
		"1 loaded, 1 gated",
		"keep  source=workspace  won collision",
		"gone  source=managed  disabled",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
