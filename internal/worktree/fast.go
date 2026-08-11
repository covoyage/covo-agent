// Package worktree provides fast git worktree creation using CoW cloning.
//
// Strategy:
//  1. Use `git worktree add --no-checkout` for instant metadata creation
//  2. Copy files from the source tree using OS-native CoW operations
//     (macOS: clonefile, Linux: FICLONE ioctl, fallback: cp -r)
//  3. Optionally replicate dirty/ignored files
package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// FastWorktree creates git worktrees with CoW file cloning for speed.
type FastWorktree struct {
	repoDir string
}

// New creates a FastWorktree for the given repository directory.
func New(repoDir string) *FastWorktree {
	return &FastWorktree{repoDir: repoDir}
}

// CreateOptions controls worktree creation behavior.
type CreateOptions struct {
	// Branch is the branch to check out (creates if doesn't exist).
	Branch string
	// StartPoint is the commit or branch used to populate the worktree.
	StartPoint string
	// Detach creates the worktree with a detached HEAD.
	Detach bool
	// CopyDirtyFiles copies uncommitted changes from the source tree.
	CopyDirtyFiles bool
	// CopyIgnoredFiles copies gitignored files (e.g. node_modules).
	CopyIgnoredFiles bool
	// NoCheckout skips file checkout (metadata only).
	NoCheckout bool
	// ParallelCopies is the number of parallel copy workers.
	ParallelCopies int
}

// DefaultOptions returns sensible defaults.
func DefaultOptions() CreateOptions {
	return CreateOptions{
		NoCheckout:       true, // We'll do our own CoW checkout
		CopyDirtyFiles:   false,
		CopyIgnoredFiles: false,
		ParallelCopies:   runtime.NumCPU(),
	}
}

// Create creates a new worktree at the given path.
func (fw *FastWorktree) Create(worktreePath string, opts CreateOptions) error {
	if opts.ParallelCopies <= 0 {
		opts.ParallelCopies = runtime.NumCPU()
	}

	// 1. Create worktree metadata with --no-checkout
	args := []string{"worktree", "add", "--no-checkout"}
	if opts.Detach {
		args = append(args, "--detach")
	}
	if opts.Branch != "" {
		args = append(args, "-b", opts.Branch)
	}
	args = append(args, worktreePath)
	if opts.StartPoint != "" {
		args = append(args, opts.StartPoint)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = fw.repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add: %w\n%s", err, string(out))
	}

	if opts.NoCheckout {
		// 2. CoW checkout: clone files from source tree to worktree
		if err := fw.cowCheckout(fw.repoDir, worktreePath, opts); err != nil {
			return fmt.Errorf("cow checkout: %w", err)
		}
	}

	// 3. Optionally copy dirty files
	if opts.CopyDirtyFiles {
		if err := fw.copyDirtyFiles(worktreePath); err != nil {
			return fmt.Errorf("copy dirty files: %w", err)
		}
	}

	// 4. Optionally copy ignored files
	if opts.CopyIgnoredFiles {
		if err := fw.copyIgnoredFiles(worktreePath); err != nil {
			return fmt.Errorf("copy ignored files: %w", err)
		}
	}

	return nil
}

// cowCheckout copies files from source to dest using CoW operations.
func (fw *FastWorktree) cowCheckout(source, dest string, opts CreateOptions) error {
	// Get the list of tracked files
	cmd := exec.Command("git", "ls-files", "--cached", "--modified", "--others", "--exclude-standard")
	cmd.Dir = fw.repoDir
	out, err := cmd.Output()
	if err != nil {
		// Fallback: copy everything
		return copyDir(source, dest, opts.ParallelCopies)
	}

	files := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(files) == 0 || (len(files) == 1 && files[0] == "") {
		return nil
	}

	// Parallel copy
	var wg sync.WaitGroup
	sem := make(chan struct{}, opts.ParallelCopies)
	errs := make(chan error, len(files))

	for _, file := range files {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(f string) {
			defer wg.Done()
			defer func() { <-sem }()

			src := filepath.Join(source, f)
			dst := filepath.Join(dest, f)

			if err := copyFileCoW(src, dst); err != nil {
				// Skip missing files (might be deleted)
				if !os.IsNotExist(err) {
					errs <- err
				}
			}
		}(file)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			return err
		}
	}

	// Run git checkout-index to update worktree metadata
	checkout := exec.Command("git", "checkout-index", "-a", "-f")
	checkout.Dir = dest
	checkout.Run() // ignore error — files are already there

	return nil
}

