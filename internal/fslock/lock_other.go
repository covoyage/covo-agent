//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package fslock

import (
	"fmt"
	"os"
)

func lockFile(*os.File) error {
	return fmt.Errorf("filesystem locking is unsupported on this platform")
}
