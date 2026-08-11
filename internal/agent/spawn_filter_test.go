package agent

import (
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

// TestFilterInjectableParentMessages_DropsToolAndSystem verifies that in "full"
// context mode only standard user/assistant messages are injected into the
// child's state: tool results (which require a matching assistant tool_calls
// chain the child lacks) and system messages (which would duplicate the child's
// own system prompt) are filtered out.
func TestFilterInjectableParentMessages_DropsToolAndSystem(t *testing.T) {
	msgs := []agentcore.Message{
		{Role: agentcore.RoleSystem, Content: "parent system prompt"},
		{Role: agentcore.RoleUser, Content: "please fix the bug"},
		{Role: agentcore.RoleAssistant, Content: "looking into it", ToolCalls: []agentcore.ToolCall{{ID: "tc1", Name: "Read"}}},
		{Role: agentcore.RoleTool, ToolCallID: "tc1", Content: "file contents"},
		{Role: agentcore.RoleAssistant, Content: "done"},
	}

	got := filterInjectableParentMessages(msgs)

	if len(got) != 3 {
		t.Fatalf("expected 3 injectable messages, got %d: %+v", len(got), got)
	}
	for _, m := range got {
		if m.Role == agentcore.RoleTool {
			t.Errorf("tool message should be filtered out: %+v", m)
		}
		if m.Role == agentcore.RoleSystem {
			t.Errorf("system message should be filtered out: %+v", m)
		}
	}
	// Order and content of kept messages must be preserved.
	if got[0].Content != "please fix the bug" {
		t.Errorf("first kept message = %+v, want user message", got[0])
	}
	if got[1].Content != "looking into it" {
		t.Errorf("second kept message = %+v, want assistant message", got[1])
	}
	if got[2].Content != "done" {
		t.Errorf("third kept message = %+v, want assistant message", got[2])
	}
	// An assistant message carrying tool_calls is still a standard conversation
	// message and must be retained (only role=tool results are dropped).
	if len(got[1].ToolCalls) != 1 {
		t.Errorf("assistant tool_calls should be preserved, got %d", len(got[1].ToolCalls))
	}
}

// TestFilterInjectableParentMessages_AllFiltered ensures that a conversation
// consisting solely of tool/system messages yields an empty result (so the
// spawn runner injects nothing).
func TestFilterInjectableParentMessages_AllFiltered(t *testing.T) {
	msgs := []agentcore.Message{
		{Role: agentcore.RoleSystem, Content: "sys"},
		{Role: agentcore.RoleTool, ToolCallID: "x", Content: "result"},
	}
	got := filterInjectableParentMessages(msgs)
	if len(got) != 0 {
		t.Errorf("expected 0 messages, got %d: %+v", len(got), got)
	}
}

// TestFilterInjectableParentMessages_AllKept ensures user/assistant-only input
// passes through unchanged.
func TestFilterInjectableParentMessages_AllKept(t *testing.T) {
	msgs := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: "u"},
		{Role: agentcore.RoleAssistant, Content: "a"},
	}
	got := filterInjectableParentMessages(msgs)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages retained, got %d: %+v", len(got), got)
	}
}

// TestFilterInjectableParentMessages_Empty covers the nil/empty input case.
func TestFilterInjectableParentMessages_Empty(t *testing.T) {
	got := filterInjectableParentMessages(nil)
	if len(got) != 0 {
		t.Errorf("expected 0 messages for nil input, got %d", len(got))
	}
	got = filterInjectableParentMessages([]agentcore.Message{})
	if len(got) != 0 {
		t.Errorf("expected 0 messages for empty input, got %d", len(got))
	}
}
