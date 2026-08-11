package worktree

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// WorktreeManager manages git worktrees for isolated agent workspaces.
type WorktreeManager struct {
	mu          sync.RWMutex
	homeDir     string
	originalCWD string
	active      string // current worktree path, empty = main repo
	worktrees   map[string]worktreeEntry
}

type worktreeEntry struct {
	Path      string    `json:"path"`
	Branch    string    `json:"branch"`
	CreatedAt time.Time `json:"created_at"`
}

func NewWorktreeManager(homeDir string) *WorktreeManager {
	cwd, _ := os.Getwd()
	wm := &WorktreeManager{
		homeDir:     homeDir,
		originalCWD: cwd,
		worktrees:   make(map[string]worktreeEntry),
	}
	wm.load()
	return wm
}

func (wm *WorktreeManager) OriginalCWD() string {
	return wm.originalCWD
}

func (wm *WorktreeManager) Active() string {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	if wm.active == "" {
		return wm.originalCWD
	}
	return wm.active
}

// CreateWorktree creates a new git worktree for the given branch.
func (wm *WorktreeManager) CreateWorktree(branch string) (string, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if wm.active != "" {
		return "", fmt.Errorf("already in worktree %s, exit it first", wm.active)
	}

	repoRoot, err := findGitRoot(wm.originalCWD)
	if err != nil {
		return "", fmt.Errorf("not in a git repo: %w", err)
	}

	// Validate branch exists
	checkCmd := exec.Command("git", "branch", "--list", branch)
	checkOut, err := checkCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git branch check: %w", err)
	}
	branchExists := strings.Contains(string(checkOut), branch)

	// Generate unique worktree path
	wtName := fmt.Sprintf("covo-%s", sanitizeBranchName(branch))
	wtPath := filepath.Join(wm.homeDir, "worktrees", wtName)

	// Remove stale worktree if exists
	if _, err := os.Stat(wtPath); err == nil {
		os.RemoveAll(wtPath)
		exec.Command("git", "worktree", "prune").Run()
	}

	// Create the worktree
	args := []string{"worktree", "add", wtPath}
	if branchExists {
		args = append(args, branch)
	} else {
		args = append(args, "-b", branch)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git worktree add: %w\n%s", err, string(output))
	}

	wm.active = wtPath
	wm.worktrees[branch] = worktreeEntry{
		Path:      wtPath,
		Branch:    branch,
		CreatedAt: time.Now(),
	}
	wm.save()

	// Change working directory to worktree
	os.Chdir(wtPath)

	return wtPath, nil
}

// RemoveWorktree removes a worktree, cleans up the tracked entry, and returns to original CWD.
func (wm *WorktreeManager) RemoveWorktree() error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if wm.active == "" {
		return fmt.Errorf("not in a worktree")
	}

	wtPath := wm.active

	// Remove via git
	cmd := exec.Command("git", "worktree", "remove", wtPath, "--force")
	cmd.Dir = wm.originalCWD
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove: %w\n%s", err, string(output))
	}

	// Remove from tracked entries
	for branch, wt := range wm.worktrees {
		if wt.Path == wtPath {
			delete(wm.worktrees, branch)
			break
		}
	}
	wm.save()

	// Return to original CWD
	os.Chdir(wm.originalCWD)
	wm.active = ""

	return nil
}

// PruneStale scans tracked worktrees and removes entries whose directories no
// longer exist or whose git worktree records are stale. Returns the pruned branches.
func (wm *WorktreeManager) PruneStale() []string {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	// Run git worktree prune first to let git clean its own records
	exec.Command("git", "worktree", "prune").Run()

	// Cache git worktree list output for efficient checking
	listOutput, _ := exec.Command("git", "worktree", "list").Output()
	worktreeList := string(listOutput)

	var pruned []string
	for branch, wt := range wm.worktrees {
		if _, err := os.Stat(wt.Path); os.IsNotExist(err) {
			delete(wm.worktrees, branch)
			pruned = append(pruned, branch)
			continue
		}
		if !strings.Contains(worktreeList, wt.Path) {
			delete(wm.worktrees, branch)
			pruned = append(pruned, branch)
		}
	}
	if len(pruned) > 0 {
		wm.save()
	}
	return pruned
}

// ListWorktrees lists active worktrees.
func (wm *WorktreeManager) ListWorktrees() []map[string]any {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	var result []map[string]any
	for _, wt := range wm.worktrees {
		active := wt.Path == wm.active
		result = append(result, map[string]any{
			"branch":     wt.Branch,
			"path":       wt.Path,
			"created_at": wt.CreatedAt.Format(time.RFC3339),
			"active":     active,
		})
	}
	return result
}

