package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNewIsolatedGitWorktreeIgnoresEnv(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	isolateGit(t)
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
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// isolateGit points git at a writable dummy config. Windows cannot use NUL
// (os.DevNull) as GIT_CONFIG_GLOBAL, and GitHub runners often reject temp
// repos as dubious ownership unless safe.directory is set.
func isolateGit(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	cfg := filepath.Join(home, "gitconfig")
	content := "[user]\n\tname = test\n\temail = test@example.com\n[safe]\n\tdirectory = *\n"
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_SYSTEM", cfg)
	t.Setenv("GIT_TEMPLATE_DIR", "")
}
