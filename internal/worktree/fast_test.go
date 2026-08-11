package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFastWorktree_CreateAndRemove(t *testing.T) {
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Create a temp repo
	repoDir := t.TempDir()
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "config", "user.email", "test@test.com")
	runGit(t, repoDir, "config", "user.name", "Test")

	// Add a file
	os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\n"), 0o644)
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "initial")

	// Create worktree
	fw := New(repoDir)
	wtPath := filepath.Join(repoDir, "..", "worktree-test")

	err := fw.Create(wtPath, DefaultOptions())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer os.RemoveAll(wtPath)

	// Verify worktree exists
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Fatal("worktree directory not created")
	}

	// Verify file was copied
	copiedFile := filepath.Join(wtPath, "main.go")
	if _, err := os.Stat(copiedFile); os.IsNotExist(err) {
		t.Fatal("file not copied to worktree")
	}

	// List worktrees
	list, err := fw.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) < 2 {
		t.Errorf("expected at least 2 worktrees, got %d", len(list))
	}

	// Remove worktree
	if err := fw.Remove(wtPath); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

func TestFastWorktree_CreateWithStats(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoDir := t.TempDir()
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "config", "user.email", "test@test.com")
	runGit(t, repoDir, "config", "user.name", "Test")

	// Create several files
	for i := 0; i < 10; i++ {
		os.WriteFile(
			filepath.Join(repoDir, "file_"+string(rune('a'+i))+".go"),
			[]byte("package main\n"),
			0o644,
		)
	}
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "initial")

	fw := New(repoDir)
	wtPath := filepath.Join(repoDir, "..", "worktree-stats")
	defer os.RemoveAll(wtPath)

	stats, err := fw.CreateWithStats(wtPath, DefaultOptions())
	if err != nil {
		t.Fatalf("CreateWithStats: %v", err)
	}

	if stats.Duration <= 0 {
		t.Error("expected positive duration")
	}
	if stats.FilesCopied < 10 {
		t.Errorf("expected at least 10 files, got %d", stats.FilesCopied)
	}
}

func TestFastWorktree_CreateDetachedFromStartPoint(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoDir := t.TempDir()
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "config", "user.email", "test@test.com")
	runGit(t, repoDir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "initial")

	branchOutput, err := exec.Command("git", "-C", repoDir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	options := DefaultOptions()
	options.Detach = true
	options.StartPoint = strings.TrimSpace(string(branchOutput))
	worktreePath := filepath.Join(repoDir, "..", "worktree-detached")
	defer os.RemoveAll(worktreePath)

	fast := New(repoDir)
	if err := fast.Create(worktreePath, options); err != nil {
		t.Fatalf("Create detached: %v", err)
	}
	head, err := exec.Command("git", "-C", worktreePath, "symbolic-ref", "-q", "HEAD").Output()
	if err == nil || len(head) != 0 {
		t.Fatalf("expected detached HEAD, got %q", head)
	}
	if err := fast.Remove(worktreePath); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

func TestSupportsCoW(t *testing.T) {
	// Just verify it doesn't panic
	_ = supportsCoW()
}

func TestCopyFileRegular(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	srcFile := filepath.Join(src, "test.txt")
	os.WriteFile(srcFile, []byte("hello world"), 0o644)

	dstFile := filepath.Join(dst, "test.txt")
	if err := copyFileRegular(srcFile, dstFile, 0o644); err != nil {
		t.Fatalf("copyFileRegular: %v", err)
	}

	data, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(data))
	}
}

func TestCopyDir(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Create nested structure
	os.MkdirAll(filepath.Join(src, "subdir"), 0o755)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(src, "subdir", "b.txt"), []byte("b"), 0o644)

	if err := copyDir(src, dst, 1); err != nil {
		t.Fatalf("copyDir: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dst, "a.txt")); err != nil {
		t.Error("file a.txt not copied")
	}
	if _, err := os.Stat(filepath.Join(dst, "subdir", "b.txt")); err != nil {
		t.Error("file subdir/b.txt not copied")
	}
}

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()
	if !opts.NoCheckout {
		t.Error("expected NoCheckout=true by default")
	}
	if opts.ParallelCopies <= 0 {
		t.Error("expected positive parallel copies")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", args[0], err, string(out))
	}
}
