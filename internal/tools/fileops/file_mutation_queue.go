package fileops

import (
	"os"
	"path/filepath"
	"sort"
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

	absPath = filepath.Clean(absPath)
	for ancestor := absPath; ; ancestor = filepath.Dir(ancestor) {
		if resolved, err := filepath.EvalSymlinks(ancestor); err == nil {
			remainder, err := filepath.Rel(ancestor, absPath)
			if err == nil {
				return filepath.Join(resolved, remainder)
			}
		}
		if filepath.Dir(ancestor) == ancestor {
			break
		}
	}
	return absPath
}

func withFileMutationLock(path string, fn func() error) error {
	return WithFileMutationLocks([]string{path}, fn)
}

func WithFileMutationLocks(paths []string, fn func() error) error {
	unique := make(map[string]bool, len(paths))
	for _, path := range paths {
		unique[getFileMutexKey(path)] = true
	}
	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	locks := make([]*sync.Mutex, 0, len(keys))

	fileMutationMu.Lock()
	for _, key := range keys {
		mu, ok := fileMutexes[key]
		if !ok {
			mu = &sync.Mutex{}
			fileMutexes[key] = mu
		}
		locks = append(locks, mu)
	}
	fileMutationMu.Unlock()

	for _, mu := range locks {
		mu.Lock()
	}
	defer func() {
		for index := len(locks) - 1; index >= 0; index-- {
			locks[index].Unlock()
		}
	}()

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
