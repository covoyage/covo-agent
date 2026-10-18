package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/covoyage/covo-agent/internal/fslock"
)

type BrowserOptions struct {
	ProfileDir string
}

func prepareBrowserProfile(path string) (string, func(), error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return "", nil, err
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("browser profile must be a dedicated directory, not a symlink")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return "", nil, fmt.Errorf("browser profile must be private (chmod 700)")
	}
	unlock, err := fslock.Acquire(filepath.Join(abs, ".covo-agent.lock"))
	if err != nil {
		return "", nil, err
	}
	ok := false
	defer func() {
		if !ok {
			unlock()
		}
	}()
	const marker = "covo-agent dedicated browser profile v1\n"
	markerPath := filepath.Join(abs, ".covo-agent-profile")
	if info, err := os.Lstat(markerPath); err == nil {
		if !info.Mode().IsRegular() || info.Size() != int64(len(marker)) {
			return "", nil, fmt.Errorf("invalid browser profile marker")
		}
		data, err := os.ReadFile(markerPath)
		if err != nil || string(data) != marker {
			return "", nil, fmt.Errorf("invalid browser profile marker")
		}
	} else if os.IsNotExist(err) {
		entries, err := os.ReadDir(abs)
		if err != nil {
			return "", nil, err
		}
		for _, entry := range entries {
			if entry.Name() != ".covo-agent.lock" {
				return "", nil, fmt.Errorf("browser profile must be empty or previously initialized by covo-agent; daily browser profiles are not imported")
			}
		}
		file, err := os.OpenFile(markerPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return "", nil, err
		}
		_, writeErr := file.WriteString(marker)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return "", nil, fmt.Errorf("cannot initialize browser profile")
		}
	} else {
		return "", nil, err
	}
	ok = true
	return abs, unlock, nil
}
