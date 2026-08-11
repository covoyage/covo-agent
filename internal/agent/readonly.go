package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReadOnlyManager manages file patterns that the agent should never modify.
// Patterns are loaded from built-in defaults, env var, and project .covoignore.
type ReadOnlyManager struct {
	patterns []readOnlyPattern
	workDir  string
}

type readOnlyPattern struct {
	raw     string
	matchFn func(path string) bool
}

// NewReadOnlyManager creates a ReadOnlyManager with built-in defaults,
// plus patterns from COVO_READ_ONLY and .covoignore.
func NewReadOnlyManager(workDir string) *ReadOnlyManager {
	m := &ReadOnlyManager{workDir: workDir}
	// Built-in default patterns
	defaults := []string{
		"vendor/**",
		"node_modules/**",
		"*.pb.go",
		"*_gen.go",
		"*_generated.go",
		"*.generated.*",
		"*.min.js",
		"*.min.css",
		"go.sum",
		"package-lock.json",
		"yarn.lock",
		"pnpm-lock.yaml",
		"Cargo.lock",
		"Gemfile.lock",
		"composer.lock",
		"poetry.lock",
	}
	for _, p := range defaults {
		m.addPattern(p)
	}
	// From env var
	if env := os.Getenv("COVO_READ_ONLY"); env != "" {
		for _, p := range strings.Split(env, ",") {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				m.addPattern(trimmed)
			}
		}
	}
	// From .covoignore in project root
	if workDir != "" {
		m.loadIgnoreFile(filepath.Join(workDir, ".covoignore"))
	}
	return m
}

func (m *ReadOnlyManager) addPattern(raw string) {
	m.patterns = append(m.patterns, readOnlyPattern{
		raw:     raw,
		matchFn: compileGlob(raw),
	})
}

func (m *ReadOnlyManager) loadIgnoreFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m.addPattern(line)
	}
}

// IsReadOnly returns true if the given path matches any read-only pattern.
// Patterns are matched against the last segment of the path (e.g. "vendor/**"
// matches "/any/project/vendor/...").
func (m *ReadOnlyManager) IsReadOnly(absPath string) bool {
	for _, p := range m.patterns {
		if p.matchFn(absPath) {
			return true
		}
	}
	return false
}

// Patterns returns the raw pattern strings for display/inspection.
func (m *ReadOnlyManager) Patterns() []string {
	var out []string
	for _, p := range m.patterns {
		out = append(out, p.raw)
	}
	return out
}

// compileGlob converts a glob pattern (with ** support) to a matching function.
// Supports: *, ?, [abc], ** (matches any number of directories).
// Patterns are matched against the full path. Directory-centric patterns like
// "vendor/**" match "vendor/..." at any depth.
func compileGlob(pattern string) func(string) bool {
	pattern = filepath.ToSlash(pattern)
	hasStarStar := strings.Contains(pattern, "**")

	// Pure filename pattern like "*.pb.go" — match against basename
	if !hasStarStar && !strings.Contains(pattern, "/") {
		return func(path string) bool {
			path = filepath.ToSlash(path)
			matched, _ := filepath.Match(pattern, filepath.Base(path))
			return matched
		}
	}

	// Simple non-** pattern with path — match against full path
	if !hasStarStar {
		return func(path string) bool {
			path = filepath.ToSlash(path)
			matched, _ := filepath.Match(pattern, path)
			return matched
		}
	}

	// **/X/** — match any path containing "/X/" as a directory component
	if strings.HasPrefix(pattern, "**/") && strings.HasSuffix(pattern, "/**") {
		mid := pattern[3 : len(pattern)-3]
		return func(path string) bool {
			path = filepath.ToSlash(path)
			if mid == "" {
				return true
			}
			target := "/" + mid + "/"
			return strings.Contains(path, target) || strings.HasSuffix(path, "/"+mid) || strings.HasPrefix(path, mid+"/")
		}
	}

	// **/ prefix — match any path that has the suffix as a trailing component
	if strings.HasPrefix(pattern, "**/") {
		suffix := pattern[3:]
		return func(path string) bool {
			path = filepath.ToSlash(path)
			return strings.HasSuffix(path, "/"+suffix) || strings.HasSuffix(path, suffix)
		}
	}

	// /** suffix — match any path that has the prefix as a directory component.
	// "vendor/**" matches "vendor/foo.go", "/project/vendor/foo.go", etc.
	if strings.HasSuffix(pattern, "/**") {
		prefix := pattern[:len(pattern)-3]
		return func(path string) bool {
			path = filepath.ToSlash(path)
			if strings.HasPrefix(path, prefix+"/") {
				return true
			}
			return strings.Contains(path, "/"+prefix+"/")
		}
	}

	// ** in the middle, e.g. "a/**/b"
	parts := strings.SplitN(pattern, "**", 2)
	prefix := parts[0]
	suffix := parts[1]
	return func(path string) bool {
		path = filepath.ToSlash(path)
		if prefix == "" {
			return strings.HasSuffix(path, suffix) || strings.Contains(path, "/"+suffix)
		}
		if !strings.HasPrefix(path, prefix) && !strings.Contains(path, "/"+prefix) {
			return false
		}
		if suffix == "" {
			return true
		}
		return strings.HasSuffix(path, suffix)
	}
}

// CheckReadOnly returns a user-facing error if the path matches read-only patterns.
func (m *ReadOnlyManager) CheckReadOnly(path string) error {
	if m == nil {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	if m.IsReadOnly(abs) {
		return fmt.Errorf("file %q is read-only (matched pattern)", filepath.Base(path))
	}
	// Also check relative to workDir
	if m.workDir != "" {
		rel, err := filepath.Rel(m.workDir, abs)
		if err == nil && rel != "." && m.IsReadOnly(rel) {
			return fmt.Errorf("file %q is read-only (matched pattern %q)", filepath.Base(path), rel)
		}
	}
	return nil
}

// MatchByGlob tests a single glob pattern against a path (useful for testing).
func MatchByGlob(pattern, path string) bool {
	return compileGlob(pattern)(path)
}
