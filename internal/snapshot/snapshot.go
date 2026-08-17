// Package snapshot provides file-level snapshot and revert capability using a
// content-addressed git tree store in an isolated repository.
//
// Design:
//   - An isolated git repository at <dataDir>/snapshot/<workdirHash>/ stores
//     tree/blob objects (content-addressed, deduplicated). The user's working
//     repo is never touched.
//   - Track() captures the current working tree as a git tree object
//     (git add --all + git write-tree). Returns the tree hash.
//   - Patch(fromHash) lists files changed since the given snapshot.
//   - Revert(patches) checks out each file from its snapshot hash, achieving
//     file-level granularity rollback.
//   - Restore(snapshot) does a full working-tree restore (for unrevert).
//   - Diff(fromHash) returns a unified diff for display.
//
// This gives covo-agent file-level revert that survives process restarts
// (snapshots are in the isolated git object store, not in-memory).
//
// Disk-safety guards:
//   - The object store itself (and the whole data dir) is excluded from
//     snapshots via info/exclude, so a session whose workDir contains the
//     data dir never snapshots its own object store.
//   - Files larger than maxFileBytes (env COVO_SNAPSHOT_MAX_FILE_MB, default
//     64 MiB) are excluded: large binary payloads gain nothing from content
//     addressing and would balloon the store.
//   - GC(retained) prunes objects that no retained snapshot references.
package snapshot

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// defaultMaxFileBytes is the default per-file size limit for snapshot
	// tracking (64 MiB).
	defaultMaxFileBytes = 64 << 20

	// excludeRefreshInterval is how often the dynamic (big-file) portion of
	// the exclude list is recomputed during Track().
	excludeRefreshInterval = 10 * time.Minute

	// excludeFileHeader marks lines managed by covo-agent in info/exclude.
	excludeFileHeader = "# Managed by covo-agent snapshot service. Do not edit."
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
	dataDir string // absolute path of the covo-agent data dir (parent of snapshot stores)
	workDir string // user's working directory
	enabled bool

	// maxFileBytes excludes files larger than this from snapshots.
	// 0 or negative disables the size limit.
	maxFileBytes int64

	// staticExcludes are gitignore-style patterns that never change for the
	// lifetime of the service: the snapshot object store itself (when it
	// lives inside workDir) and the whole data dir. This keeps the store
	// self-contained and bounded when workDir overlaps the data dir.
	staticExcludes []string

	// excludesRefreshedAt caches the last big-file exclude scan.
	excludesRefreshedAt time.Time
	// lastExcludeContent is the last content written to info/exclude; used
	// to detect when the index must be re-staged from scratch.
	lastExcludeContent string
}

// NewService initializes an isolated git repository for tracking snapshots of
// workDir. The git objects are stored at <dataDir>/snapshot/<workdirHash>/.
// Returns a disabled service (no-op) if git is unavailable or init fails.
func NewService(workDir, dataDir string) (*Service, error) {
	workDirAbs, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("snapshot: resolve workdir: %w", err)
	}
	dataDirAbs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("snapshot: resolve datadir: %w", err)
	}
	workdirHash := hashPath(workDirAbs)
	gitDir := filepath.Join(dataDirAbs, "snapshot", workdirHash)

	s := &Service{
		gitDir:       gitDir,
		dataDir:      dataDirAbs,
		workDir:      workDirAbs,
		maxFileBytes: maxFileBytesFromEnv(),
	}

	// Initialize the isolated git repo if not already present.
	if err := s.initRepo(); err != nil {
		// Non-fatal: return a disabled service so callers can still operate.
		s.enabled = false
		return s, nil
	}
	s.enabled = true

	// Configure excludes (self-recursion guard + big files) before any
	// Track() call can stage objects.
	s.mu.Lock()
	s.computeStaticExcludes()
	s.refreshExcludesLocked()
	s.mu.Unlock()
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

