package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/snapshot"
)

// newTestSnapshotManagerWithDir creates a SnapshotManager and returns it along
// with the working directory, so tests can modify files.
func newTestSnapshotManagerWithDir(t *testing.T) (*SnapshotManager, string) {
	t.Helper()
	workDir := t.TempDir()
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	svc, err := snapshot.NewService(workDir, dataDir)
	if err != nil {
		t.Fatalf("snapshot.NewService: %v", err)
	}
	if !svc.Enabled() {
		t.Skip("snapshot service not enabled (git unavailable)")
	}
	return NewSnapshotManager(svc), workDir
}

func TestSnapshotManager_Diff_AgainstLastSnapshot(t *testing.T) {
	mgr, workDir := newTestSnapshotManagerWithDir(t)

	if err := mgr.Track("baseline", 0); err != nil {
		t.Fatalf("track: %v", err)
	}

	// Modify the file.
	if err := os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("hello world\nmodified\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	diff, err := mgr.Diff(-1)
	if err != nil {
		t.Fatalf("Diff(-1): %v", err)
	}
	if diff == "" {
		t.Error("expected non-empty diff after file modification")
	}
	if !strings.Contains(diff, "modified") {
		t.Errorf("diff should contain 'modified', got:\n%s", diff)
	}
}

func TestSnapshotManager_Diff_NoSnapshots(t *testing.T) {
	mgr, _ := newTestSnapshotManagerWithDir(t)
	_, err := mgr.Diff(-1)
	if err == nil {
		t.Error("expected error when diffing with no snapshots")
	}
}

func TestSnapshotManager_Diff_InvalidIndex(t *testing.T) {
	mgr, _ := newTestSnapshotManagerWithDir(t)
	if err := mgr.Track("baseline", 0); err != nil {
		t.Fatalf("track: %v", err)
	}
	_, err := mgr.Diff(99)
	if err == nil {
		t.Error("expected error for invalid snapshot index")
	}
}

func TestSnapshotManager_DiffBetween(t *testing.T) {
	mgr, workDir := newTestSnapshotManagerWithDir(t)

	// Snapshot 0: initial state.
	if err := mgr.Track("baseline", 0); err != nil {
		t.Fatalf("track 0: %v", err)
	}

	// Modify file and snapshot 1.
	if err := os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("version 2\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := mgr.Track("edit", 1); err != nil {
		t.Fatalf("track 1: %v", err)
	}

	// Modify file again and snapshot 2.
	if err := os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("version 3\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := mgr.Track("edit", 2); err != nil {
		t.Fatalf("track 2: %v", err)
	}

	// Diff between snapshot 0 and 2.
	diff, err := mgr.DiffBetween(0, 2)
	if err != nil {
		t.Fatalf("DiffBetween(0, 2): %v", err)
	}
	if diff == "" {
		t.Error("expected non-empty diff between snapshots 0 and 2")
	}
	if !strings.Contains(diff, "version 3") {
		t.Errorf("diff should contain 'version 3', got:\n%s", diff)
	}
}

func TestSnapshotManager_DiffBetween_InvalidIndices(t *testing.T) {
	mgr, _ := newTestSnapshotManagerWithDir(t)
	if err := mgr.Track("baseline", 0); err != nil {
		t.Fatalf("track: %v", err)
	}

	_, err := mgr.DiffBetween(99, 0)
	if err == nil {
		t.Error("expected error for invalid from index")
	}

	_, err = mgr.DiffBetween(0, 99)
	if err == nil {
		t.Error("expected error for invalid to index")
	}
}

func TestSnapshotManager_DiffBetween_NoSnapshots(t *testing.T) {
	mgr, _ := newTestSnapshotManagerWithDir(t)
	_, err := mgr.DiffBetween(0, 1)
	if err == nil {
		t.Error("expected error when diffing with no snapshots")
	}
}
