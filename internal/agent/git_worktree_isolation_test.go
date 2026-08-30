package agent

import (
	"os"
	"os/exec"
	"testing"
)

func TestNewIsolatedGitWorktreeIgnoresEnv(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	t.Setenv("COVO_WORKTREE", "")

	if NewIsolatedGitWorktree(dir).Enabled() {
		t.Fatal("non-git directory should not enable isolation")
	}
	if NewGitWorktree(dir).Enabled() {
		t.Fatal("non-git directory should not enable COVO_WORKTREE worktree")
	}

	runGit(t, dir, "init")
	t.Setenv("COVO_WORKTREE", "")
	if !NewIsolatedGitWorktree(dir).Enabled() {
		t.Fatal("git repo should enable isolation regardless of COVO_WORKTREE")
	}
	if NewGitWorktree(dir).Enabled() {
		t.Fatal("NewGitWorktree should stay off when COVO_WORKTREE is unset")
	}

	t.Setenv("COVO_WORKTREE", "true")
	if !NewGitWorktree(dir).Enabled() {
		t.Fatal("NewGitWorktree should enable when COVO_WORKTREE=true in a git repo")
	}
}

func TestGitWorktreeBaseDir(t *testing.T) {
	if got := (*GitWorktree)(nil).BaseDir(); got != "" {
		t.Fatalf("nil BaseDir = %q", got)
	}
	gw := NewIsolatedGitWorktree("/tmp/example")
	if gw.BaseDir() != "/tmp/example" {
		t.Fatalf("BaseDir = %q", gw.BaseDir())
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}
