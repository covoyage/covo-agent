// Package trust implements a folder-trust gate that protects users from
// running covo-agent in untrusted directories containing repo-local code
// execution configurations (hooks, MCP servers, .envrc, etc.).
//
// The trust store is persisted at ~/.covo-agent/trusted_folders.toml.
// The precedence chain follows the trust policy design:
//  1. Feature flag off → trusted
//  2. Store (self/ancestor recorded trusted) → trusted
//  3. No repo-local code-exec configs → trusted
//  4. Interactive TTY → prompt user
//  5. Headless → untrusted
package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Outcome represents the trust decision for a workspace.
type Outcome int

const (
	OutcomeTrusted   Outcome = iota // safe to run
	OutcomeUntrusted                // blocked
	OutcomePrompt                   // needs user confirmation
)

func (o Outcome) String() string {
	switch o {
	case OutcomeTrusted:
		return "trusted"
	case OutcomeUntrusted:
		return "untrusted"
	case OutcomePrompt:
		return "prompt"
	default:
		return "unknown"
	}
}

// CodeExecConfig represents a repo-local configuration that could execute code.
type CodeExecConfig struct {
	Path        string // relative path from workspace root
	Type        string // "hooks", "mcp", "envrc", "scripts"
	Description string
}

// TrustStore persists trusted folder records.
type TrustStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]bool // workspace_key -> trusted
}

// NewTrustStore creates or loads a trust store at the given path
// (typically ~/.covo-agent/trusted_folders.toml).
func NewTrustStore(path string) (*TrustStore, error) {
	ts := &TrustStore{
		path:    path,
		entries: make(map[string]bool),
	}
	if err := ts.load(); err != nil {
		return nil, fmt.Errorf("load trust store: %w", err)
	}
	return ts, nil
}

// workspaceKey generates a stable key for a workspace path.
func workspaceKey(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	h := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(h[:16])
}

// IsTrusted checks if the directory or any ancestor is in the trust store.
func (ts *TrustStore) IsTrusted(dir string) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}

	// Walk up the directory tree
	for {
		key := workspaceKey(abs)
		if ts.entries[key] {
			return true
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
	}
	return false
}

// Grant marks a directory as trusted and persists the store.
func (ts *TrustStore) Grant(dir string) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	ts.entries[workspaceKey(abs)] = true
	return ts.saveLocked()
}

// Revoke removes a directory from the trust store.
func (ts *TrustStore) Revoke(dir string) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	delete(ts.entries, workspaceKey(abs))
	return ts.saveLocked()
}

// All returns all trusted workspace keys.
func (ts *TrustStore) All() []string {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	keys := make([]string, 0, len(ts.entries))
	for k := range ts.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (ts *TrustStore) load() error {
	data, err := os.ReadFile(ts.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Simple key=value format: <hash> = true
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if val == "true" {
			ts.entries[key] = true
		}
	}
	return nil
}

func (ts *TrustStore) saveLocked() error {
	var b strings.Builder
	b.WriteString("# covo-agent trusted folders — DO NOT EDIT MANUALLY\n")
	b.WriteString("# This file records folders you have explicitly trusted.\n\n")
	for k := range ts.entries {
		b.WriteString(k)
		b.WriteString(" = true\n")
	}
	return os.WriteFile(ts.path, []byte(b.String()), 0o644)
}

// ScanCodeExecConfigs scans the workspace directory for repo-local
// code execution configurations that could run arbitrary code.
func ScanCodeExecConfigs(dir string) []CodeExecConfig {
	var configs []CodeExecConfig

	// Patterns to look for (relative to workspace root)
	patterns := []struct {
		relPath    string
		configType string
		desc       string
	}{
		{".covo-agent/hooks", "hooks", "covo-agent hooks directory"},
		{".covo-agent/hooks.json", "hooks", "covo-agent hooks configuration"},
		{".covo-agent/mcp.json", "mcp", "covo-agent MCP server configuration"},
		{".covo-agent/config.yaml", "config", "covo-agent configuration"},
		{".envrc", "envrc", "direnv environment file"},
		{".claude/settings.json", "hooks", "Claude Code settings with hooks"},
		{".claude/hooks", "hooks", "Claude Code hooks directory"},
		{".grok/hooks", "hooks", "Grok hooks directory"},
		{".grok/config.toml", "config", "Grok configuration"},
		{".cursor/mcp.json", "mcp", "Cursor MCP server configuration"},
	}

	for _, p := range patterns {
		full := filepath.Join(dir, p.relPath)
		if _, err := os.Stat(full); err == nil {
			rel, _ := filepath.Rel(dir, full)
			configs = append(configs, CodeExecConfig{
				Path:        rel,
				Type:        p.configType,
				Description: p.desc,
			})
		}
	}

	return configs
}

// IsUnsafeRoot checks if the directory is an over-broad root (user's $HOME,
// filesystem root) that the trust store should refuse to persist.
func IsUnsafeRoot(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return true
	}
	if abs == "/" || abs == filepath.Clean("/") {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil && abs == home {
		return true
	}
	return false
}

// Decide resolves the trust outcome for a workspace directory.
//
// Parameters:
//   - dir: the workspace directory
//   - store: the trust store (may be nil to skip persistence)
//   - interactive: true if running in an interactive TTY
//   - enabled: true if folder trust feature is enabled
func Decide(dir string, store *TrustStore, interactive bool, enabled bool) (Outcome, []CodeExecConfig) {
	// 1. Feature flag off → trusted
	if !enabled {
		return OutcomeTrusted, nil
	}

	// 2. Store (self/ancestor recorded trusted) → trusted
	if store != nil && store.IsTrusted(dir) {
		return OutcomeTrusted, nil
	}

	// 3. Unsafe root → trusted (can't durably gate)
	if IsUnsafeRoot(dir) {
		return OutcomeTrusted, nil
	}

	// 4. No repo-local code-exec configs → trusted
	configs := ScanCodeExecConfigs(dir)
	if len(configs) == 0 {
		return OutcomeTrusted, nil
	}

	// 5. Interactive TTY → prompt
	if interactive {
		return OutcomePrompt, configs
	}

	// 6. Headless → untrusted
	return OutcomeUntrusted, configs
}

// CheckAndPrompt performs the trust check and, if in interactive mode and
// the outcome is OutcomePrompt, asks the user via stderr.
// Returns true if the workspace is trusted (either already trusted or
// the user confirmed), false otherwise.
func CheckAndPrompt(dir string, store *TrustStore, interactive bool, enabled bool) (bool, []CodeExecConfig) {
	outcome, configs := Decide(dir, store, interactive, enabled)
	switch outcome {
	case OutcomeTrusted:
		return true, configs
	case OutcomeUntrusted:
		return false, configs
	case OutcomePrompt:
		// Prompt the user
		fmt.Fprintf(os.Stderr, "\n⚠️  This workspace contains code-execution configurations:\n")
		for _, c := range configs {
			fmt.Fprintf(os.Stderr, "   • %s (%s)\n", c.Path, c.Description)
		}
		fmt.Fprintf(os.Stderr, "\nDo you trust this folder? (y/N): ")

		var answer string
		fmt.Fscanln(os.Stdin, &answer)
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer == "y" || answer == "yes" {
			if store != nil {
				_ = store.Grant(dir)
			}
			return true, configs
		}
		return false, configs
	default:
		return false, configs
	}
}
