package sessions

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSessionsSendRequiresTargetAndCallback(t *testing.T) {
	tool := BuildSessionsSendTool(nil)
	raw, _ := json.Marshal(map[string]any{"message": "hi", "target": "abc"})
	_, err := tool.Func(context.Background(), raw)
	if err == nil || !strings.Contains(err.Error(), "no inbox") {
		t.Fatalf("got %v", err)
	}

	wired := BuildSessionsSendTool(func(_ context.Context, target, msg string) (string, error) {
		if target != "abc" || msg != "hi" {
			t.Fatalf("target=%q msg=%q", target, msg)
		}
		return "ok", nil
	})
	out, err := wired.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["status"] != "sent" {
		t.Fatalf("got %#v", out)
	}

	raw, _ = json.Marshal(map[string]any{"message": "hi"})
	if _, err := wired.Func(context.Background(), raw); err == nil {
		t.Fatal("expected target required")
	}
}
