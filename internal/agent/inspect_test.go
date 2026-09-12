package agent

import "testing"

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("abc", 10); got != "abc" {
		t.Fatalf("got %q", got)
	}
	got := truncateRunes("abcdefghij", 4)
	if got != "abcd…" {
		t.Fatalf("got %q", got)
	}
}

func TestInspectToolsEmptyAgent(t *testing.T) {
	ca := &CovoAgent{baseCfg: CovoAgentConfig{ToolProfile: "coding", LeanTools: true}}
	info := ca.InspectTools()
	if info.Profile != "coding" || !info.LeanMode || info.PlanMode {
		t.Fatalf("unexpected inspect: %+v", info)
	}
	if len(info.Kept) != 0 {
		t.Fatalf("empty agent should keep no tools, got %v", info.Kept)
	}
}

func TestInspectContextEmptyAgent(t *testing.T) {
	ca := &CovoAgent{baseCfg: CovoAgentConfig{ContextEngine: "truncate"}}
	info := ca.InspectContext()
	if info.Engine != "truncate" {
		t.Fatalf("engine=%q", info.Engine)
	}
	if info.Messages != 0 || info.SystemTokens != 0 {
		t.Fatalf("empty agent should have empty context: %+v", info)
	}
}
