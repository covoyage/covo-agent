package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestNestedRunCodeExecutorRunsInvokeToolHooks(t *testing.T) {
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

	out, err := nestedRunCodeExecutor(ag)(context.Background(), "echo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "pong" {
		t.Fatalf("got %#v", out)
	}
	if len(seen) != 1 || seen[0] != "echo" {
		t.Fatalf("nested ToolCall should run hooks, seen %v", seen)
	}
}

func TestNestedRunCodeExecutorRejectsSelf(t *testing.T) {
	ag := agentcore.New(agentcore.Config{})
	_, err := nestedRunCodeExecutor(ag)(context.Background(), "run_code", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "cannot invoke itself") {
		t.Fatalf("got %v", err)
	}
}
