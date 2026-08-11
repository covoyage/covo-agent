package subagent

import (
	"testing"
	"time"
)

func newTestSubagentStore(t *testing.T) *SubagentStore {
	t.Helper()
	s, err := NewSubagentStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSubagentStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSubagentStore_CreateAndList(t *testing.T) {
	s := newTestSubagentStore(t)
	now := time.Now()
	rec := &SubagentRecord{
		ID:              "sub-1",
		ParentSessionID: "parent-sess",
		Task:            "do something",
		Status:          "running",
		Depth:           1,
		StartedAt:       now,
		LastHeartbeatAt: now,
	}
	if err := s.Create(rec); err != nil {
		t.Fatalf("Create: %v", err)
	}

	records, err := s.ListByStatus("running")
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 running, got %d", len(records))
	}
	if records[0].ID != "sub-1" || records[0].Task != "do something" {
		t.Fatalf("unexpected record: %+v", records[0])
	}
	if records[0].ParentSessionID != "parent-sess" {
		t.Fatalf("expected parent 'parent-sess', got %q", records[0].ParentSessionID)
	}
}

func TestSubagentStore_MarkCompleted(t *testing.T) {
	s := newTestSubagentStore(t)
	now := time.Now()
	_ = s.Create(&SubagentRecord{
		ID: "sub-1", Task: "t", Status: "running", StartedAt: now,
	})

	if err := s.MarkCompleted("sub-1", "completed", ""); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}

	running, _ := s.ListByStatus("running")
	if len(running) != 0 {
		t.Fatalf("expected 0 running after complete, got %d", len(running))
	}
	completed, _ := s.ListByStatus("completed")
	if len(completed) != 1 {
		t.Fatalf("expected 1 completed, got %d", len(completed))
	}
	if completed[0].EndedAt.IsZero() {
		t.Fatalf("expected non-zero EndedAt")
	}
}

func TestSubagentStore_MarkCompletedWithError(t *testing.T) {
	s := newTestSubagentStore(t)
	_ = s.Create(&SubagentRecord{
		ID: "sub-1", Task: "t", Status: "running", StartedAt: time.Now(),
	})
	_ = s.MarkCompleted("sub-1", "failed", "task failed: timeout")

	failed, _ := s.ListByStatus("failed")
	if len(failed) != 1 {
		t.Fatalf("expected 1 failed, got %d", len(failed))
	}
	if failed[0].Error != "task failed: timeout" {
		t.Fatalf("expected error message, got %q", failed[0].Error)
	}
}

func TestSubagentStore_UpdateHeartbeat(t *testing.T) {
	s := newTestSubagentStore(t)
	old := time.Now().Add(-5 * time.Minute)
	_ = s.Create(&SubagentRecord{
		ID: "sub-1", Task: "t", Status: "running", StartedAt: old, LastHeartbeatAt: old,
	})

	// Update heartbeat
	if err := s.UpdateHeartbeat("sub-1"); err != nil {
		t.Fatalf("UpdateHeartbeat: %v", err)
	}

	records, _ := s.ListByStatus("running")
	if len(records) != 1 {
		t.Fatalf("expected 1 running, got %d", len(records))
	}
	if !records[0].LastHeartbeatAt.After(old) {
		t.Fatalf("heartbeat should be updated to be more recent than %v, got %v", old, records[0].LastHeartbeatAt)
	}
}

func TestSubagentStore_Recover(t *testing.T) {
	s := newTestSubagentStore(t)
	// Create 2 running + 1 completed
	_ = s.Create(&SubagentRecord{ID: "sub-1", Task: "t1", Status: "running", StartedAt: time.Now()})
	_ = s.Create(&SubagentRecord{ID: "sub-2", Task: "t2", Status: "running", StartedAt: time.Now()})
	_ = s.Create(&SubagentRecord{ID: "sub-3", Task: "t3", Status: "completed", StartedAt: time.Now()})

	orphaned, err := s.Recover()
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(orphaned) != 2 {
		t.Fatalf("expected 2 orphaned, got %d", len(orphaned))
	}

	// After recover, no running should remain
	running, _ := s.ListByStatus("running")
	if len(running) != 0 {
		t.Fatalf("expected 0 running after recover, got %d", len(running))
	}
	// Orphaned should be 2
	orphans, _ := s.ListByStatus("orphaned")
	if len(orphans) != 2 {
		t.Fatalf("expected 2 orphaned status, got %d", len(orphans))
	}
	// Completed should be unaffected
	completed, _ := s.ListByStatus("completed")
	if len(completed) != 1 {
		t.Fatalf("expected 1 completed (unaffected), got %d", len(completed))
	}
}

