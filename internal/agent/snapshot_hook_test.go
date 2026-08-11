package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/covoyage/covo-agent/internal/snapshot"
	"github.com/covoyage/covonaut/agentcore"
)

func TestShouldSnapshot_FileMutatingTools(t *testing.T) {
	fileTools := []string{"write_file", "edit_block", "edit", "move", "patch", "apply_patch", "append_file", "bash"}
	for _, tool := range fileTools {
		if !ShouldSnapshot(tool) {
			t.Errorf("ShouldSnapshot(%q) = false, want true", tool)
		}
	}
}

func TestShouldSnapshot_NonFileMutatingTools(t *testing.T) {
	readOnlyTools := []string{"read", "grep", "glob", "ls", "list", "search", "fetch", "web_search", ""}
	for _, tool := range readOnlyTools {
		if ShouldSnapshot(tool) {
			t.Errorf("ShouldSnapshot(%q) = true, want false", tool)
		}
	}
}

// newTestSnapshotManager creates a SnapshotManager backed by a real snapshot
// service operating in a temp directory.
func newTestSnapshotManager(t *testing.T) *SnapshotManager {
	t.Helper()
	workDir := t.TempDir()
	dataDir := t.TempDir()
	// Seed an initial file so the working tree is non-empty.
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
	return NewSnapshotManager(svc)
}

func TestSnapshotAfterHook_TracksFileMutatingTool(t *testing.T) {
	mgr := newTestSnapshotManager(t)
	ca := &CovoAgent{snapshotMgr: mgr}

	// Initial snapshot — captures the seeded file.
	if err := mgr.Track("setup", 0); err != nil {
		t.Fatalf("initial track: %v", err)
	}

	hook := ca.snapshotAfterHook()
	hc := &agentcore.HookContext{
		ToolName:  "write_file",
		Arguments: json.RawMessage(`{}`),
	}

	// The hook should not panic and should leave the manager in a trackable state.
	hook(context.Background(), hc, "wrote file", nil)

	entries := mgr.List()
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries after hook (initial + post-hook), got %d", len(entries))
	}
	// The last entry should be attributed to write_file.
	last := entries[len(entries)-1]
	if last.ToolName != "write_file" {
		t.Errorf("last entry tool = %q, want %q", last.ToolName, "write_file")
	}
}

func TestSnapshotAfterHook_IgnoresReadOnlyTool(t *testing.T) {
	mgr := newTestSnapshotManager(t)
	ca := &CovoAgent{snapshotMgr: mgr}

	if err := mgr.Track("setup", 0); err != nil {
		t.Fatalf("initial track: %v", err)
	}
	before := len(mgr.List())

	hook := ca.snapshotAfterHook()
	hc := &agentcore.HookContext{
		ToolName:  "read", // read-only tool — should NOT trigger snapshot
		Arguments: json.RawMessage(`{}`),
	}
	hook(context.Background(), hc, "file contents", nil)

	after := len(mgr.List())
	if after != before {
		t.Errorf("read-only tool should not create snapshot: entries before=%d after=%d", before, after)
	}
}

func TestSnapshotAfterHook_NoOpWhenDisabled(t *testing.T) {
	// Manager with nil service → disabled.
	ca := &CovoAgent{snapshotMgr: NewSnapshotManager(nil)}

	hook := ca.snapshotAfterHook()
	hc := &agentcore.HookContext{
		ToolName:  "write_file",
		Arguments: json.RawMessage(`{}`),
	}
	// Should be a no-op — no panic, no error (hook signature doesn't return error).
	hook(context.Background(), hc, "result", nil)

	if entries := ca.snapshotMgr.List(); len(entries) != 0 {
		t.Errorf("disabled manager should have 0 entries, got %d", len(entries))
	}
}

func TestSnapshotAfterHook_NoOpWhenManagerNil(t *testing.T) {
	ca := &CovoAgent{snapshotMgr: nil}

	hook := ca.snapshotAfterHook()
	hc := &agentcore.HookContext{
		ToolName:  "write_file",
		Arguments: json.RawMessage(`{}`),
	}
	// Must not panic even though snapshotMgr is nil.
	hook(context.Background(), hc, "result", nil)
}