// NeedsTracking reports whether the working tree differs from the given
// snapshot tree hash, using cheap stat-based checks that do not hash file
// contents, stage the tree, or write objects:
//
//  1. ls-files --others: any untracked (non-excluded) file means dirty.
//  2. update-index --refresh: nonzero exit means the work tree differs from
//     the index's stat cache, i.e. dirty.
//  3. diff-index --quiet: nonzero exit means the index differs from lastHash.
//
// It returns true on any doubt (disabled service, empty hash, git errors) --
// the safe default is to run the full Track.
func (s *Service) NeedsTracking(lastHash string) bool {
	if !s.enabled || lastHash == "" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Untracked files would be picked up by `git add --all`.
	if out, err := s.git("ls-files", "--others", "--exclude-standard"); err == nil && strings.TrimSpace(out) != "" {
		return true
	}
	// Refresh the stat cache: nonzero exit reports files that differ from
	// the index (changed, or stat-dirty needing content comparison).
	if code := s.runExitCode("update-index", "--refresh"); code != 0 {
		return true
	}
	// Compare index against the last recorded tree.
	if code := s.runExitCode("diff-index", "--quiet", lastHash); code != 0 {
		return true
	}
	return false
}

// CaptureIndexHash returns the current tree hash by reading the index state
// only: it refreshes the stat cache and runs write-tree, without staging the
// working tree (no add). This is a cheap "anchor" (~a few git calls) that
// callers can capture synchronously at startup, before the full baseline
// snapshot runs in the background. It returns "" when the tree cannot be
// determined cheaply (disabled service, dirty tree, or git error) — callers
// should treat "" as "wait for the full snapshot".
//
// It is meaningful right after Service creation or a Track call, when the
// index already reflects the working tree. On a fresh (empty) index with a
// clean tree it returns the hash of the empty tree.
func (s *Service) CaptureIndexHash() string {
	if !s.enabled {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.captureIndexHashLocked()
}

// captureIndexHashLocked implements CaptureIndexHash; callers must hold s.mu.
func (s *Service) captureIndexHashLocked() string {
	// Untracked files would make the index hash incomplete.
	if out, err := s.git("ls-files", "--others", "--exclude-standard"); err == nil && strings.TrimSpace(out) != "" {
		return ""
	}
	// Stat-cache refresh; nonzero exit means tracked files differ from the
	// index, so the index does not reflect the working tree.
	if code := s.runExitCode("update-index", "--refresh"); code != 0 {
		return ""
	}
	out, err := s.git("write-tree")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// emptyTreeHash is git's well-known empty tree object. git treats it as
// always present (cat-file -e succeeds even when no such object was ever
// written), so it must never be used as evidence that an entry is valid.
const emptyTreeHash = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// HasTree reports whether the object store still contains the given tree
// hash. Used to validate persisted snapshot entries on load: entries whose
// objects were pruned or lost (e.g. a deleted store) are not usable for
// undo/revert and should be dropped. The empty tree is excluded: git answers
// "exists" for it unconditionally, which would let empty-tree entries
// survive every cleanup without backing objects.
func (s *Service) HasTree(hash string) bool {
	if !s.enabled || hash == "" || hash == emptyTreeHash {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	code := s.runExitCode("cat-file", "-e", hash+"^{tree}")
	return code == 0
}

// runExitCode runs a git command and returns its exit code (0 on success).
// Like git(), it targets the isolated repo and the user's work tree.
func (s *Service) runExitCode(args ...string) int {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.workDir
	cmd.Env = append(os.Environ(),
		"GIT_DIR="+s.gitDir,
		"GIT_WORK_TREE="+s.workDir,
	)
	// Discard output: these commands are pure predicates for our use.
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return -1 // spawn failure etc.
	}
	return 0
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

	// Keep the exclude list (big files) fresh so `git add --all` respects it.
	s.refreshExcludesLocked()

	// Stage all working tree files into the isolated repo's index.
	// info/exclude (written by refreshExcludesLocked) keeps the object store
	// itself, the data dir, and oversized files out of the snapshot.
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
			// Never touch paths under the snapshot store itself; a stale
			// entry from a legacy (pre-exclude) store could otherwise make
			// revert delete live object files.
			if s.isExcludedPath(file) {
				continue
			}
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
	// Strip excluded paths (object store / data dir) from the restored tree
	// before checkout: legacy trees recorded before the exclude guard may
	// contain snapshot-store paths, and checkout-index would overwrite the
	// live object store with stale object files.
	s.dropExcludedFromIndexLocked()
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

// GC garbage-collects the object store: it points refs at the retained tree
// hashes (so their objects stay reachable) and then prunes everything else.
// Call this when snapshot entries are pruned so that trimmed-away snapshots
// do not keep their objects on disk forever.
func (s *Service) GC(retained []string) error {
	if !s.enabled {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Drop previous refs/snapshots/* refs.
	out, err := s.git("for-each-ref", "--format=%(refname)", "refs/snapshots")
	if err != nil {
		return fmt.Errorf("gc: for-each-ref: %w", err)
	}
	for _, ref := range splitLines(out) {
		if _, err := s.git("update-ref", "-d", ref); err != nil {
			return fmt.Errorf("gc: delete ref %s: %w", ref, err)
		}
	}

	// Pin the retained hashes.
	seen := make(map[string]bool, len(retained))
	for _, h := range retained {
		h = strings.TrimSpace(h)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		if _, err := s.git("update-ref", "refs/snapshots/"+h, h); err != nil {
			return fmt.Errorf("gc: pin %s: %w", h, err)
		}
	}

	// Prune unreachable objects. The index is treated as a reachability root
	// by git gc, so currently-staged state is never collected.
	if _, err := s.git("gc", "--quiet", "--prune=now"); err != nil {
		return fmt.Errorf("gc: %w", err)
	}
	return nil
}

// --- exclude management ---

// computeStaticExcludes populates staticExcludes with the data dir (and thus
// the object store) whenever it lives inside the working directory. It must
// be called with s.mu held.
func (s *Service) computeStaticExcludes() {
	s.staticExcludes = nil
	for _, root := range []string{s.dataDir, s.gitDir} {
		rel, ok := s.relToWorkDir(root)
		if !ok {
			continue // outside the work tree; git will never see it
		}
		s.staticExcludes = appendUnique(s.staticExcludes, anchorPattern(rel))
	}
}

// refreshExcludesLocked (re)computes the exclude file: static patterns plus
// patterns for every file larger than maxFileBytes. When the exclude set
// changes, the index is emptied so the next `git add --all` re-stages the
// tree without previously-staged excluded paths. Callers must hold s.mu.
func (s *Service) refreshExcludesLocked() {
	// Respect the TTL: skip the (potentially expensive) walk if the exclude
	// file was computed recently.
	if s.lastExcludeContent != "" && time.Since(s.excludesRefreshedAt) < excludeRefreshInterval {
		return
	}
	s.excludesRefreshedAt = time.Now()
	patterns := append([]string{}, s.staticExcludes...)
	if s.maxFileBytes > 0 {
		patterns = append(patterns, s.scanBigFilesLocked()...)
	}

	var b strings.Builder
	b.WriteString(excludeFileHeader + "\n")
	for _, p := range patterns {
		b.WriteString(p + "\n")
	}
	content := b.String()
	if content == s.lastExcludeContent {
		return // nothing changed
	}

	excludePath := filepath.Join(s.gitDir, "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(excludePath), 0755); err != nil {
		return // best-effort
	}
	if err := os.WriteFile(excludePath, []byte(content), 0644); err != nil {
		return // best-effort
	}
	s.lastExcludeContent = content

	// The exclude set changed: empty the index so already-staged excluded
	// paths (from a legacy store, or newly oversized files) drop out on the
	// next add. --ignore-unmatch makes this a no-op-safe operation.
	_, _ = s.git("rm", "--cached", "-r", "--quiet", "--ignore-unmatch", ".")
}

// scanBigFilesLocked walks the working tree and returns anchored gitignore
// patterns for every regular file larger than maxFileBytes. Skips the
// excluded roots (data dir / object store) and .git directories. Callers must
// hold s.mu.
func (s *Service) scanBigFilesLocked() []string {
	var patterns []string
	_ = filepath.WalkDir(s.workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		rel, ok := s.relToWorkDir(path)
		if !ok {
			return nil
		}
		if d.IsDir() {
			if rel == ".git" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			if s.isExcludedPath(rel) {
				return filepath.SkipDir // data dir / object store subtree
			}
			return nil
		}
		if s.isExcludedPath(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() > s.maxFileBytes {
			patterns = appendUnique(patterns, anchorPattern(rel))
		}
		return nil
	})
	return patterns
}

// isExcludedPath reports whether rel (relative to workDir) falls under one of
// the static exclude roots. Callers must hold s.mu (reads staticExcludes).
func (s *Service) isExcludedPath(rel string) bool {
	clean := filepath.ToSlash(filepath.Clean(rel))
	for _, pattern := range s.staticExcludes {
		root := strings.TrimSuffix(strings.TrimPrefix(pattern, "/"), "/")
		if root == "" {
			continue
		}
		if clean == root || strings.HasPrefix(clean, root+"/") {
			return true
		}
	}
	return false
}

// relToWorkDir returns path relative to the working directory. ok is false
// when path is outside the working directory.
func (s *Service) relToWorkDir(path string) (string, bool) {
	rel, err := filepath.Rel(s.workDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// dropExcludedFromIndexLocked removes all statically excluded paths from
// the current index without touching the working tree. Callers must hold s.mu.
func (s *Service) dropExcludedFromIndexLocked() {
	roots := make([]string, 0, len(s.staticExcludes))
	for _, pattern := range s.staticExcludes {
		root := strings.TrimSuffix(strings.TrimPrefix(pattern, "/"), "/")
		if root != "" {
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		return
	}
	args := append([]string{"rm", "--cached", "-r", "--quiet", "--ignore-unmatch", "--"}, roots...)
	_, _ = s.git(args...)
}

// restageWithoutExcludes empties the index and re-stages the working tree so
// that excluded paths (object store, big files) are not part of the index
// after an index-mutating operation. Callers must hold s.mu.
func (s *Service) restageWithoutExcludes() {
	_, _ = s.git("rm", "--cached", "-r", "--quiet", "--ignore-unmatch", ".")
	_, _ = s.git("add", "--all")
}

// anchorPattern converts a relative path into an anchored gitignore pattern.
func anchorPattern(rel string) string {
	return "/" + escapeGitignore(filepath.ToSlash(filepath.Clean(rel)))
}

// escapeGitignore escapes gitignore metacharacters so the pattern matches a
// literal path.
func escapeGitignore(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch c {
		case '\\', '*', '?', '[', ']', '#', '!':
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}

// maxFileBytesFromEnv reads COVO_SNAPSHOT_MAX_FILE_MB. Absent/invalid values
// fall back to the default; an explicit 0 disables the size limit.
func maxFileBytesFromEnv() int64 {
	v := strings.TrimSpace(os.Getenv("COVO_SNAPSHOT_MAX_FILE_MB"))
	if v == "" {
		return defaultMaxFileBytes
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return defaultMaxFileBytes
	}
	if n == 0 {
		return 0 // explicit opt-out of the size limit
	}
	return n << 20
}

// hashPath creates a stable filesystem-safe hash for a working directory path.
func hashPath(p string) string {
	h := sha1.Sum([]byte(p))
	return hex.EncodeToString(h[:8])
}

// appendUnique appends s to list if not already present.
func appendUnique(list []string, s string) []string {
	for _, existing := range list {
		if existing == s {
			return list
		}
	}
	return append(list, s)
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
