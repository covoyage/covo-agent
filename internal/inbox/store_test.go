package inbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(dir, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStore_SendAndDrain(t *testing.T) {
	s := newTestStore(t)

	id, err := s.Send("parent-1", "child-1", "task done")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	// Count before drain
	n, err := s.Count("parent-1")
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 pending, got %d", n)
	}

	msgs, err := s.Drain("parent-1")
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Message != "task done" {
		t.Fatalf("expected 'task done', got %q", msgs[0].Message)
	}
	if msgs[0].SenderSession != "child-1" {
		t.Fatalf("expected sender 'child-1', got %q", msgs[0].SenderSession)
	}
	if msgs[0].Status != "pending" {
		t.Fatalf("drain should return pending status, got %q", msgs[0].Status)
	}

	// After drain, count should be 0
	n, _ = s.Count("parent-1")
	if n != 0 {
		t.Fatalf("expected 0 pending after drain, got %d", n)
	}

	// Second drain returns nil
	msgs2, err := s.Drain("parent-1")
	if err != nil {
		t.Fatalf("Drain (2): %v", err)
	}
	if msgs2 != nil {
		t.Fatalf("expected nil on empty drain, got %v", msgs2)
	}
}

func TestStore_DrainOrder(t *testing.T) {
	s := newTestStore(t)
	for _, m := range []string{"first", "second", "third"} {
		if _, err := s.Send("p", "s", m); err != nil {
			t.Fatalf("Send %s: %v", m, err)
		}
		time.Sleep(5 * time.Millisecond) // ensure distinct created_at
	}
	msgs, err := s.Drain("p")
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3, got %d", len(msgs))
	}
	if msgs[0].Message != "first" || msgs[1].Message != "second" || msgs[2].Message != "third" {
		t.Fatalf("expected oldest-first order, got %v %v %v", msgs[0].Message, msgs[1].Message, msgs[2].Message)
	}
}

func TestStore_PeekDoesNotMark(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Send("p", "s", "msg"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	msgs, err := s.Peek("p")
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1, got %d", len(msgs))
	}
	n, _ := s.Count("p")
	if n != 1 {
		t.Fatalf("peek should not drain, expected 1 pending, got %d", n)
	}
}

func TestStore_EmptyInbox(t *testing.T) {
	s := newTestStore(t)
	msgs, err := s.Drain("nonexistent")
	if err != nil {
		t.Fatalf("Drain on empty: %v", err)
	}
	if msgs != nil {
		t.Fatalf("expected nil, got %v", msgs)
	}
	n, _ := s.Count("nonexistent")
	if n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
}

func TestStore_SendValidation(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Send("", "s", "m"); err == nil {
		t.Fatal("expected error for empty recipient")
	}
	if _, err := s.Send("p", "s", ""); err == nil {
		t.Fatal("expected error for empty message")
	}
}

func TestStore_Isolation(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Send("p1", "c", "for p1"); err != nil {
		t.Fatalf("Send p1: %v", err)
	}
	if _, err := s.Send("p2", "c", "for p2"); err != nil {
		t.Fatalf("Send p2: %v", err)
	}
	msgs, _ := s.Drain("p1")
	if len(msgs) != 1 || msgs[0].Message != "for p1" {
		t.Fatalf("p1 should only get its own message, got %v", msgs)
	}
	msgs2, _ := s.Drain("p2")
	if len(msgs2) != 1 || msgs2[0].Message != "for p2" {
		t.Fatalf("p2 should only get its own message, got %v", msgs2)
	}
}

func TestStore_GC(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	// Insert an old drained message directly via raw SQL
	old := time.Now().Add(-8 * 24 * time.Hour).Unix()
	_, err = s.db.Exec(
		`INSERT INTO inbox(recipient_session_id, sender_session_id, message, created_at, status) VALUES(?, ?, ?, ?, 'drained')`,
		"p", "c", "old drained", old,
	)
	if err != nil {
		t.Fatalf("insert old drained: %v", err)
	}
	// Insert a recent pending message
	if _, err := s.Send("p", "c", "recent pending"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	deleted, err := s.GC(7 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted (old drained), got %d", deleted)
	}

	// Recent pending should survive
	n, _ := s.Count("p")
	if n != 1 {
		t.Fatalf("expected 1 pending after GC, got %d", n)
	}
}

func TestStore_GC_PendingGrace(t *testing.T) {
	s := newTestStore(t)
	// Pending messages get 2x grace period, so an old pending message
	// within 2*maxAge should survive.
	old := time.Now().Add(-8 * 24 * time.Hour).Unix()
	_, err := s.db.Exec(
		`INSERT INTO inbox(recipient_session_id, sender_session_id, message, created_at, status) VALUES(?, ?, ?, ?, 'pending')`,
		"p", "c", "old pending within grace", old,
	)
	if err != nil {
		t.Fatalf("insert old pending: %v", err)
	}
	deleted, err := s.GC(7 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("pending within 2*maxAge should survive, got %d deleted", deleted)
	}
}

func TestStore_PersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s1, err := NewStore(dir, 0)
	if err != nil {
		t.Fatalf("NewStore s1: %v", err)
	}
	if _, err := s1.Send("p", "c", "survive restart"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_ = s1.Close()

	// Reopen — message must survive
	s2, err := NewStore(dir, 0)
	if err != nil {
		t.Fatalf("NewStore s2: %v", err)
	}
	defer s2.Close()
	msgs, err := s2.Drain("p")
	if err != nil {
		t.Fatalf("Drain after reopen: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Message != "survive restart" {
		t.Fatalf("message did not survive reopen, got %v", msgs)
	}
}

func TestStore_DBSchemaMigration(t *testing.T) {
	dir := t.TempDir()
	// First open creates the schema
	s1, err := NewStore(dir, 0)
	if err != nil {
		t.Fatalf("NewStore s1: %v", err)
	}
	_ = s1.Close()
	// Second open should not error (idempotent migrate)
	s2, err := NewStore(dir, 0)
	if err != nil {
		t.Fatalf("NewStore s2 (re-migrate): %v", err)
	}
	defer s2.Close()

	// Verify table exists and is usable
	if _, err := s2.Send("p", "s", "m"); err != nil {
		t.Fatalf("Send after re-migrate: %v", err)
	}
}

func TestStore_ConcurrentSend(t *testing.T) {
	s := newTestStore(t)
	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func(i int) {
			_, err := s.Send("p", "c", "concurrent")
			done <- err
		}(i)
	}
	for i := 0; i < 10; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent send %d: %v", i, err)
		}
	}
	n, _ := s.Count("p")
	if n != 10 {
		t.Fatalf("expected 10 pending, got %d", n)
	}
}

func TestMain(m *testing.M) {
	// Ensure temp dirs are cleaned even on panic
	code := m.Run()
	_ = filepath.Glob
	os.Exit(code)
}
