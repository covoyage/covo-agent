package slashcmd

import (
	"strings"
	"testing"
)

func TestHandleReviewTaskSubmitsPrompt(t *testing.T) {
	var got string
	sctx := &SlashContext{
		UI: UIDependencies{
			SubmitPrompt: func(text string) { got = text },
		},
	}
	if !handleReviewTask(sctx, []string{"review", "auth"}) {
		t.Fatal("expected handled")
	}
	if !strings.Contains(got, "uncommitted changes") {
		t.Fatalf("review prompt missing body: %q", got)
	}
	if !strings.Contains(got, "Focus on: auth") {
		t.Fatalf("review prompt missing focus: %q", got)
	}
}

func TestHandleInterviewSubmitsPrompt(t *testing.T) {
	var got string
	sctx := &SlashContext{
		UI: UIDependencies{
			SubmitPrompt: func(text string) { got = text },
		},
	}
	if !handleInterview(sctx, []string{"interview", "build"}) {
		t.Fatal("expected handled")
	}
	if !strings.Contains(got, "spec.md") {
		t.Fatalf("interview prompt missing spec: %q", got)
	}
	if !strings.Contains(got, "build") {
		t.Fatalf("interview prompt missing topic: %q", got)
	}
	if strings.Contains(got, "write or update AGENTS.md") {
		t.Fatalf("interview should not write AGENTS.md: %q", got)
	}
}
