package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestRedactToolResultAfterToolCall(t *testing.T) {
	ca := &CovoAgent{}
	hook := ca.redactToolResultAfterToolCall()

	secret := "sk-live-1234567890abcd"
	result := &agentcore.ToolResult{
		ToolName: "bash",
		Result:   "token: " + secret,
		ForLLM:   "token: " + secret,
		ForUser:  "token: " + secret,
	}
	got := hook(context.Background(), agentcore.ToolCall{}, result)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	for _, field := range []struct{ name, value string }{
		{"Result", got.Result},
		{"ForLLM", got.ForLLM},
		{"ForUser", got.ForUser},
	} {
		if strings.Contains(field.value, secret) {
			t.Errorf("%s: secret was not redacted: %q", field.name, field.value)
		}
	}
}

func TestRedactToolResultAfterToolCall_NilResult(t *testing.T) {
	ca := &CovoAgent{}
	hook := ca.redactToolResultAfterToolCall()
	if got := hook(context.Background(), agentcore.ToolCall{}, nil); got != nil {
		t.Errorf("expected nil for nil result, got %#v", got)
	}
}
