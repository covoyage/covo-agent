package fileops

import (
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/text/unicode/norm"
)

var (
	fileMutationMu sync.Mutex
	fileMutexes    = make(map[string]*sync.Mutex)
)

func getFileMutexKey(path string) string {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		return resolved
	}
	return absPath
}

func withFileMutationLock(path string, fn func() error) error {
	key := getFileMutexKey(path)

	fileMutationMu.Lock()
	mu, ok := fileMutexes[key]
	if !ok {
		mu = &sync.Mutex{}
		fileMutexes[key] = mu
	}
	fileMutationMu.Unlock()

	mu.Lock()
	defer mu.Unlock()

	return fn()
}

func resolveToCwd(filePath, cwd string) string {
	if filepath.IsAbs(filePath) {
		return filePath
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return filepath.Join(cwd, filePath)
}

func resolveReadPath(filePath, cwd string) string {
	resolved := resolveToCwd(filePath, cwd)

	if _, err := os.Stat(resolved); err == nil {
		return resolved
	}

	nfdPath := norm.NFD.String(resolved)
	if _, err := os.Stat(nfdPath); err == nil {
		return nfdPath
	}

	return resolved
}
