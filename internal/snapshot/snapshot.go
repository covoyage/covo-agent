// Package snapshot provides file-level snapshot and revert capability using a
// content-addressed git tree store in an isolated repository.
//
// Design:
//   - An isolated git repository at <dataDir>/snapshot/<workdirHash>/ stores
//     tree/blob objects (content-addressed, deduplicated). The user's working
//     repo is never touched.
//   - Track() captures the current working tree state as a git tree object
//     (git add --all + git write-tree). Returns the tree hash.
//   - Patch(fromHash) lists files changed since the given snapshot.
//   - Revert(patches) checks out each file from its snapshot hash, achieving
//     file-level granularity rollback.
//   - Restore(snapshot) does a full working-tree restore (for unrevert).
//   - Diff(fromHash) returns a unified diff for display.
//
// This gives covo-agent file-level revert that survives process restarts
// (snapshots are in the isolated git object store, not in-memory).
package snapshot

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Patch describes the files changed in one snapshot step.
type Patch struct {
	Hash  string   `json:"hash"`  // tree hash of this snapshot
	Files []string `json:"files"` // paths changed since the previous snapshot
}

// Entry is a recorded snapshot with metadata.
type Entry struct {
	Hash         string   `json:"hash"`
	ToolName     string   `json:"tool_name,omitempty"`
	Timestamp    int64    `json:"timestamp"`
	Files        []string `json:"files,omitempty"`
	MessageIndex int      `json:"message_index"` // conversation position when this snapshot was taken
}

// Service manages file snapshots in an isolated git repository.
type Service struct {
	mu      sync.Mutex
	gitDir  string // isolated git repo path
	workDir string // user's working directory
	enabled bool
}

// NewService initializes an isolated git repository for tracking snapshots of
// workDir. The git objects are stored at <dataDir>/snapshot/<workdirHash>/.
// Returns a disabled service (no-op) if git is unavailable or init fails.
func NewService(workDir, dataDir string) (*Service, error) {
	workDirAbs, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("snapshot: resolve workdir: %w", err)
	}
	workdirHash := hashPath(workDirAbs)
	gitDir := filepath.Join(dataDir, "snapshot", workdirHash)

	s := &Service{
		gitDir:  gitDir,
		workDir: workDirAbs,
	}

	// Initialize the isolated git repo if not already present.
	if err := s.initRepo(); err != nil {
		// Non-fatal: return a disabled service so callers can still operate.
		s.enabled = false
		return s, nil
	}
	s.enabled = true
	return s, nil
}

// Enabled reports whether the snapshot service is active.
func (s *Service) Enabled() bool { return s.enabled }

// initRepo creates the isolated git repository if it doesn't exist.
// Uses --bare so that gitDir itself is the git directory (HEAD, objects/,
// refs/ live directly under gitDir). Then sets core.bare=false so git allows
// work-tree operations via GIT_WORK_TREE.
func (s *Service) initRepo() error {
	if _, err := os.Stat(filepath.Join(s.gitDir, "HEAD")); err == nil {
		return nil // already initialized
	}
	if err := os.MkdirAll(s.gitDir, 0755); err != nil {
		return fmt.Errorf("create git dir: %w", err)
	}
	cmd := exec.Command("git", "init", "--quiet", "--bare", s.gitDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init: %w: %s", err, string(out))
	}
	// Allow work-tree operations (bare repos reject add/checkout by default).
	cfgCmd := exec.Command("git", "--git-dir", s.gitDir, "config", "core.bare", "false")
	if out, err := cfgCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git config core.bare: %w: %s", err, string(out))
	}
	return nil
}

// git runs a git command with GIT_DIR pointing to the isolated repo and
// GIT_WORK_TREE pointing to the user's working directory.
func (s *Service) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.workDir
	cmd.Env = append(os.Environ(),
		"GIT_DIR="+s.gitDir,
		"GIT_WORK_TREE="+s.workDir,
	)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s", args[0], err, stderr.String())
	}
	return stdout.String(), nil
}

