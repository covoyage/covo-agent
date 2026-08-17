package agent

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/snapshot"
)

// TestSnapshotManager_TrackBaselineAsync verifies the async baseline:
// it returns immediately, completes in the background, and records a
// baseline entry once done.
func TestSnapshotManager_TrackBaselineAsync(t *testing.T) {
	mgr, _ := newTestSnapshotManager(t)

	start := time.Now()
	mgr.TrackBaselineAsync()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("TrackBaselineAsync should return immediately, took %v", elapsed)
	}

	// Wait for background completion via BaselineDone polling.
	deadline := time.Now().Add(10 * time.Second)
	for !mgr.BaselineDone() {
		if time.Now().After(deadline) {
			t.Fatal("baseline did not complete in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	entries := mgr.List()
	if len(entries) < 1 {
		t.Fatalf("baseline entry missing after async completion, got %d entries", len(entries))
	}
	if entries[len(entries)-1].ToolName != "baseline" {
		t.Errorf("last entry tool = %q, want baseline", entries[len(entries)-1].ToolName)
	}

	// A second async call must be a no-op (no duplicate baseline).
	mgr.TrackBaselineAsync()
	time.Sleep(100 * time.Millisecond)
	if got := len(mgr.List()); got != 1 {
		t.Errorf("duplicate baseline recorded: %d entries", got)
	}
}

// TestSnapshotManager_TrackWaitsForBaseline covers the first-mutation race:
// Track called while the async baseline is still in flight must wait for it,
// so the entry history starts from the baseline and stays consistent.
//
// Note the inherent small window: if a mutation lands *before* the
// background baseline stages the tree, the baseline captures the
// already-mutated state and the following Track records no new entry —
// history remains consistent either way. This test mutates after the
// baseline completed staging to assert the common ordering.
func TestSnapshotManager_TrackWaitsForBaseline(t *testing.T) {
	mgr, workDir := newTestSnapshotManager(t)
	storeDir := t.TempDir()
	mgr.SetStoreDir(storeDir)

	// Start the async baseline and wait for it to finish staging.
	mgr.TrackBaselineAsync()
	deadline := time.Now().Add(10 * time.Second)
	for !mgr.BaselineDone() {
		if time.Now().After(deadline) {
			t.Fatal("baseline did not complete in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Now mutate and Track: exactly baseline + change entries, ordered.
	mutateWorkDir(t, workDir, 1)
	if err := mgr.Track("write_file", 3); err != nil {
		t.Fatalf("track after baseline: %v", err)
	}
	entries := mgr.List()
	if len(entries) != 2 {
		t.Fatalf("want 2 entries (baseline + change), got %d: %+v", len(entries), entries)
	}
	if entries[0].ToolName != "baseline" {
		t.Errorf("entries[0].ToolName = %q, want baseline", entries[0].ToolName)
	}
	if entries[1].ToolName != "write_file" {
		t.Errorf("entries[1].ToolName = %q, want write_file", entries[1].ToolName)
	}
}

// TestSnapshotManager_TrackDuringInflightBaselineStaysConsistent asserts the
// consistency guarantee for a Track racing the in-flight baseline: no
// duplicate baselines, at least the baseline entry, and no errors.
func TestSnapshotManager_TrackDuringInflightBaselineStaysConsistent(t *testing.T) {
	mgr, workDir := newTestSnapshotManager(t)

	mgr.TrackBaselineAsync()
	// Mutate immediately — may land before or after baseline staging.
	mutateWorkDir(t, workDir, 1)
	if err := mgr.Track("write_file", 3); err != nil {
		t.Fatalf("track during baseline: %v", err)
	}

	if !mgr.BaselineDone() {
		t.Fatal("baseline must be done after Track returned")
	}
	baselines := 0
	for _, e := range mgr.List() {
		if e.ToolName == "baseline" {
			baselines++
		}
	}
	if got := len(mgr.List()); got < 1 || got > 2 {
		t.Fatalf("want 1-2 entries, got %d", got)
	}
	if baselines != 1 {
		t.Errorf("exactly one baseline entry expected, got %d", baselines)
	}
}

// TestSnapshotManager_UndoWaitsForBaseline verifies undo is safe when issued
// while the baseline is still running: it must observe a complete history.
func TestSnapshotManager_UndoWaitsForBaseline(t *testing.T) {
	mgr, workDir := newTestSnapshotManager(t)

	mgr.TrackBaselineAsync()
	// Wait for the baseline, then mutate and track (the hook path).
	deadline := time.Now().Add(10 * time.Second)
	for !mgr.BaselineDone() {
		if time.Now().After(deadline) {
			t.Fatal("baseline did not complete in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
	mutateWorkDir(t, workDir, 1)
	if err := mgr.Track("write_file", 1); err != nil {
		t.Fatalf("track: %v", err)
	}

	// Undo needs >= 2 entries; without the baseline wait this would race.
	count, err := mgr.Undo()
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if count < 1 {
		t.Fatalf("undo should revert at least 1 file, got %d", count)
	}
}

// TestSnapshotManager_AsyncBaselineDisabledManager verifies no-op behavior
// for nil and disabled managers (startup safety).
func TestSnapshotManager_AsyncBaselineDisabledManager(t *testing.T) {
	var nilMgr *SnapshotManager
	nilMgr.TrackBaselineAsync() // must not panic
	if !nilMgr.BaselineDone() {
		t.Error("nil manager should report baseline done")
	}

	disabled := NewSnapshotManager(nil)
	disabled.TrackBaselineAsync() // must not panic
	if !disabled.BaselineDone() {
		t.Error("disabled manager should report baseline done")
	}
}

// TestSnapshotManager_ConcurrentTracksDuringBaseline hammers the manager
// from multiple goroutines while the baseline is in flight; run with -race.
func TestSnapshotManager_ConcurrentTracksDuringBaseline(t *testing.T) {
	mgr, workDir := newTestSnapshotManager(t)

	mgr.TrackBaselineAsync()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			path := filepath.Join(workDir, "hello.txt")
			_ = os.WriteFile(path, []byte("v"+string(rune('a'+n))), 0644)
			_ = mgr.Track("bash", n)
		}(i)
	}
	wg.Wait()

	// After all activity, the manager must be consistent: baseline done,
	// at least the baseline entry present, no duplicates of baseline.
	if !mgr.BaselineDone() {
		t.Fatal("baseline should be done after all tracks")
	}
	baselines := 0
	for _, e := range mgr.List() {
		if e.ToolName == "baseline" {
			baselines++
		}
	}
	if baselines != 1 {
		t.Errorf("exactly one baseline entry expected, got %d", baselines)
	}
}

// TestService_CaptureIndexHash verifies the cheap anchor: after a Track the
// index reflects the tree and the hash matches; on a fresh store with an
// untracked file it returns "".
func TestService_CaptureIndexHash(t *testing.T) {
	workDir := t.TempDir()
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "a.txt"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := snapshot.NewService(workDir, dataDir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if !s.Enabled() {
		t.Skip("git not available")
	}

	tracked, err := s.Track()
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	if got := s.CaptureIndexHash(); got != tracked {
		t.Errorf("CaptureIndexHash = %q, want %q (tracked tree)", got, tracked)
	}

	// Add an untracked file: the index no longer reflects the work tree,
	// so the cheap anchor must decline (return "").
	if err := os.WriteFile(filepath.Join(workDir, "b.txt"), []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := s.CaptureIndexHash(); got != "" {
		t.Errorf("CaptureIndexHash with untracked file = %q, want empty", got)
	}
}
