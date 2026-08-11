package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadGitBranchAndDirtyCount(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	runGit("init", "-b", "test-branch")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test")
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "file.txt")
	runGit("commit", "-m", "initial")

	if got := readGitBranch(dir); got != "⎇ test-branch" {
		t.Fatalf("clean branch = %q", got)
	}
	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readGitBranch(dir); !strings.HasPrefix(got, "⎇ test-branch (+") {
		t.Fatalf("dirty branch = %q", got)
	}
}

func TestGitBranchTrackerStopIsIdempotent(t *testing.T) {
	tracker := NewGitBranchTracker(t.TempDir())
	tracker.Stop()
	tracker.Stop()
}
