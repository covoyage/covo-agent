package slashcmd

import (
	"strings"
	"testing"

	"github.com/covoyage/covonaut/tui/chat"
)

func TestBuildSessionRecapEmpty(t *testing.T) {
	got := buildSessionRecap(nil)
	if !strings.Contains(got, "recap") && got == "" {
		t.Fatalf("empty recap should not be blank")
	}
}

func TestBuildSessionRecapSummarizesLastTurns(t *testing.T) {
	got := buildSessionRecap([]chat.ChatMessage{
		{Role: chat.RoleUser, Text: "first question"},
		{Role: chat.RoleAssistant, Text: "first answer"},
		{Role: chat.RoleUser, Text: "second question about worktrees"},
		{Role: chat.RoleAssistant, Text: "second answer with details"},
		{Role: chat.RoleSystem, Text: "ignored"},
	})
	if !strings.Contains(got, "1 user") && !strings.Contains(got, "2 user") {
		t.Fatalf("expected user count, got %q", got)
	}
	if !strings.Contains(got, "second question") {
		t.Fatalf("expected last user text, got %q", got)
	}
	if !strings.Contains(got, "second answer") {
		t.Fatalf("expected last assistant text, got %q", got)
	}
	if !strings.Contains(got, "first question") {
		t.Fatalf("expected earlier user text, got %q", got)
	}
}
