package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/snapshot"
)

// TestSnapshotManager_TrackFastPath verifies that Track skips the full
// staging walk when the work tree is unchanged, and records a new entry as
// soon as anything changes (content, new file, or deletion).
func TestSnapshotManager_TrackFastPath(t *testing.T) {
	mgr, workDir := newTestSnapshotManager(t)

	if err := mgr.Track("baseline", 0); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if got := len(mgr.List()); got != 1 {
		t.Fatalf("after baseline: want 1 entry, got %d", got)
	}

	// No change → fast path must skip; no new entry.
	if err := mgr.Track("bash", 1); err != nil {
		t.Fatalf("unchanged track: %v", err)
	}
	if got := len(mgr.List()); got != 1 {
		t.Fatalf("unchanged work tree must not add entries: got %d", got)
	}

	// Modified tracked file → new entry.
	if err := os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Track("bash", 2); err != nil {
		t.Fatalf("modified track: %v", err)
	}
	if got := len(mgr.List()); got != 2 {
		t.Fatalf("modified work tree must add an entry: got %d", got)
	}

	// New untracked file → new entry.
	if err := os.WriteFile(filepath.Join(workDir, "new.txt"), []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Track("bash", 3); err != nil {
		t.Fatalf("untracked track: %v", err)
	}
	if got := len(mgr.List()); got != 3 {
		t.Fatalf("new untracked file must add an entry: got %d", got)
	}

	// Deleted tracked file → new entry.
	if err := os.Remove(filepath.Join(workDir, "new.txt")); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Track("bash", 4); err != nil {
		t.Fatalf("delete track: %v", err)
	}
	if got := len(mgr.List()); got != 4 {
		t.Fatalf("deletion must add an entry: got %d", got)
	}
}

// TestSnapshotManager_LoadPrunesStaleEntries simulates the aftermath of a
// lost object store: persisted entries reference trees that no longer exist.
// Loading must drop them and rewrite snapshots.json.
func TestSnapshotManager_LoadPrunesStaleEntries(t *testing.T) {
	workDir := t.TempDir()
	dataDir := t.TempDir()
	storeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	svc1, err := snapshot.NewService(workDir, dataDir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if !svc1.Enabled() {
		t.Skip("git not available")
	}
	mgr1 := NewSnapshotManager(svc1)
	mgr1.SetStoreDir(storeDir)
	if err := mgr1.Track("baseline", 0); err != nil {
		t.Fatalf("track: %v", err)
	}
	if got := len(mgr1.List()); got != 1 {
		t.Fatalf("want 1 entry, got %d", got)
	}

	// Wipe the object store to simulate a lost store.
	if err := os.RemoveAll(filepath.Join(dataDir, "snapshot")); err != nil {
		t.Fatal(err)
	}

	// A fresh service over the same (now-empty) store + same store dir must
	// prune the stale entries on load.
	svc2, err := snapshot.NewService(workDir, dataDir)
	if err != nil {
		t.Fatalf("NewService 2: %v", err)
	}
	mgr2 := NewSnapshotManager(svc2)
	mgr2.SetStoreDir(storeDir)
	if got := len(mgr2.List()); got != 0 {
		t.Fatalf("stale entries must be pruned on load, got %d", got)
	}
	// And snapshots.json must have been rewritten without them.
	mgr3 := NewSnapshotManager(svc2)
	mgr3.SetStoreDir(storeDir)
	if got := len(mgr3.List()); got != 0 {
		t.Fatalf("rewritten snapshots.json must be empty, got %d entries", got)
	}
}

// TestSnapshotManager_TrackAfterLoad uses an entry list loaded from disk with
// the live store intact: the fast path must still detect real changes.
func TestSnapshotManager_TrackAfterLoad(t *testing.T) {
	workDir := t.TempDir()
	dataDir := t.TempDir()
	storeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	svc, err := snapshot.NewService(workDir, dataDir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if !svc.Enabled() {
		t.Skip("git not available")
	}

	mgr1 := NewSnapshotManager(svc)
	mgr1.SetStoreDir(storeDir)
	if err := mgr1.Track("baseline", 0); err != nil {
		t.Fatalf("track: %v", err)
	}

	// Simulate a process restart: fresh manager, same service data.
	svc2, err := snapshot.NewService(workDir, dataDir)
	if err != nil {
		t.Fatalf("NewService 2: %v", err)
	}
	mgr2 := NewSnapshotManager(svc2)
	mgr2.SetStoreDir(storeDir)
	if got := len(mgr2.List()); got != 1 {
		t.Fatalf("want 1 loaded entry, got %d", got)
	}

	// Unchanged → no new entry (fast path against loaded hash).
	if err := mgr2.Track("bash", 1); err != nil {
		t.Fatalf("unchanged track: %v", err)
	}
	if got := len(mgr2.List()); got != 1 {
		t.Fatalf("unchanged: want 1 entry, got %d", got)
	}

	// Change → new entry. Ensure the file's mtime moves so the stat-based
	// dirty check reliably sees it even on coarse-grained filesystems
	// (FAT has 2s resolution; some Linux filesystems cache mtime in ns but
	// same-tick writes can compare equal).
	future := time.Now().Add(3 * time.Second)
	if err := os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("v2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(workDir, "hello.txt"), future, future); err != nil {
		t.Fatal(err)
	}
	if err := mgr2.Track("bash", 2); err != nil {
		t.Fatalf("changed track: %v", err)
	}
	if got := len(mgr2.List()); got != 2 {
		t.Fatalf("changed: want 2 entries, got %d", got)
	}
}
