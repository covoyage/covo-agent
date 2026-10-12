package tools

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/covoyage/covonaut/agentcore"
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

func TestAskUserExtensionLateBinding(t *testing.T) {
	home := t.TempDir()
	extension := NewExtension(ExtensionConfig{
		HomeDir: home, SessionsDir: filepath.Join(home, "sessions"), WorkDir: home,
	})
	t.Cleanup(func() { _ = extension.Dispose() })
	workshop := evolution.NewWorkshopStore(home)
	if err := workshop.Init(); err != nil {
		t.Fatal(err)
	}
	extension.SetWorkshopStore(workshop)
	core := agentcore.New(agentcore.Config{})
	t.Cleanup(core.Close)
	if err := extension.Init(context.Background(), core); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"clarify", "ask_user"} {
		t.Run(name, func(t *testing.T) {
			tool, ok := core.GetTool(name)
			if !ok {
				t.Fatalf("%s not registered", name)
			}
			extension.SetAskUserCallback(nil)
			question := json.RawMessage(`{"question":"pick one","choices":["A","B"]}`)
			if name == "ask_user" {
				question = json.RawMessage(`{"question":"pick one","options":["A","B"]}`)
			}
			if _, err := tool.Func(context.Background(), question); err == nil || !strings.Contains(err.Error(), "no interactive user available") {
				t.Fatalf("unbound callback: %v", err)
			}
			calls := 0
			extension.SetAskUserCallback(func(ctx context.Context, q string, options []string, defaultValue string) (string, error) {
				calls++
				if q != "pick one" || len(options) != 2 || options[1] != "B" || defaultValue != "" {
					t.Errorf("callback arguments: q=%q options=%v default=%q", q, options, defaultValue)
				}
				extension.SetAskUserCallback(nil)
				return "B", nil
			})
			done := make(chan struct{})
			var out any
			var callErr error
			go func() {
				out, callErr = tool.Func(context.Background(), question)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("callback replacement blocked while awaiting user")
			}
			if callErr != nil {
				t.Fatal(callErr)
			}
			result := out.(map[string]any)
			if calls != 1 || result["response"] != "B" || result["used_default"] != false {
				t.Fatalf("late-bound result: %#v calls=%d", result, calls)
			}
			fallback := json.RawMessage(`{"question":"q","default":"skip","default_choice":"skip"}`)
			out, err := tool.Func(context.Background(), fallback)
			if err != nil {
				t.Fatal(err)
			}
			result = out.(map[string]any)
			if result["response"] != "skip" || result["used_default"] != true {
				t.Fatalf("disabled callback fallback: %#v", result)
			}
			extension.SetAskUserCallback(func(context.Context, string, []string, string) (string, error) {
				return "updated", nil
			})
			out, err = tool.Func(context.Background(), question)
			if err != nil || out.(map[string]any)["response"] != "updated" {
				t.Fatalf("replaced callback: %#v err=%v", out, err)
			}
		})
	}
}

func TestAskUserCallbackConcurrentReplacement(t *testing.T) {
	extension := &Extension{}
	callback := func(context.Context, string, []string, string) (string, error) { return "answer", nil }
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		for iteration := 0; iteration < 1000; iteration++ {
			extension.SetAskUserCallback(callback)
			extension.SetAskUserCallback(nil)
		}
	}()
	go func() {
		defer workers.Done()
		for iteration := 0; iteration < 1000; iteration++ {
			answer, err := extension.askUser(context.Background(), "q", nil, "")
			if err != nil {
				if !strings.Contains(err.Error(), "no interactive user available") {
					t.Errorf("unexpected callback error: %v", err)
				}
			} else if answer != "answer" {
				t.Errorf("unexpected answer: %q", answer)
			}
		}
	}()
	workers.Wait()
}
