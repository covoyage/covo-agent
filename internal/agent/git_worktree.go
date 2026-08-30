package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	fastworktree "github.com/covoyage/covo-agent/internal/worktree"
)

// GitWorktree manages a temporary git worktree for agent operations.
// When enabled, the agent operates in an isolated worktree instead of
// directly on the user's working directory. On cleanup, the worktree
// is removed unless there are unpushed commits.
type GitWorktree struct {
	mu           sync.Mutex
	worktreePath string
	branch       string
	baseDir      string
	enabled      bool
	created      bool
}

// NewGitWorktree creates a new GitWorktree manager.
// baseDir is the original working directory.
func NewGitWorktree(baseDir string) *GitWorktree {
	return &GitWorktree{
		baseDir: baseDir,
		enabled: os.Getenv("COVO_WORKTREE") == "true" && isGitRepo(baseDir),
	}
}

// NewIsolatedGitWorktree creates a worktree isolator that is enabled whenever
// baseDir is a git repository, independent of COVO_WORKTREE.
func NewIsolatedGitWorktree(baseDir string) *GitWorktree {
	return &GitWorktree{
		baseDir: baseDir,
		enabled: isGitRepo(baseDir),
	}
}

// BaseDir returns the original working directory the worktree was created from.
func (gw *GitWorktree) BaseDir() string {
	if gw == nil {
		return ""
	}
	return gw.baseDir
}

// isGitRepo reports whether dir is inside a git working tree.
func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// Enabled returns whether worktree mode is active.
func (gw *GitWorktree) Enabled() bool {
	return gw.enabled
}

// Ensure creates the worktree if not already created and returns the
// working directory (worktree path if enabled, baseDir otherwise).
func (gw *GitWorktree) Ensure() (string, error) {
	if !gw.enabled {
		return gw.baseDir, nil
	}

	gw.mu.Lock()
	defer gw.mu.Unlock()

	if gw.created {
		return gw.worktreePath, nil
	}

	branch, err := currentBranch(gw.baseDir)
	if err != nil {
		gw.enabled = false
		return gw.baseDir, nil
	}
	gw.branch = branch

	suffix := fmt.Sprintf("covo-worktree-%d", time.Now().UnixMilli())
	worktreePath := filepath.Join(os.TempDir(), suffix)

	if os.Getenv("COVO_WORKTREE_COW") == "true" {
		options := fastworktree.DefaultOptions()
		options.Detach = true
		options.StartPoint = branch
		if err := fastworktree.New(gw.baseDir).Create(worktreePath, options); err != nil {
			gw.enabled = false
			return gw.baseDir, fmt.Errorf("fast git worktree add: %w", err)
		}
	} else {
		cmd := exec.Command("git", "worktree", "add", "--detach", worktreePath, branch)
		cmd.Dir = gw.baseDir
		cmd.Stderr = os.Stderr
		if out, err := cmd.Output(); err != nil {
			gw.enabled = false
			return gw.baseDir, fmt.Errorf("git worktree add: %w\n%s", err, string(out))
		}
	}

	gw.worktreePath = worktreePath
	gw.created = true
	return worktreePath, nil
}

// Cleanup removes the worktree. If there are unpushed commits, the worktree
// is preserved and its path is returned.
func (gw *GitWorktree) Cleanup() (preserved string, err error) {
	gw.mu.Lock()
	defer gw.mu.Unlock()

	if !gw.created || gw.worktreePath == "" {
		return "", nil
	}

	// Check for unpushed commits
	hasUnpushed, _ := gw.hasUnpushedCommits()
	if hasUnpushed {
		gw.created = false
		return gw.worktreePath, nil
	}

	cmd := exec.Command("git", "worktree", "remove", "--force", gw.worktreePath)
	cmd.Dir = gw.baseDir
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git worktree remove: %w", err)
	}

	// Also remove the temp directory if it still exists
	os.RemoveAll(gw.worktreePath)

	gw.created = false
	return "", nil
}

// WorktreePath returns the worktree path, or empty if not created.
func (gw *GitWorktree) WorktreePath() string {
	gw.mu.Lock()
	defer gw.mu.Unlock()
	return gw.worktreePath
}

func (gw *GitWorktree) hasUnpushedCommits() (bool, error) {
	cmd := exec.Command("git", "log", "--oneline", fmt.Sprintf("@'{u}..@'"))
	cmd.Dir = gw.worktreePath
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		return false, nil
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

func currentBranch(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("get current branch: %w", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch == "HEAD" {
		return "", fmt.Errorf("detached HEAD, cannot create worktree")
	}
	return branch, nil
}
