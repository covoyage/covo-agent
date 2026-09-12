package tools

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestStructuredOutputWritesFile(t *testing.T) {
	tool := buildStructuredOutputTool()
	raw, _ := json.Marshal(map[string]any{
		"headline": "done",
		"summary":  "finished the task",
	})
	out, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	path, _ := m["path"].(string)
	if path == "" {
		t.Fatal("missing path")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path)
}
