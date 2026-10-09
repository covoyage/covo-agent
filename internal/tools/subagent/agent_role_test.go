package subagent

import (
	"context"
	"strings"
	"testing"
)

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

func TestReconAndVerifierHaveSystemPrompts(t *testing.T) {
	recon, ok := GetRole("recon")
	if !ok || recon.SystemPrompt == "" {
		t.Fatal("recon role should include a system prompt")
	}
	verifier, ok := GetRole("verifier")
	if !ok || verifier.SystemPrompt == "" {
		t.Fatal("verifier role should include a system prompt")
	}
}

func TestApplySpawnRoleRecon(t *testing.T) {
	ctx, task, toolsets := ApplySpawnRole(context.Background(), "map example.com", "recon", nil)
	if SubagentRoleFromContext(ctx) != "recon" {
		t.Fatalf("role=%q", SubagentRoleFromContext(ctx))
	}
	if !strings.Contains(task, "reconnaissance specialist") || !strings.Contains(task, "map example.com") {
		t.Fatalf("task=%q", task)
	}
	found := false
	for _, name := range toolsets {
		if name == "cyber" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("toolsets=%v", toolsets)
	}

	_, again, _ := ApplySpawnRole(ctx, task, "recon", toolsets)
	if strings.Count(again, "reconnaissance specialist") != 1 {
		t.Fatalf("prompt injected more than once: %q", again)
	}
}
