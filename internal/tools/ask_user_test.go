package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func askUserCall(t *testing.T, cb AskUserFunc, args string) (map[string]any, error) {
	t.Helper()
	tool := buildAskUserTool(cb)
	raw := json.RawMessage(args)
	out, err := tool.Func(context.Background(), raw)
	if err != nil {
		return nil, err
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map", out)
	}
	return m, nil
}

func TestAskUserCallbackAnswer(t *testing.T) {
	gotOptions := []string(nil)
	cb := func(ctx context.Context, question string, options []string, defaultValue string) (string, error) {
		gotOptions = options
		return "option B", nil
	}
	m, err := askUserCall(t, cb, `{"question":"pick one","options":["A","B","C"],"default":"A"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["response"] != "option B" {
		t.Fatalf("response = %q, want %q", m["response"], "option B")
	}
	if m["used_default"] != false {
		t.Fatalf("used_default = %v, want false", m["used_default"])
	}
	if len(gotOptions) != 3 || gotOptions[1] != "B" {
		t.Fatalf("options passed = %v, want [A B C]", gotOptions)
	}
}

func TestAskUserFallsBackToDefault(t *testing.T) {
	tests := []struct {
		name string
		cb   AskUserFunc
	}{
		{name: "callback error", cb: func(ctx context.Context, q string, o []string, d string) (string, error) {
			return "", errors.New("no user")
		}},
		{name: "empty answer", cb: func(ctx context.Context, q string, o []string, d string) (string, error) {
			return "", nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := askUserCall(t, tt.cb, `{"question":"q","default":"fallback"}`)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m["response"] != "fallback" {
				t.Fatalf("response = %q, want %q", m["response"], "fallback")
			}
			if m["used_default"] != true {
				t.Fatalf("used_default = %v, want true", m["used_default"])
			}
		})
	}
}

func TestAskUserNoCallbackWithDefault(t *testing.T) {
	m, err := askUserCall(t, nil, `{"question":"q","default":"headless default"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["response"] != "headless default" {
		t.Fatalf("response = %q, want default", m["response"])
	}
}

func TestAskUserNoCallbackWithoutDefaultFails(t *testing.T) {
	_, err := askUserCall(t, nil, `{"question":"q"}`)
	if err == nil {
		t.Fatal("expected error when no callback and no default")
	}
}

func TestAskUserTimeoutUsesDefault(t *testing.T) {
	cb := func(ctx context.Context, q string, o []string, d string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	// timeout_seconds=1 with a callback that waits on ctx.Done would block for
	// a second; instead use a pre-cancelled context to exercise the path fast.
	tool := buildAskUserTool(cb)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m, err := tool.Func(ctx, json.RawMessage(`{"question":"q","default":"d"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	res := m.(map[string]any)
	if res["response"] != "d" {
		t.Fatalf("response = %q, want default on cancelled ctx", res["response"])
	}
}

func TestAskUserRequiresQuestion(t *testing.T) {
	_, err := askUserCall(t, func(ctx context.Context, q string, o []string, d string) (string, error) {
		return "x", nil
	}, `{"question":"   "}`)
	if err == nil {
		t.Fatal("expected error for empty question")
	}
}

func TestAskUserCallbackErrorWithoutDefaultFails(t *testing.T) {
	_, err := askUserCall(t, func(ctx context.Context, q string, o []string, d string) (string, error) {
		return "", errors.New("no user")
	}, `{"question":"q"}`)
	if err == nil || !strings.Contains(err.Error(), "no user") {
		t.Fatalf("err = %v, want wrapped callback error", err)
	}
}
