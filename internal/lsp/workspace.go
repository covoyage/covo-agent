package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var workspaceCache = struct {
	mu    sync.RWMutex
	cache map[string]string
	reset func()
}{cache: make(map[string]string)}

func init() {
	workspaceCache.reset = func() {
		workspaceCache.mu.Lock()
		workspaceCache.cache = make(map[string]string)
		workspaceCache.mu.Unlock()
	}
}

func ClearWorkspaceCache() {
	workspaceCache.reset()
}

func ResolveWorkspaceForFile(filePath string, rootPatterns []string) string {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return ""
	}

	workspaceCache.mu.RLock()
	if cached, ok := workspaceCache.cache[abs]; ok {
		workspaceCache.mu.RUnlock()
		return cached
	}
	workspaceCache.mu.RUnlock()

	dir := filepath.Dir(abs)
	for {
		for _, pattern := range rootPatterns {
			if strings.Contains(pattern, "*") {
				matches, _ := filepath.Glob(filepath.Join(dir, pattern))
				if len(matches) > 0 {
					workspaceCache.mu.Lock()
					workspaceCache.cache[abs] = dir
					workspaceCache.mu.Unlock()
					return dir
				}
			} else {
				if _, err := os.Stat(filepath.Join(dir, pattern)); err == nil {
					workspaceCache.mu.Lock()
					workspaceCache.cache[abs] = dir
					workspaceCache.mu.Unlock()
					return dir
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	workspaceRoot := filepath.Dir(abs)
	workspaceCache.mu.Lock()
	workspaceCache.cache[abs] = workspaceRoot
	workspaceCache.mu.Unlock()
	return workspaceRoot
}

func NearestRoot(filePath string, rootPatterns []string) string {
	return ResolveWorkspaceForFile(filePath, rootPatterns)
}
