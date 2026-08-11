package audit

import (
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStore_LogAndQuery(t *testing.T) {
	s := newTestStore(t)
	if err := s.Log("tool:start", "sess-1", "edit_block", "agent-1", map[string]any{"path": "/tmp/x"}); err != nil {
		t.Fatalf("Log: %v", err)
	}
	if err := s.Log("tool:end", "sess-1", "edit_block", "agent-1", map[string]any{"status": "ok"}); err != nil {
		t.Fatalf("Log: %v", err)
	}
	entries, err := s.Query(QueryFilter{Limit: 10})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Newest-first
	if entries[0].EventType != "tool:end" {
		t.Fatalf("expected newest first, got %q", entries[0].EventType)
	}
	if entries[1].ToolName != "edit_block" {
		t.Fatalf("expected tool_name 'edit_block', got %q", entries[1].ToolName)
	}
}

func TestStore_FilterByEventType(t *testing.T) {
	s := newTestStore(t)
	_ = s.Log("tool:start", "s", "t", "a", nil)
	_ = s.Log("tool:end", "s", "t", "a", nil)
	_ = s.Log("session:start", "s", "", "a", nil)

	entries, _ := s.Query(QueryFilter{EventType: "tool:start"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 tool:start, got %d", len(entries))
	}
	entries, _ = s.Query(QueryFilter{EventType: "tool:end"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 tool:end, got %d", len(entries))
	}
}

func TestStore_FilterBySession(t *testing.T) {
	s := newTestStore(t)
	_ = s.Log("tool:start", "sess-A", "t", "a", nil)
	_ = s.Log("tool:start", "sess-B", "t", "a", nil)

	entries, _ := s.Query(QueryFilter{SessionID: "sess-A"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 for sess-A, got %d", len(entries))
	}
	if entries[0].SessionID != "sess-A" {
		t.Fatalf("expected sess-A, got %q", entries[0].SessionID)
	}
}

func TestStore_FilterByToolName(t *testing.T) {
	s := newTestStore(t)
	_ = s.Log("tool:start", "s", "edit_block", "a", nil)
	_ = s.Log("tool:start", "s", "write_file", "a", nil)

	entries, _ := s.Query(QueryFilter{ToolName: "edit_block"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 edit_block, got %d", len(entries))
	}
}

func TestStore_Count(t *testing.T) {
	s := newTestStore(t)
	_ = s.Log("tool:start", "s", "t", "a", nil)
	_ = s.Log("tool:end", "s", "t", "a", nil)
	_ = s.Log("session:start", "s", "", "a", nil)

	n, err := s.Count(QueryFilter{})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 total, got %d", n)
	}
	n, _ = s.Count(QueryFilter{EventType: "tool:start"})
	if n != 1 {
		t.Fatalf("expected 1 tool:start, got %d", n)
	}
}

func TestStore_LimitAndOffset(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 5; i++ {
		_ = s.Log("tool:start", "s", "t", "a", nil)
		time.Sleep(5 * time.Millisecond)
	}
	entries, _ := s.Query(QueryFilter{Limit: 2})
	if len(entries) != 2 {
		t.Fatalf("expected 2 with limit, got %d", len(entries))
	}
	entries2, _ := s.Query(QueryFilter{Limit: 2, Offset: 2})
	if len(entries2) != 2 {
		t.Fatalf("expected 2 with offset, got %d", len(entries2))
	}
	// Ensure offset returns different entries
	if entries[0].ID == entries2[0].ID {
		t.Fatalf("offset should return different entries")
	}
}

func TestStore_GC(t *testing.T) {
	s := newTestStore(t)
	_ = s.Log("tool:start", "s", "t", "a", nil)
	// Insert an old entry directly
	old := time.Now().Add(-48 * time.Hour).Unix()
	_, err := s.db.Exec(
		`INSERT INTO audit_log(event_type, session_id, tool_name, agent_id, data, created_at) VALUES(?, ?, ?, ?, ?, ?)`,
		"tool:start", "s", "t", "a", "", old,
	)
	if err != nil {
		t.Fatalf("insert old: %v", err)
	}
	deleted, err := s.GC(24 * time.Hour)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}
	n, _ := s.Count(QueryFilter{})
	if n != 1 {
		t.Fatalf("expected 1 remaining after GC, got %d", n)
	}
}

func TestStore_PersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s1, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore s1: %v", err)
	}
	_ = s1.Log("tool:start", "s", "t", "a", "test data")
	_ = s1.Close()

	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore s2: %v", err)
	}
	defer s2.Close()
	entries, _ := s2.Query(QueryFilter{Limit: 10})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after reopen, got %d", len(entries))
	}
	if entries[0].Data == "" {
		t.Fatalf("data should survive reopen")
	}
}

func TestStore_NilData(t *testing.T) {
	s := newTestStore(t)
	if err := s.Log("session:start", "s", "", "a", nil); err != nil {
		t.Fatalf("Log with nil data: %v", err)
	}
	entries, _ := s.Query(QueryFilter{Limit: 1})
	if len(entries) != 1 {
		t.Fatalf("expected 1, got %d", len(entries))
	}
	if entries[0].Data != "" {
		t.Fatalf("expected empty data string, got %q", entries[0].Data)
	}
}

func TestStore_DefaultLimit(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 5; i++ {
		_ = s.Log("tool:start", "s", "t", "a", nil)
	}
	// Limit=0 should default to 100, returning all 5
	entries, _ := s.Query(QueryFilter{Limit: 0})
	if len(entries) != 5 {
		t.Fatalf("expected 5 with default limit, got %d", len(entries))
	}
}