func TestSnapshotManager_FindClosest(t *testing.T) {
	mgr := newTestSnapshotManager(t)

	// Track three snapshots at different message indices.
	if err := mgr.Track("baseline", 0); err != nil {
		t.Fatalf("track baseline: %v", err)
	}
	if err := mgr.Track("write_file", 5); err != nil {
		t.Fatalf("track write_file: %v", err)
	}
	if err := mgr.Track("edit_block", 10); err != nil {
		t.Fatalf("track edit_block: %v", err)
	}

	// Exact match — target 10 should return entry with msgIdx 10.
	e, ok := mgr.FindClosest(10)
	if !ok {
		t.Fatal("FindClosest(10) returned false")
	}
	if e.MessageIndex != 10 {
		t.Errorf("FindClosest(10) msgIdx = %d, want 10", e.MessageIndex)
	}

	// Between two entries — target 7 should return entry with msgIdx 5.
	e, ok = mgr.FindClosest(7)
	if !ok {
		t.Fatal("FindClosest(7) returned false")
	}
	if e.MessageIndex != 5 {
		t.Errorf("FindClosest(7) msgIdx = %d, want 5", e.MessageIndex)
	}

	// Before all entries — target 0 should return the first entry (msgIdx 0).
	e, ok = mgr.FindClosest(0)
	if !ok {
		t.Fatal("FindClosest(0) returned false")
	}
	if e.MessageIndex != 0 {
		t.Errorf("FindClosest(0) msgIdx = %d, want 0", e.MessageIndex)
	}

	// After all entries — target 100 should return the last entry (msgIdx 10).
	e, ok = mgr.FindClosest(100)
	if !ok {
		t.Fatal("FindClosest(100) returned false")
	}
	if e.MessageIndex != 10 {
		t.Errorf("FindClosest(100) msgIdx = %d, want 10", e.MessageIndex)
	}
}

func TestSnapshotManager_FindClosest_Empty(t *testing.T) {
	mgr := NewSnapshotManager(nil)
	if _, ok := mgr.FindClosest(10); ok {
		t.Error("FindClosest on empty manager should return false")
	}
}

func TestSnapshotManager_Get_OutOfRange(t *testing.T) {
	mgr := newTestSnapshotManager(t)
	if _, ok := mgr.Get(0); ok {
		t.Error("Get(0) on empty manager should return false")
	}
}

func TestSnapshotManager_PersistAndLoad(t *testing.T) {
	dir := t.TempDir()

	// Create and populate a manager with persistence.
	mgr1 := newTestSnapshotManager(t)
	mgr1.SetStoreDir(dir)
	if err := mgr1.Track("baseline", 0); err != nil {
		t.Fatalf("track: %v", err)
	}
	if err := mgr1.Track("write_file", 5); err != nil {
		t.Fatalf("track: %v", err)
	}

	// Verify persistence file exists.
	path := filepath.Join(dir, "snapshots.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("persistence file not created: %v", err)
	}

	// Create a new manager with the same store dir — should load entries.
	mgr2 := newTestSnapshotManager(t)
	mgr2.SetStoreDir(dir)
	entries := mgr2.List()
	if len(entries) != 2 {
		t.Fatalf("after load: expected 2 entries, got %d", len(entries))
	}
	if entries[0].ToolName != "baseline" {
		t.Errorf("entries[0].ToolName = %q, want baseline", entries[0].ToolName)
	}
	if entries[1].MessageIndex != 5 {
		t.Errorf("entries[1].MessageIndex = %d, want 5", entries[1].MessageIndex)
	}
}

func TestSnapshotManager_AutoPrune(t *testing.T) {
	mgr := newTestSnapshotManager(t)
	dir := t.TempDir()
	mgr.SetStoreDir(dir)

	// Record more than maxSnapshotEntries.
	for i := 0; i < maxSnapshotEntries+10; i++ {
		if err := mgr.Track("tool", i); err != nil {
			t.Fatalf("track %d: %v", i, err)
		}
	}

	entries := mgr.List()
	if len(entries) > maxSnapshotEntries {
		t.Errorf("expected at most %d entries after prune, got %d", maxSnapshotEntries, len(entries))
	}
}