// CleanupAll removes all worktrees.
func (wm *WorktreeManager) CleanupAll() []string {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	var removed []string
	for branch, wt := range wm.worktrees {
		exec.Command("git", "worktree", "remove", wt.Path, "--force").Run()
		removed = append(removed, branch)
	}

	// Prune stale
	exec.Command("git", "worktree", "prune").Run()

	wm.worktrees = make(map[string]worktreeEntry)
	wm.active = ""
	wm.save()
	os.Chdir(wm.originalCWD)

	return removed
}

// --- persistence ---

func (wm *WorktreeManager) load() {
	data, err := os.ReadFile(wm.dbPath())
	if err != nil {
		return
	}
	var entries []worktreeEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return
	}
	for _, wt := range entries {
		wm.worktrees[wt.Branch] = wt
	}
}

func (wm *WorktreeManager) save() {
	entries := make([]worktreeEntry, 0, len(wm.worktrees))
	for _, wt := range wm.worktrees {
		entries = append(entries, wt)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(wm.dbPath()), 0700)
	os.WriteFile(wm.dbPath(), data, 0600)
}

func (wm *WorktreeManager) dbPath() string {
	return filepath.Join(wm.homeDir, "worktrees.json")
}

// --- Helpers ---

func findGitRoot(start string) (string, error) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no .git found")
		}
		dir = parent
	}
}

func sanitizeBranchName(name string) string {
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, strings.ToLower(name))
	return strings.Trim(name, "-")
}

// --- Tools ---

func BuildEnterWorktreeTool(wm *WorktreeManager) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "enter_worktree",
		Description: strings.Join([]string{
			"Create and switch to an isolated git worktree. This gives the agent",
			"a separate working copy for parallel work on a different branch without",
			"affecting the user's working directory.",
			"Use 'exit_worktree' to return to the original working copy.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"branch": map[string]any{
					"type":        "string",
					"description": "Branch name for the worktree. Creates if not existing.",
				},
			},
			"required": []string{"branch"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Branch string `json:"branch"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if params.Branch == "" {
				return nil, fmt.Errorf("branch is required")
			}

			path, err := wm.CreateWorktree(params.Branch)
			if err != nil {
				return nil, err
			}

			return map[string]any{
				"entered":       true,
				"branch":        params.Branch,
				"worktree_path": path,
				"worktrees":     wm.ListWorktrees(),
			}, nil
		},
	}
}

func BuildExitWorktreeTool(wm *WorktreeManager) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "exit_worktree",
		Description: strings.Join([]string{
			"Remove the current worktree and return to the original working copy.",
			"The worktree is deleted and all git references are cleaned up.",
		}, "\n"),
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if err := wm.RemoveWorktree(); err != nil {
				return nil, err
			}

			return map[string]any{
				"exited":      true,
				"returned_to": wm.originalCWD,
				"worktrees":   wm.ListWorktrees(),
			}, nil
		},
	}
}

// BootstrapWorktree creates a worktree and auto-installs dependencies.
func (wm *WorktreeManager) BootstrapWorktree(branch string) (string, error) {
	path, err := wm.CreateWorktree(branch)
	if err != nil {
		return "", err
	}
	if err := autoInstallDeps(path); err != nil {
		_ = err
	}
	if err := runStartCmd(path); err != nil {
		_ = err
	}
	return path, nil
}

func autoInstallDeps(workdir string) error {
	type pkgManager struct {
		file    string
		install string
	}
	managers := []pkgManager{
		{"package.json", "npm install"},
		{"pnpm-lock.yaml", "pnpm install"},
		{"go.mod", "go mod download"},
		{"Cargo.toml", "cargo fetch"},
		{"requirements.txt", "pip install -r requirements.txt"},
		{"Gemfile", "bundle install"},
	}
	for _, m := range managers {
		if _, err := os.Stat(filepath.Join(workdir, m.file)); err == nil {
			cmd := exec.Command("sh", "-c", m.install)
			cmd.Dir = workdir
			return cmd.Run()
		}
	}
	return nil
}

func runStartCmd(workdir string) error {
	scripts := []string{"./start.sh", "./bootstrap.sh", "make setup"}
	for _, s := range scripts {
		cmd := exec.Command("sh", "-c", s)
		cmd.Dir = workdir
		if cmd.Run() == nil {
			return nil
		}
	}
	return nil
}
