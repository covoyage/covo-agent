package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestClaudeHooks_E2E_UserPromptSubmitDenyBlocksRun verifies the full wiring:
// a Claude Code UserPromptSubmit hook loaded from ~/.claude/hooks.json by
// NewCovoAgent is honored by Run, aborting the run before the provider is
// ever called.
func TestClaudeHooks_E2E_UserPromptSubmitDenyBlocksRun(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	mustWrite(t, filepath.Join(home, ".claude", "hooks.json"),
		`{"Hooks":[{"Hooks":[{"Type":"UserPromptSubmit","Command":"echo '{\"decision\":\"deny\",\"reason\":\"maintenance window\"}'"}]}]}`)

	t.Setenv("COVO_CLAUDE_HOOKS_DISABLED", "")
	t.Setenv("COVO_CLAUDE_HOOKS_PATH", "")
	t.Setenv("COVO_ACCEPT_HOOKS", "true")

	mock := &streamMockProvider{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	ca, err := NewCovoAgent(CovoAgentConfig{
		Mode:         ModeGeneral,
		Provider:     mock,
		ProviderName: "mock",
		Model:        "mock-model",
		WorkingDir:   work,
		HomeDir:      home,
		Logger:       logger,
		ToolProfile:  "minimal",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	defer ca.Close()

	_, err = ca.Run(context.Background(), "deploy now")
	if err == nil {
		t.Fatal("expected Run to fail when UserPromptSubmit hook denies")
	}
	if !strings.Contains(err.Error(), "maintenance window") {
		t.Errorf("expected reason in error, got %q", err)
	}
	if mock.calls != 0 {
		t.Errorf("provider must not be called when the input is denied, got %d calls", mock.calls)
	}
}

// TestClaudeHooks_E2E_UserPromptSubmitAllowRuns verifies the allow path:
// an approving hook lets the run proceed to the provider.
func TestClaudeHooks_E2E_UserPromptSubmitAllowRuns(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	mustWrite(t, filepath.Join(home, ".claude", "hooks.json"),
		`{"Hooks":[{"Hooks":[{"Type":"UserPromptSubmit","Command":"echo '{\"decision\":\"approve\"}'"}]}]}`)

	t.Setenv("COVO_CLAUDE_HOOKS_DISABLED", "")
	t.Setenv("COVO_CLAUDE_HOOKS_PATH", "")
	t.Setenv("COVO_ACCEPT_HOOKS", "true")

	mock := &streamMockProvider{}
	ca, err := NewCovoAgent(CovoAgentConfig{
		Mode:         ModeGeneral,
		Provider:     mock,
		ProviderName: "mock",
		Model:        "mock-model",
		WorkingDir:   work,
		HomeDir:      home,
		Logger:       slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		ToolProfile:  "minimal",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	defer ca.Close()

	result, err := ca.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "direct-response" {
		t.Fatalf("Run result = %q, want %q", result, "direct-response")
	}
	if mock.calls == 0 {
		t.Fatal("expected provider to be called on the allow path")
	}
}