// copyDirtyFiles copies uncommitted changes from source to worktree.
func (fw *FastWorktree) copyDirtyFiles(worktreePath string) error {
	cmd := exec.Command("git", "diff", "--name-only")
	cmd.Dir = fw.repoDir
	out, err := cmd.Output()
	if err != nil {
		return err
	}

	for _, file := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		src := filepath.Join(fw.repoDir, file)
		dst := filepath.Join(worktreePath, file)
		if err := copyFileCoW(src, dst); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("copy %s: %w", file, err)
		}
	}
	return nil
}

// copyIgnoredFiles copies gitignored files from source to worktree.
func (fw *FastWorktree) copyIgnoredFiles(worktreePath string) error {
	cmd := exec.Command("git", "ls-files", "--others", "--ignored", "--exclude-standard")
	cmd.Dir = fw.repoDir
	out, err := cmd.Output()
	if err != nil {
		return err
	}

	for _, file := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		src := filepath.Join(fw.repoDir, file)
		dst := filepath.Join(worktreePath, file)
		if err := copyFileCoW(src, dst); err != nil && !os.IsNotExist(err) {
			// Continue on error for ignored files
			continue
		}
	}
	return nil
}

// Remove removes a worktree.
func (fw *FastWorktree) Remove(worktreePath string) error {
	cmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	cmd.Dir = fw.repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %w\n%s", err, string(out))
	}
	return nil
}

// List returns all worktrees for the repository.
func (fw *FastWorktree) List() ([]string, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = fw.repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var worktrees []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			worktrees = append(worktrees, strings.TrimPrefix(line, "worktree "))
		}
	}
	return worktrees, nil
}

// Stats returns performance statistics for worktree creation.
type Stats struct {
	Duration    time.Duration `json:"duration"`
	FilesCopied int           `json:"files_copied"`
	CoWUsed     bool          `json:"cow_used"`
	BytesCopied int64         `json:"bytes_copied"`
}

// CreateWithStats creates a worktree and returns performance stats.
func (fw *FastWorktree) CreateWithStats(worktreePath string, opts CreateOptions) (*Stats, error) {
	start := time.Now()

	stats := &Stats{CoWUsed: supportsCoW()}

	// Count files before
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = fw.repoDir
	out, err := cmd.Output()
	if err == nil {
		stats.FilesCopied = len(strings.Split(strings.TrimSpace(string(out)), "\n"))
	}

	if err := fw.Create(worktreePath, opts); err != nil {
		return nil, err
	}

	stats.Duration = time.Since(start)

	// Calculate bytes copied
	if info, err := os.Stat(worktreePath); err == nil {
		stats.BytesCopied = info.Size()
	}

	return stats, nil
}

// ---------------------------------------------------------------------------
// Platform-specific CoW file copy
// ---------------------------------------------------------------------------

// copyFileCoW copies a file using Copy-on-Write when available.
func copyFileCoW(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return copyDir(src, dst, 1)
	}

	// Try platform-specific CoW first
	if err := copyFileCoWPlatform(src, dst, info.Mode()); err == nil {
		return nil
	}

	// Fallback: regular copy
	return copyFileRegular(src, dst, info.Mode())
}

// copyFileRegular does a standard file copy.
func copyFileRegular(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
}

// copyDir copies a directory tree.
func copyDir(src, dst string, workers int) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}

		// Skip symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		return copyFileRegular(path, target, info.Mode())
	})
}

// supportsCoW checks if the platform supports Copy-on-Write file cloning.
func supportsCoW() bool {
	switch runtime.GOOS {
	case "darwin":
		return true // APFS supports clonefile
	case "linux":
		return true // Btrfs/overlayfs support FICLONE
	default:
		return false
	}
}
