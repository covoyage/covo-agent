package subagent

import "testing"

func TestExplorerAndReviewerHaveSystemPrompts(t *testing.T) {
	explorer, ok := GetRole("explorer")
	if !ok || explorer.SystemPrompt == "" {
		t.Fatal("explorer role should include a system prompt")
	}
	reviewer, ok := GetRole("reviewer")
	if !ok || reviewer.SystemPrompt == "" {
		t.Fatal("reviewer role should include a system prompt")
	}
}