func TestSubagentStore_RecoverEmpty(t *testing.T) {
	s := newTestSubagentStore(t)
	orphaned, err := s.Recover()
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if orphaned != nil {
		t.Fatalf("expected nil on empty recover, got %v", orphaned)
	}
}

func TestSubagentStore_ListByParent(t *testing.T) {
	s := newTestSubagentStore(t)
	_ = s.Create(&SubagentRecord{ID: "sub-1", ParentSessionID: "p1", Task: "t", Status: "running", StartedAt: time.Now()})
	_ = s.Create(&SubagentRecord{ID: "sub-2", ParentSessionID: "p2", Task: "t", Status: "running", StartedAt: time.Now()})
	_ = s.Create(&SubagentRecord{ID: "sub-3", ParentSessionID: "p1", Task: "t", Status: "running", StartedAt: time.Now()})

	records, err := s.ListByParent("p1")
	if err != nil {
		t.Fatalf("ListByParent: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 for p1, got %d", len(records))
	}
}

func TestSubagentStore_NextID(t *testing.T) {
	s := newTestSubagentStore(t)

	id1, err := s.NextID("sub")
	if err != nil {
		t.Fatalf("NextID 1: %v", err)
	}
	if id1 != "sub-1" {
		t.Fatalf("expected sub-1, got %q", id1)
	}
	// Create a record with this ID
	_ = s.Create(&SubagentRecord{ID: id1, Task: "t", Status: "running", StartedAt: time.Now()})

	id2, err := s.NextID("sub")
	if err != nil {
		t.Fatalf("NextID 2: %v", err)
	}
	if id2 != "sub-2" {
		t.Fatalf("expected sub-2, got %q", id2)
	}
}

func TestSubagentStore_NextIDWithGaps(t *testing.T) {
	s := newTestSubagentStore(t)
	// Create records with non-sequential IDs (simulating recovery)
	_ = s.Create(&SubagentRecord{ID: "sub-1", Task: "t", Status: "completed", StartedAt: time.Now()})
	_ = s.Create(&SubagentRecord{ID: "sub-5", Task: "t", Status: "orphaned", StartedAt: time.Now()})

	id, err := s.NextID("sub")
	if err != nil {
		t.Fatalf("NextID: %v", err)
	}
	if id != "sub-6" {
		t.Fatalf("expected sub-6 (max+1), got %q", id)
	}
}

func TestSubagentStore_GC(t *testing.T) {
	s := newTestSubagentStore(t)
	old := time.Now().Add(-48 * time.Hour)
	_ = s.Create(&SubagentRecord{ID: "sub-1", Task: "t", Status: "completed", StartedAt: old})
	_ = s.Create(&SubagentRecord{ID: "sub-2", Task: "t", Status: "running", StartedAt: time.Now()})

	deleted, err := s.GC(24 * time.Hour)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}
	// Recent record should survive
	running, _ := s.ListByStatus("running")
	if len(running) != 1 {
		t.Fatalf("expected 1 running after GC, got %d", len(running))
	}
}

func TestSubagentStore_PersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s1, err := NewSubagentStore(dir)
	if err != nil {
		t.Fatalf("NewSubagentStore s1: %v", err)
	}
	_ = s1.Create(&SubagentRecord{ID: "sub-1", Task: "survive", Status: "running", StartedAt: time.Now()})
	_ = s1.Close()

	s2, err := NewSubagentStore(dir)
	if err != nil {
		t.Fatalf("NewSubagentStore s2: %v", err)
	}
	defer s2.Close()
	running, _ := s2.ListByStatus("running")
	if len(running) != 1 || running[0].Task != "survive" {
		t.Fatalf("record did not survive reopen, got %v", running)
	}
}

// --- Registry persistence integration tests ---

