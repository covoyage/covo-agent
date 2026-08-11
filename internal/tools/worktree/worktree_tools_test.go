package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorktreeManagerPersistence(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, ".covo-agent")

	wm1 := NewWorktreeManager(homeDir)
	if wm1.homeDir != homeDir {
		t.Errorf("homeDir = %q, want %q", wm1.homeDir, homeDir)
	}

	// Track a fake entry by poking the internal map directly
	branch := "test-branch"
	entry := worktreeEntry{
		Path:   filepath.Join(homeDir, "worktrees", "covo-test-branch"),
		Branch: branch,
	}
	wm1.worktrees[branch] = entry
	wm1.save()

	// Reload from new manager
	wm2 := NewWorktreeManager(homeDir)
	wm2.load()
	if len(wm2.worktrees) != 1 {
		t.Fatalf("expected 1 worktree after reload, got %d", len(wm2.worktrees))
	}
	if wm2.worktrees[branch].Path != entry.Path {
		t.Errorf("path = %q, want %q", wm2.worktrees[branch].Path, entry.Path)
	}
}

func TestWorktreePruneStale(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, ".covo-agent")
	wm := NewWorktreeManager(homeDir)

	// Add an entry that points to a nonexistent directory
	wm.worktrees["stale-branch"] = worktreeEntry{
		Path:   filepath.Join(dir, "nonexistent"),
		Branch: "stale-branch",
	}

	pruned := wm.PruneStale()
	if len(pruned) != 1 {
		t.Fatalf("expected 1 pruned, got %d: %v", len(pruned), pruned)
	}
	if pruned[0] != "stale-branch" {
		t.Errorf("expected 'stale-branch', got %q", pruned[0])
	}

	if len(wm.worktrees) != 0 {
		t.Errorf("expected 0 remaining worktrees, got %d", len(wm.worktrees))
	}
}

func TestWorktreeListWorktrees(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, ".covo-agent")
	wm := NewWorktreeManager(homeDir)

	initial := wm.ListWorktrees()
	if len(initial) != 0 {
		t.Errorf("expected empty list initially, got %d", len(initial))
	}

	wm.worktrees["feature-x"] = worktreeEntry{
		Path:   "/tmp/worktree-x",
		Branch: "feature-x",
	}
	list := wm.ListWorktrees()
	if len(list) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(list))
	}
	if list[0]["branch"] != "feature-x" {
		t.Errorf("branch = %q, want 'feature-x'", list[0]["branch"])
	}
	if list[0]["active"] != false {
		t.Errorf("active should be false")
	}
}

func TestWorktreeDbPath(t *testing.T) {
	homeDir := "/tmp/.covo-agent"
	wm := NewWorktreeManager(homeDir)
	expected := filepath.Join(homeDir, "worktrees.json")
	if wm.dbPath() != expected {
		t.Errorf("dbPath = %q, want %q", wm.dbPath(), expected)
	}
}

func TestWorktreeCorruptedJSON(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, ".covo-agent")
	os.MkdirAll(homeDir, 0700)
	os.WriteFile(filepath.Join(homeDir, "worktrees.json"), []byte("{corrupted"), 0600)

	wm := NewWorktreeManager(homeDir)
	if len(wm.worktrees) != 0 {
		t.Errorf("expected empty worktrees after corrupted JSON, got %d", len(wm.worktrees))
	}
}

func TestWorktreeCleanupAll(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, ".covo-agent")
	wm := NewWorktreeManager(homeDir)

	wm.worktrees["branch-a"] = worktreeEntry{
		Path:   filepath.Join(dir, "wt-a"),
		Branch: "branch-a",
	}
	wm.worktrees["branch-b"] = worktreeEntry{
		Path:   filepath.Join(dir, "wt-b"),
		Branch: "branch-b",
	}

	removed := wm.CleanupAll()
	if len(removed) != 2 {
		t.Errorf("expected 2 removed, got %d", len(removed))
	}
	if len(wm.worktrees) != 0 {
		t.Errorf("expected empty worktrees after cleanup, got %d", len(wm.worktrees))
	}
}

func TestWorktreeActive(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, ".covo-agent")
	wm := NewWorktreeManager(homeDir)

	// Without active worktree, should return original CWD
	if wm.Active() != wm.originalCWD {
		t.Errorf("expected original CWD, got %q", wm.Active())
	}

	// With active worktree, should return it
	wm.active = "/tmp/active-wt"
	if wm.Active() != "/tmp/active-wt" {
		t.Errorf("expected active worktree path, got %q", wm.Active())
	}
}
