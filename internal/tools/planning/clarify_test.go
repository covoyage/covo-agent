package planning

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestClarifyBlocksAndReturnsAnswer(t *testing.T) {
	tool := BuildClarifyTool(func(ctx context.Context, q string, opts []string, def string) (string, error) {
		if !strings.Contains(q, "Auth") {
			t.Fatalf("question=%q", q)
		}
		if len(opts) != 2 || opts[0] != "A" {
			t.Fatalf("opts=%v", opts)
		}
		return "B", nil
	})
	raw, _ := json.Marshal(map[string]any{
		"question": "which?",
		"header":   "Auth",
		"choices":  []string{"A", "B"},
	})
	out, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["response"] != "B" || m["used_default"] != false {
		t.Fatalf("%#v", out)
	}
}

func TestClarifyUsesDefaultWithoutCallback(t *testing.T) {
	tool := BuildClarifyTool(nil)
	raw, _ := json.Marshal(map[string]any{
		"question":       "which?",
		"default_choice": "skip",
	})
	out, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["response"] != "skip" || m["used_default"] != true {
		t.Fatalf("%#v", out)
	}
}

func TestClarifyRequiresDefaultHeadless(t *testing.T) {
	tool := BuildClarifyTool(nil)
	raw, _ := json.Marshal(map[string]any{"question": "which?"})
	if _, err := tool.Func(context.Background(), raw); err == nil {
		t.Fatal("expected error")
	}
}