// Track captures the current working tree as a git tree object and returns
// the tree hash. This is the snapshot primitive: call before/after file
// changes to record points you can later revert to.
func (s *Service) Track() (string, error) {
	if !s.enabled {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Stage all working tree files into the isolated repo's index.
	if _, err := s.git("add", "--all"); err != nil {
		return "", fmt.Errorf("track: add: %w", err)
	}
	// Write the tree object (content-addressed, deduplicated).
	out, err := s.git("write-tree")
	if err != nil {
		return "", fmt.Errorf("track: write-tree: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// Patch returns the list of files changed between fromHash and the current
// index state. Call Track() first to refresh the index.
func (s *Service) Patch(fromHash string) (*Patch, error) {
	if !s.enabled || fromHash == "" {
		return &Patch{Files: []string{}}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	out, err := s.git("diff", "--cached", "--name-only", fromHash)
	if err != nil {
		return nil, fmt.Errorf("patch: %w", err)
	}
	files := splitLines(out)
	return &Patch{Hash: fromHash, Files: files}, nil
}

// Revert checks out each file from the given snapshot hash, restoring it to
// the state captured at that snapshot. Files that didn't exist at snapshot
// time are removed. This is the file-level revert primitive.
func (s *Service) Revert(patches []Patch) error {
	if !s.enabled {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range patches {
		for _, file := range p.Files {
			// Check if the file exists in the snapshot tree.
			out, err := s.git("ls-tree", p.Hash, "--", file)
			if err != nil {
				return fmt.Errorf("revert: ls-tree %s %s: %w", p.Hash, file, err)
			}
			if strings.TrimSpace(out) == "" {
				// File didn't exist at snapshot time — remove it.
				absPath := filepath.Join(s.workDir, file)
				_ = os.Remove(absPath)
				continue
			}
			// Restore the file from the snapshot.
			if _, err := s.git("checkout", p.Hash, "--", file); err != nil {
				return fmt.Errorf("revert: checkout %s -- %s: %w", p.Hash, file, err)
			}
		}
	}
	return nil
}

// Restore does a full working-tree restore to the given snapshot hash.
// Used for unrevert: restores the entire working tree to a captured state.
func (s *Service) Restore(snapshot string) error {
	if !s.enabled || snapshot == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.git("read-tree", snapshot); err != nil {
		return fmt.Errorf("restore: read-tree: %w", err)
	}
	if _, err := s.git("checkout-index", "-a", "-f"); err != nil {
		return fmt.Errorf("restore: checkout-index: %w", err)
	}
	return nil
}

// RestoreIndex loads a tree hash into the index without checking out files.
// This is used by DiffBetween to set up the index for diffing without
// modifying the working tree.
func (s *Service) RestoreIndex(hash string) error {
	if !s.enabled || hash == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.git("read-tree", hash); err != nil {
		return fmt.Errorf("restore-index: read-tree: %w", err)
	}
	return nil
}

// Diff returns a unified diff between fromHash and the current index state.
// Call Track() first to refresh the index.
func (s *Service) Diff(fromHash string) (string, error) {
	if !s.enabled || fromHash == "" {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	out, err := s.git("diff", "--cached", fromHash)
	if err != nil {
		return "", fmt.Errorf("diff: %w", err)
	}
	return out, nil
}

// ListFiles returns the files tracked at a given snapshot hash.
func (s *Service) ListFiles(hash string) ([]string, error) {
	if !s.enabled || hash == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	out, err := s.git("ls-tree", "-r", "--name-only", hash)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	return splitLines(out), nil
}

// hashPath creates a stable filesystem-safe hash for a working directory path.
func hashPath(p string) string {
	h := sha1.Sum([]byte(p))
	return hex.EncodeToString(h[:8])
}

// splitLines splits output into trimmed non-empty lines.
func splitLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
