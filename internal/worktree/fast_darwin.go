//go:build darwin

package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
)

// copyFileCoWPlatform uses `cp -c` on macOS to leverage APFS clonefile.
// The -c flag tells cp to use copy-on-write when the filesystem supports it.
func copyFileCoWPlatform(src, dst string, mode os.FileMode) error {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	// Use cp -c for CoW cloning on APFS
	cmd := exec.Command("cp", "-c", src, dst)
	if err := cmd.Run(); err != nil {
		// Fallback to regular copy
		return copyFileRegular(src, dst, mode)
	}
	return nil
}
