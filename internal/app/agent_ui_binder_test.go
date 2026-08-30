package app

import (
	"strings"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

func TestRetryUIStateFlushesOnFailure(t *testing.T) {
	flushed := make(chan string, 1)
	state := newRetryUIState(func(message string) { flushed <- message })
	state.onRetry(&agentcore.AutoRetryEvent{Attempt: 1, MaxRetries: 3, Delay: 1500 * time.Millisecond})
	state.onRetry(&agentcore.AutoRetryEvent{Attempt: 2, MaxRetries: 3, Delay: 2 * time.Second})
	state.onFailure()

	select {
	case message := <-flushed:
		for _, want := range []string{"retry 1/3", "retry 2/3", "1.5s", "2s"} {
			if !strings.Contains(message, want) {
				t.Errorf("flushed retry status missing %q: %q", want, message)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("retry status was not flushed")
	}
}

func TestRetryUIStateDiscardsOnSuccess(t *testing.T) {
	flushed := make(chan string, 1)
	state := newRetryUIState(func(message string) { flushed <- message })
	state.onRetry(&agentcore.AutoRetryEvent{Attempt: 1, MaxRetries: 2, Delay: time.Second})
	state.onSuccess()
	select {
	case message := <-flushed:
		t.Fatalf("successful retry unexpectedly flushed %q", message)
	default:
	}
}

func TestFormatTokenUsage(t *testing.T) {
	usage := agentcore.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	if got := formatTokenUsage(usage); got != "tokens: 10 in + 5 out = 15" {
		t.Fatalf("formatTokenUsage() = %q", got)
	}
}

func TestAgentUIBinderNilCoreIsSafe(t *testing.T) {
	binder := &AgentUIBinder{}
	binder.Bind(nil)
}

func TestFormatCompletionChip(t *testing.T) {
	got := formatCompletionChip("code", "gpt-4.1", 5500*time.Millisecond)
	if got != "code · gpt-4.1 · 5.5s" {
		t.Fatalf("formatCompletionChip() = %q", got)
	}
	got = formatCompletionChip("", shortModelName("openai/gpt-4.1"), 12*time.Second)
	if got != "gpt-4.1 · 12s" {
		t.Fatalf("formatCompletionChip() = %q", got)
	}
	got = formatCompletionChip("code", "gpt-4.1", 83*time.Second)
	if got != "code · gpt-4.1 · 1m23s" {
		t.Fatalf("formatCompletionChip() = %q", got)
	}
}

func TestShortModelName(t *testing.T) {
	if got := shortModelName("openai/gpt-4.1"); got != "gpt-4.1" {
		t.Fatalf("shortModelName() = %q", got)
	}
	if got := shortModelName("  gpt-4.1  "); got != "gpt-4.1" {
		t.Fatalf("shortModelName() = %q", got)
	}
}
