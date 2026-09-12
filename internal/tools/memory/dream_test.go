package memory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMemoryExtractStoresFacts(t *testing.T) {
	dc := NewDreamCycle()
	tool := BuildMemoryExtractTool(dc)
	raw, _ := json.Marshal(map[string]any{
		"turns": "We decided to use SQLite for sessions.\nUser prefers terse answers.\n---\nshort",
	})
	out, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["count"].(int) < 1 {
		t.Fatalf("%#v", out)
	}
	if dc.Stats()["insight_count"].(int) < 1 {
		t.Fatal("expected stored insights")
	}
}

func TestMemoryDreamConsolidates(t *testing.T) {
	dc := NewDreamCycle()
	tool := BuildMemoryDreamTool(dc)
	raw, _ := json.Marshal(map[string]any{
		"memories": []map[string]any{
			{"id": "a", "content": "Use SQLite"},
			{"id": "b", "content": "Keep sessions local"},
		},
	})
	out, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["phase"] != "dream" {
		t.Fatalf("%#v", out)
	}
	insight, _ := m["insight"].(string)
	if !strings.Contains(insight, "SQLite") {
		t.Fatalf("insight=%q", insight)
	}
}