func TestSubagentRegistry_Persistence(t *testing.T) {
	store := newTestSubagentStore(t)
	reg := NewSubagentRegistry()
	reg.SetStore(store, func() string { return "parent-sess" })

	id := reg.Start("task A", 1)
	if id != "sub-1" {
		t.Fatalf("expected sub-1, got %q", id)
	}

	// Verify persisted
	records, _ := store.ListByStatus("running")
	if len(records) != 1 {
		t.Fatalf("expected 1 persisted running, got %d", len(records))
	}
	if records[0].ParentSessionID != "parent-sess" {
		t.Fatalf("expected parent 'parent-sess', got %q", records[0].ParentSessionID)
	}

	// Complete
	reg.Complete(id, false)
	completed, _ := store.ListByStatus("completed")
	if len(completed) != 1 {
		t.Fatalf("expected 1 persisted completed, got %d", len(completed))
	}
	running, _ := store.ListByStatus("running")
	if len(running) != 0 {
		t.Fatalf("expected 0 running after complete, got %d", len(running))
	}
}

func TestSubagentRegistry_InterruptPersists(t *testing.T) {
	store := newTestSubagentStore(t)
	reg := NewSubagentRegistry()
	reg.SetStore(store, func() string { return "p" })

	ctx, id := reg.StartWithCancel(t.Context(), "task", 1)
	defer func() {
		// Cancel via context if still active
		select {
		case <-ctx.Done():
		default:
		}
	}()

	if !reg.Interrupt(id) {
		t.Fatalf("Interrupt returned false")
	}
	interrupted, _ := store.ListByStatus("interrupted")
	if len(interrupted) != 1 {
		t.Fatalf("expected 1 interrupted in store, got %d", len(interrupted))
	}
	if interrupted[0].Error == "" {
		t.Fatalf("expected error message for interrupted record")
	}
}

func TestSubagentRegistry_RecoverOrphaned(t *testing.T) {
	dir := t.TempDir()
	// First session: create a running subagent, then "crash" (close store without completing)
	s1, err := NewSubagentStore(dir)
	if err != nil {
		t.Fatalf("NewSubagentStore s1: %v", err)
	}
	reg1 := NewSubagentRegistry()
	reg1.SetStore(s1, func() string { return "p" })
	reg1.Start("task that was running when crash happened", 1)
	_ = s1.Close() // simulate crash

	// Second session: reopen, recover orphans
	s2, err := NewSubagentStore(dir)
	if err != nil {
		t.Fatalf("NewSubagentStore s2: %v", err)
	}
	defer s2.Close()
	reg2 := NewSubagentRegistry()
	reg2.SetStore(s2, func() string { return "p" })

	orphaned, err := reg2.RecoverOrphaned()
	if err != nil {
		t.Fatalf("RecoverOrphaned: %v", err)
	}
	if len(orphaned) != 1 {
		t.Fatalf("expected 1 orphaned, got %d", len(orphaned))
	}
	if orphaned[0].Task != "task that was running when crash happened" {
		t.Fatalf("unexpected orphan task: %q", orphaned[0].Task)
	}

	// Next ID should be sub-2 (not sub-1, since sub-1 is taken by orphan)
	id := reg2.Start("new task after recovery", 1)
	if id != "sub-2" {
		t.Fatalf("expected sub-2 after recovery, got %q", id)
	}
}

func TestSubagentRegistry_NoStoreFallback(t *testing.T) {
	// Without store, should work exactly like before (in-memory only)
	reg := NewSubagentRegistry()
	id := reg.Start("task", 0)
	if id != "sub-1" {
		t.Fatalf("expected sub-1, got %q", id)
	}
	// Complete should work without error
	reg.Complete(id, false)
	// ListOrphaned should return nil (no store)
	orphans, err := reg.ListOrphaned()
	if err != nil {
		t.Fatalf("ListOrphaned: %v", err)
	}
	if orphans != nil {
		t.Fatalf("expected nil orphans without store, got %v", orphans)
	}
}

func TestSubagentRegistry_IDConsistency(t *testing.T) {
	// Verify that in-memory counter stays consistent with store-allocated IDs
	store := newTestSubagentStore(t)
	reg := NewSubagentRegistry()
	reg.SetStore(store, func() string { return "" })

	id1 := reg.Start("t1", 0)
	id2 := reg.Start("t2", 0)
	if id1 != "sub-1" || id2 != "sub-2" {
		t.Fatalf("expected sub-1, sub-2; got %q, %q", id1, id2)
	}
}

func TestParseSubagentNum(t *testing.T) {
	tests := []struct {
		id   string
		want int
	}{
		{"sub-1", 1},
		{"sub-42", 42},
		{"sub-100", 100},
		{"sub-", 0},
		{"sub", 0},
		{"sub-abc", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := parseSubagentNum(tt.id)
		if got != tt.want {
			t.Errorf("parseSubagentNum(%q) = %d, want %d", tt.id, got, tt.want)
		}
	}
}
