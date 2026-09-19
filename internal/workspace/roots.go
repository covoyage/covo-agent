package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const MaxExtraRoots = 16

// ResolveExtraRoot canonicalizes a user-supplied extra workspace directory.
func ResolveExtraRoot(path, workingDir string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home directory: %w", err)
		}
		if path == "~" {
			path = home
		} else if strings.HasPrefix(path, "~/") {
			path = filepath.Join(home, path[2:])
		}
	}
	if !filepath.IsAbs(path) {
		base := workingDir
		if base == "" {
			base, _ = os.Getwd()
		}
		path = filepath.Join(base, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("path not found: %s", abs)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", abs)
	}
	if abs == string(filepath.Separator) || abs == filepath.VolumeName(abs)+string(filepath.Separator) {
		return "", fmt.Errorf("refusing filesystem root %s", abs)
	}
	if home, err := os.UserHomeDir(); err == nil {
		if homeAbs, err := filepath.Abs(home); err == nil && abs == homeAbs {
			return "", fmt.Errorf("refusing to add the home directory")
		}
	}
	return abs, nil
}

// AddExtraRoot appends a canonical directory, de-duplicating against the
// primary working directory and existing extra roots.
func AddExtraRoot(workingDir string, extra []string, path string) ([]string, string, error) {
	abs, err := ResolveExtraRoot(path, workingDir)
	if err != nil {
		return extra, "", err
	}
	if workAbs, err := filepath.Abs(workingDir); err == nil {
		if resolved, err := filepath.EvalSymlinks(workAbs); err == nil {
			workAbs = resolved
		}
		if abs == workAbs {
			return extra, "", fmt.Errorf("already the working directory")
		}
	}
	for _, existing := range extra {
		if existing == abs {
			return extra, "", fmt.Errorf("already added: %s", abs)
		}
	}
	if len(extra) >= MaxExtraRoots {
		return extra, "", fmt.Errorf("at most %d extra directories", MaxExtraRoots)
	}
	out := append(append([]string(nil), extra...), abs)
	return out, abs, nil
}

// Contains reports whether resolvedPath is inside any of the given roots.
func Contains(resolvedPath string, roots ...string) bool {
	path := resolvedPath
	if absPath, err := filepath.Abs(path); err == nil {
		path = absPath
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		if path == abs || strings.HasPrefix(path, abs+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
