package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestToolCallRejectsSelf(t *testing.T) {
	ag := agentcore.New(agentcore.Config{})
	tool := buildToolCallTool(ag)
	raw, _ := json.Marshal(map[string]any{
		"name":      "tool_call",
		"arguments": map[string]any{},
	})
	_, err := tool.Func(context.Background(), raw)
	if err == nil || !strings.Contains(err.Error(), "cannot invoke itself") {
		t.Fatalf("expected self-call rejection, got %v", err)
	}
}

func TestToolCallRunsInvokeToolHooks(t *testing.T) {
	var seen []string
	ag := agentcore.New(agentcore.Config{
		ExecutionConfig: agentcore.ExecutionConfig{
			GlobalBefore: []agentcore.BeforeHook{
				func(_ context.Context, hc *agentcore.HookContext) error {
					seen = append(seen, hc.ToolName)
					return nil
				},
			},
		},
	})
	echo := &agentcore.Tool{
		Name: "echo",
		Func: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "pong", nil
		},
	}
	ag.RegisterTools(echo)
	if _, err := echo.Func(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 0 {
		t.Fatalf("direct Func must not run hooks, seen %v", seen)
	}

	call := buildToolCallTool(ag)
	raw, _ := json.Marshal(map[string]any{
		"name":      "echo",
		"arguments": map[string]any{},
	})
	out, err := call.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["status"] != "ok" {
		t.Fatalf("unexpected result %#v", out)
	}
	if len(seen) != 1 || seen[0] != "echo" {
		t.Fatalf("InvokeTool should run hooks, seen %v", seen)
	}
}
