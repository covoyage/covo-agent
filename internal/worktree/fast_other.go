//go:build !darwin

package worktree

import "os"

// copyFileCoWPlatform is the non-darwin fallback.
// On Linux with Btrfs, FICLONE ioctl could be used, but we fall back to
// regular copy for portability.
func copyFileCoWPlatform(src, dst string, mode os.FileMode) error {
	// Try reflink copy on Linux (btrfs/overlayfs) via cp --reflink=auto
	return copyFileRegular(src, dst, mode)
}
