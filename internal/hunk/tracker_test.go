package hunk

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTracker_RecordAgentEdit(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	// Create a file first
	file := filepath.Join(dir, "test.go")
	os.WriteFile(file, []byte("package main\n"), 0o644)

	// Record initial state via CheckExternalChanges (discovers existing files)
	_, err := tracker.CheckExternalChanges()
	if err != nil {
		t.Fatalf("CheckExternalChanges: %v", err)
	}

	// Simulate agent edit
	os.WriteFile(file, []byte("package main\nfunc main() {}\n"), 0o644)
	if err := tracker.RecordAgentEdit(file, "write_file", "call-1"); err != nil {
		t.Fatalf("RecordAgentEdit: %v", err)
	}

	hunks := tracker.GetHunks(SourceAgent)
	if len(hunks) != 1 {
		t.Fatalf("expected 1 agent hunk, got %d", len(hunks))
	}
	if hunks[0].ToolName != "write_file" {
		t.Errorf("expected tool 'write_file', got %s", hunks[0].ToolName)
	}
}

func TestTracker_DetectExternalChanges(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	// Initial scan — empty workspace
	changes, err := tracker.CheckExternalChanges()
	if err != nil {
		t.Fatalf("initial scan: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("expected 0 changes in empty workspace, got %d", len(changes))
	}

	// Create a file externally
	file := filepath.Join(dir, "main.go")
	os.WriteFile(file, []byte("package main\n"), 0o644)
	time.Sleep(10 * time.Millisecond)

	changes, err = tracker.CheckExternalChanges()
	if err != nil {
		t.Fatalf("scan after external change: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 external change, got %d", len(changes))
	}
	if changes[0].Source != SourceExternal {
		t.Errorf("expected external source, got %s", changes[0].Source)
	}
}

func TestTracker_DetectExternalModification(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	file := filepath.Join(dir, "test.go")
	os.WriteFile(file, []byte("hello\n"), 0o644)
	tracker.CheckExternalChanges()

	// Modify externally
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(file, []byte("hello world\nmodified\n"), 0o644)

	changes, err := tracker.CheckExternalChanges()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Source != SourceExternal {
		t.Errorf("expected external, got %s", changes[0].Source)
	}
}

func TestTracker_ConflictDetection(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	file := filepath.Join(dir, "shared.go")
	os.WriteFile(file, []byte("package main\n"), 0o644)
	tracker.CheckExternalChanges()

	// Agent edits
	os.WriteFile(file, []byte("package main\n// agent edit\n"), 0o644)
	tracker.RecordAgentEdit(file, "write_file", "call-1")

	// External modification
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(file, []byte("package main\n// user edit\n"), 0o644)
	tracker.CheckExternalChanges()

	if !tracker.HasConflict(file) {
		t.Error("expected conflict detected")
	}

	conflicts := tracker.GetConflicts()
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
}

func TestTracker_FileHistory(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	file := filepath.Join(dir, "history.go")
	os.WriteFile(file, []byte("v1\n"), 0o644)
	tracker.CheckExternalChanges()

	os.WriteFile(file, []byte("v2\n"), 0o644)
	tracker.RecordAgentEdit(file, "write_file", "call-1")

	os.WriteFile(file, []byte("v3\n"), 0o644)
	time.Sleep(10 * time.Millisecond)
	tracker.CheckExternalChanges()

	history := tracker.GetFileHistory(file)
	if len(history) < 2 {
		t.Fatalf("expected at least 2 history entries, got %d", len(history))
	}
}

func TestTracker_GetHunksBySource(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	// External file
	extFile := filepath.Join(dir, "ext.go")
	os.WriteFile(extFile, []byte("external\n"), 0o644)
	tracker.CheckExternalChanges()

	// Agent file
	agentFile := filepath.Join(dir, "agent.go")
	os.WriteFile(agentFile, []byte("agent\n"), 0o644)
	tracker.RecordAgentEdit(agentFile, "write_file", "call-1")

	agentHunks := tracker.GetHunks(SourceAgent)
	if len(agentHunks) != 1 {
		t.Fatalf("expected 1 agent hunk, got %d", len(agentHunks))
	}

	extHunks := tracker.GetHunks(SourceExternal)
	if len(extHunks) < 1 {
		t.Fatalf("expected at least 1 external hunk, got %d", len(extHunks))
	}

	allHunks := tracker.GetHunks(SourceUnknown)
	if len(allHunks) < 2 {
		t.Fatalf("expected at least 2 total hunks, got %d", len(allHunks))
	}
}

func TestTracker_SkipGitDir(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	// Create .git directory with files
	gitDir := filepath.Join(dir, ".git", "objects")
	os.MkdirAll(gitDir, 0o755)
	os.WriteFile(filepath.Join(gitDir, "abc123"), []byte("git object"), 0o644)

	changes, err := tracker.CheckExternalChanges()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, c := range changes {
		if filepath.Base(filepath.Dir(c.FilePath)) == ".git" {
			t.Errorf("should not track .git directory: %s", c.FilePath)
		}
	}
}

func TestTracker_Reset(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	file := filepath.Join(dir, "test.go")
	os.WriteFile(file, []byte("test\n"), 0o644)
	tracker.CheckExternalChanges()

	tracker.Reset()
	if len(tracker.GetHunks(SourceUnknown)) != 0 {
		t.Error("expected empty hunks after reset")
	}
}

func TestTracker_MaxHunks(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)
	tracker.SetMaxHunks(5)

	// Create more than maxHunks files
	for i := 0; i < 10; i++ {
		file := filepath.Join(dir, "file_"+string(rune('a'+i))+".go")
		os.WriteFile(file, []byte("test\n"), 0o644)
		time.Sleep(1 * time.Millisecond)
	}
	tracker.CheckExternalChanges()

	allHunks := tracker.GetHunks(SourceUnknown)
	if len(allHunks) > 5 {
		t.Errorf("expected at most 5 hunks, got %d", len(allHunks))
	}
}

func TestSource_String(t *testing.T) {
	if SourceAgent.String() != "agent" {
		t.Error("bad string")
	}
	if SourceExternal.String() != "external" {
		t.Error("bad string")
	}
	if SourceUnknown.String() != "unknown" {
		t.Error("bad string")
	}
}

func TestEstimateLineDelta(t *testing.T) {
	added, deleted := estimateLineDelta(100, 200)
	if added == 0 || deleted != 0 {
		t.Errorf("expected growth: added=%d, deleted=%d", added, deleted)
	}

	added, deleted = estimateLineDelta(200, 100)
	if added != 0 || deleted == 0 {
		t.Errorf("expected shrinkage: added=%d, deleted=%d", added, deleted)
	}

	added, deleted = estimateLineDelta(100, 100)
	if added != 0 || deleted != 0 {
		t.Errorf("expected no change: added=%d, deleted=%d", added, deleted)
	}
}
