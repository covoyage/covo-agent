package slashcmd

import (
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseReviewArgs(t *testing.T) {
	tests := []struct {
		parts []string
		scope string
		extra string
	}{
		{[]string{"review"}, reviewScopeUncommitted, ""},
		{[]string{"review", "conversation"}, reviewScopeConversation, ""},
		{[]string{"review", "branch", "auth"}, reviewScopeBranch, "auth"},
		{[]string{"review", "uncommitted"}, reviewScopeUncommitted, ""},
		{[]string{"review", "login", "flow"}, reviewScopeUncommitted, "login flow"},
	}
	for _, tt := range tests {
		scope, extra := parseReviewArgs(tt.parts)
		if scope != tt.scope || extra != tt.extra {
			t.Errorf("parseReviewArgs(%v) = %q %q, want %q %q", tt.parts, scope, extra, tt.scope, tt.extra)
		}
	}
}

func TestBuildReviewPromptScopes(t *testing.T) {
	conv := buildReviewPrompt(reviewScopeConversation, "")
	if !strings.Contains(conv, "this conversation") {
		t.Fatalf("conversation prompt: %s", conv)
	}
	branch := buildReviewPrompt(reviewScopeBranch, "auth")
	if !strings.Contains(branch, "compared to main") || !strings.Contains(branch, "Focus on: auth") {
		t.Fatalf("branch prompt: %s", branch)
	}
}

func TestHandleReviewTaskConversationScope(t *testing.T) {
	var got string
	sctx := &SlashContext{UI: UIDependencies{SubmitPrompt: func(text string) { got = text }}}
	if !handleReviewTask(sctx, []string{"review", "conversation"}) {
		t.Fatal("expected handled")
	}
	if !strings.Contains(got, "this conversation") {
		t.Fatalf("got %q", got)
	}
}

func TestHandleInterviewWritesSpecNotAgents(t *testing.T) {
	var got string
	sctx := &SlashContext{UI: UIDependencies{SubmitPrompt: func(text string) { got = text }}}
	if !handleInterview(sctx, []string{"interview", "oauth"}) {
		t.Fatal("expected handled")
	}
	if !strings.Contains(got, "spec.md") {
		t.Fatalf("missing spec: %q", got)
	}
	if !strings.Contains(got, "Request:\n oauth") && !strings.Contains(got, "Request:\noauth") {
		t.Fatalf("missing topic: %q", got)
	}
	if !strings.Contains(got, "Do not make code changes") {
		t.Fatalf("should forbid code changes: %q", got)
	}
}

func TestHandlePlanWithTaskSubmitsPrompt(t *testing.T) {
	var got string
	sctx := &SlashContext{
		Runtime: RuntimeDependencies{Busy: new(atomic.Bool)},
		UI:      UIDependencies{SubmitPrompt: func(text string) { got = text }},
	}
	if !handlePlan(sctx, []string{"plan", "add", "oauth"}) {
		t.Fatal("expected handled")
	}
	if !strings.Contains(got, "add oauth") {
		t.Fatalf("missing task: %q", got)
	}
	if !strings.Contains(got, "Do not edit files") {
		t.Fatalf("should forbid edits: %q", got)
	}
}
