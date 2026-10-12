//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package fslock

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockFile(file *os.File) error { return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
