package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func spillCall(t *testing.T, homeDir, sessionID, args string) (map[string]any, error) {
	t.Helper()
	tool := BuildSpillTool(homeDir, func() string { return sessionID })
	out, err := tool.Func(context.Background(), json.RawMessage(args))
	if err != nil {
		return nil, err
	}
	return out.(map[string]any), nil
}

func spillListCall(t *testing.T, homeDir, sessionID, args string) (map[string]any, error) {
	t.Helper()
	tool := BuildSpillListTool(homeDir, func() string { return sessionID })
	out, err := tool.Func(context.Background(), json.RawMessage(args))
	if err != nil {
		return nil, err
	}
	return out.(map[string]any), nil
}

func TestSpillWritesFileAndLineage(t *testing.T) {
	home := t.TempDir()

	m, err := spillCall(t, home, "sess-1", `{"text":"hello world","name":"note","purpose":"keep for later"}`)
	if err != nil {
		t.Fatalf("spill error: %v", err)
	}

	path, _ := m["path"].(string)
	if path == "" {
		t.Fatal("spill returned empty path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spilled file: %v", err)
	}
	if string(data) != "hello world" {
		t.Fatalf("content = %q, want %q", string(data), "hello world")
	}

	lineage, err := os.ReadFile(filepath.Join(home, "spill", spillLineageFile))
	if err != nil {
		t.Fatalf("read lineage: %v", err)
	}
	if !strings.Contains(string(lineage), "sess-1") || !strings.Contains(string(lineage), "note") {
		t.Fatalf("lineage missing session/name: %s", lineage)
	}
}

func TestSpillListFiltersBySessionAndPurpose(t *testing.T) {
	home := t.TempDir()

	if _, err := spillCall(t, home, "sess-A", `{"text":"alpha","name":"a","purpose":"parsing"}`); err != nil {
		t.Fatalf("spill A: %v", err)
	}
	if _, err := spillCall(t, home, "sess-A", `{"text":"beta","name":"b","purpose":"report"}`); err != nil {
		t.Fatalf("spill B: %v", err)
	}
	if _, err := spillCall(t, home, "sess-B", `{"text":"gamma","name":"c","purpose":"parsing"}`); err != nil {
		t.Fatalf("spill C: %v", err)
	}

	// Session A sees only its own spills.
	m, err := spillListCall(t, home, "sess-A", `{}`)
	if err != nil {
		t.Fatalf("spill_list error: %v", err)
	}
	entries := m["entries"].([]SpillEntry)
	if len(entries) != 2 {
		t.Fatalf("session A entries = %d, want 2", len(entries))
	}

	// Purpose filter narrows further.
	m, err = spillListCall(t, home, "sess-A", `{"purpose":"parsing"}`)
	if err != nil {
		t.Fatalf("spill_list purpose error: %v", err)
	}
	entries = m["entries"].([]SpillEntry)
	if len(entries) != 1 {
		t.Fatalf("purpose-filtered entries = %d, want 1", len(entries))
	}
	first := entries[0]
	if first.Name != "a" {
		t.Fatalf("filtered entry name = %q, want a", first.Name)
	}

	// Limit applies.
	m, err = spillListCall(t, home, "sess-A", `{"limit":1}`)
	if err != nil {
		t.Fatalf("spill_list limit error: %v", err)
	}
	if got := m["count"]; got != 1 {
		t.Fatalf("count = %v, want 1", got)
	}
}

func TestSpillRequiresText(t *testing.T) {
	_, err := spillCall(t, t.TempDir(), "s", `{"text":"  "}`)
	if err == nil {
		t.Fatal("expected error for empty text")
	}
}

func TestSpillListEmptyStore(t *testing.T) {
	m, err := spillListCall(t, t.TempDir(), "s", `{}`)
	if err != nil {
		t.Fatalf("spill_list error: %v", err)
	}
	if m["count"] != 0 {
		t.Fatalf("count = %v, want 0", m["count"])
	}
}

func TestSanitizeSpillName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"webpack config", "webpack_config"},
		{"../evil", "evil"},
		{"", ""},
		{"keep-dots.v1", "keep-dots.v1"},
	}
	for _, tt := range tests {
		if got := sanitizeSpillName(tt.in); got != tt.want {
			t.Fatalf("sanitize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
