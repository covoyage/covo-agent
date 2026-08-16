package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/safego"
)

const (
	defaultHookTimeout  = 60 * time.Second
	maxHookTimeout      = 300 * time.Second
	defaultBlockMessage = "Blocked by shell hook."

	// Circuit breaker parameters.
	cbFailureThreshold = 3               // consecutive failures before tripping
	cbCooldown         = 5 * time.Second // open-state duration before half-open trial
	cbExecTimeout      = 5 * time.Second // max execution time under circuit breaker

	// Hot reload parameters.
	hotReloadInterval = 500 * time.Millisecond
)

// HookEvent is the JSON payload written to a hook command's stdin. The field
// names follow the Claude Code hooks protocol (hook_event_name, tool_name,
// tool_input, tool_response, prompt, hook_input, transcript_path, cwd,
// session_id) plus the Codex-specific fields (model, permission_mode, source)
// so that existing Claude Code and Codex hook scripts work unchanged.
type HookEvent struct {
	EventName      string         `json:"hook_event_name"`
	ToolName       string         `json:"tool_name,omitempty"`
	ToolInput      map[string]any `json:"tool_input,omitempty"`
	ToolResponse   string         `json:"tool_response,omitempty"`
	Prompt         string         `json:"prompt,omitempty"`
	HookInput      map[string]any `json:"hook_input,omitempty"`
	TranscriptPath string         `json:"transcript_path,omitempty"`
	SessionID      string         `json:"session_id"`
	Cwd            string         `json:"cwd"`
	Model          string         `json:"model,omitempty"`
	PermissionMode string         `json:"permission_mode,omitempty"`
	Source         string         `json:"source,omitempty"`
	TimeoutSeconds int            `json:"timeout_seconds,omitempty"`
	Async          bool           `json:"async,omitempty"`
	Extra          map[string]any `json:"extra,omitempty"`
}

// HookResponse is parsed from a hook command's stdout. Decisions follow the
// Claude Code protocol: "approve" (allow), "deny"/"block" (stop the operation),
// "ask" (needs a human — treated as fail-open here, with the reason surfaced).
type HookResponse struct {
	Decision string `json:"decision,omitempty"`
	Action   string `json:"action,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Message  string `json:"message,omitempty"`
	Context  string `json:"context,omitempty"`
}

type ShellHookSpec struct {
	Event           string         `json:"event"`
	Command         string         `json:"command"`
	Matcher         string         `json:"matcher,omitempty"`
	Timeout         time.Duration  `json:"-"`
	Async           bool           `json:"async,omitempty"`
	compiledMatcher *regexp.Regexp `json:"-"`
}

// normalizeEvent canonicalizes an event name so both Claude Code camelCase
// types (PreToolUse, PostToolUse, ...) and existing snake_case / lowercase
// events ("stop", "pre_tool", ...) land in the same bucket.
func normalizeEvent(event string) string {
	return camelToSnake(strings.TrimSpace(event))
}

// camelToSnake converts CamelCase to snake_case (PreToolUse -> pre_tool_use)
// and lowercases everything else, leaving existing snake_case identifiers
// ("pre_tool", "stop") unchanged.
func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// compileMatcher compiles a tool-name matcher case-insensitively. Claude Code
// tool matchers are case-insensitive ("Bash" matches "bash"), so both the
// literal and regexp paths ignore case.
func compileMatcher(matcher string) *regexp.Regexp {
	re, err := regexp.Compile("(?i)" + matcher)
	if err != nil {
		return nil
	}
	return re
}

func (s *ShellHookSpec) MatchesTool(toolName string) bool {
	if s.Matcher == "" {
		return true
	}
	if s.compiledMatcher != nil {
		return s.compiledMatcher.MatchString(toolName)
	}
	return strings.EqualFold(toolName, s.Matcher)
}

// circuitBreaker implements a per-hook failure tracker with three states:
// closed (normal), open (tripped — skip hook), half-open (one trial after cooldown).
//
// After cbFailureThreshold consecutive failures, the breaker trips to "open"
// and skips the hook for cbCooldown. After that, one trial is allowed
// ("half-open"); success resets to closed, failure re-opens.
type circuitBreaker struct {
	mu              sync.Mutex
	failures        int       // consecutive failure count
	state           string    // "closed", "open", "half-open"
	lastFailureTime time.Time // when the breaker last tripped
	trialInProgress bool      // true when a half-open trial request is in flight
}

const (
	cbClosed   = "closed"
	cbOpen     = "open"
	cbHalfOpen = "half-open"
)

// allowExecution checks whether the hook should run. If the breaker is open
// and the cooldown hasn't elapsed, it returns false (skip). If the cooldown
// has elapsed, it transitions to half-open and allows one trial. While a
// half-open trial is in flight, subsequent callers are rejected until the
// trial completes (recordSuccess/recordFailure).
func (cb *circuitBreaker) allowExecution() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case cbClosed:
		return true
	case cbOpen:
		if time.Since(cb.lastFailureTime) >= cbCooldown {
			cb.state = cbHalfOpen
			cb.trialInProgress = true
			return true // allow one trial
		}
		return false // still in cooldown, skip
	case cbHalfOpen:
		// Only allow one trial request; reject others until the trial completes.
		if cb.trialInProgress {
			return false
		}
		cb.trialInProgress = true
		return true
	}
	return true
}

// recordSuccess resets the breaker to closed.
func (cb *circuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	cb.state = cbClosed
	cb.trialInProgress = false
}

// recordFailure increments the failure count and trips the breaker if the
// threshold is reached.
func (cb *circuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	cb.lastFailureTime = time.Now()
	cb.trialInProgress = false
	if cb.failures >= cbFailureThreshold {
		cb.state = cbOpen
	}
}

type ShellHookManager struct {
	mu            sync.Mutex
	hooks         map[string][]*ShellHookSpec
	allowlist     map[string]map[string]bool
	allowlistMu   sync.Mutex
	allowlistPath string
	autoAccept    bool
	registered    map[string]bool

	// Hot reload
	hotReloadStop   chan struct{}
	hotReloadPaths  []string
	hotReloadMtimes map[string]time.Time
	lastMtime       time.Time // most recent successful reload timestamp (newest across watched files)

	// Circuit breakers per hook (key: event:matcher:command)
	breakers   map[string]*circuitBreaker
	breakersMu sync.Mutex
}

func NewShellHookManager(hermesHome string, autoAccept bool) *ShellHookManager {
	m := &ShellHookManager{
		hooks:         make(map[string][]*ShellHookSpec),
		allowlist:     make(map[string]map[string]bool),
		allowlistPath: filepath.Join(hermesHome, "shell-hooks-allowlist.json"),
		autoAccept:    autoAccept,
		registered:    make(map[string]bool),
		breakers:      make(map[string]*circuitBreaker),
	}
	m.loadAllowlist()
	return m
}

// StartHotReload launches a background goroutine that stats the watched hooks
// files every 500ms and reloads them if any mtime changed. The project
// .covo-agent-hooks.json is always watched; extraPaths (e.g. Claude Code's
// .claude/hooks.json files) can be added for live reload too. Call Stop to
// terminate the goroutine. Safe to call multiple times (no-op if already
// running).
func (m *ShellHookManager) StartHotReload(workDir string, extraPaths ...string) {
	m.mu.Lock()
	if m.hotReloadStop != nil {
		m.mu.Unlock()
		return // already running
	}
	m.hotReloadStop = make(chan struct{})
	paths := make([]string, 0, 1+len(extraPaths))
	paths = append(paths, filepath.Join(workDir, ".covo-agent-hooks.json"))
	paths = append(paths, extraPaths...)
	m.hotReloadPaths = paths
	m.hotReloadMtimes = make(map[string]time.Time, len(paths))
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			m.hotReloadMtimes[p] = fi.ModTime()
			if fi.ModTime().After(m.lastMtime) {
				m.lastMtime = fi.ModTime()
			}
		}
	}
	stop := m.hotReloadStop
	m.mu.Unlock()

	safego.SafeGo(func() {
		ticker := time.NewTicker(hotReloadInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				m.checkAndReload(paths)
			}
		}
	}, nil)
}

// checkAndReload stats the watched hooks files and reloads all of them if any
// mtime changed.
//
// To avoid clearing hooks on a transient load failure (e.g. a file is
// mid-write and yields invalid JSON), the new hooks are loaded into temporary
// structures and only swapped in atomically on success. mtimes are updated
// only after a successful load so that a failed reload retries on the next
// poll.
func (m *ShellHookManager) checkAndReload(paths []string) {
	m.mu.Lock()
	prev := make(map[string]time.Time, len(m.hotReloadMtimes))
	for k, v := range m.hotReloadMtimes {
		prev[k] = v
	}
	m.mu.Unlock()

	changed := false
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue // file missing; nothing to reload
		}
		if prevT, ok := prev[p]; !ok || fi.ModTime().After(prevT) {
			changed = true
		}
	}
	if !changed {
		return
	}

	// Load into temporary structures first; only swap on success.
	newHooks, newRegistered, err := m.loadHooksForReload(paths)
	if err != nil {
		// Load failed (e.g. invalid JSON, file being written). Keep old hooks,
		// do not update mtimes — the next poll will retry.
		return
	}

	m.mu.Lock()
	m.hooks = newHooks
	m.registered = newRegistered
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			m.hotReloadMtimes[p] = fi.ModTime()
			if fi.ModTime().After(m.lastMtime) {
				m.lastMtime = fi.ModTime()
			}
		}
	}
	// Clean up circuit breakers for hooks that no longer exist after reload.
	m.cleanupStaleBreakers(newRegistered)
	m.mu.Unlock()
}

// loadHooksForReload parses all watched hooks files into fresh maps without
// mutating the manager's state. It supports every file shape (Claude Code
// "Hooks" array, settings.json "hooks" map, the legacy .covo-agent-hooks.json
// array, and Codex .codex/hooks.json) and mirrors the allowlist checks
// performed by Register. Results are written into local maps so the caller can
// atomically swap them in on success.
func (m *ShellHookManager) loadHooksForReload(paths []string) (map[string][]*ShellHookSpec, map[string]bool, error) {
	newHooks := make(map[string][]*ShellHookSpec)
	newRegistered := make(map[string]bool)

	register := func(entry map[string]any) {
		spec := &ShellHookSpec{Timeout: defaultHookTimeout}
		event, ok := entry["event"].(string)
		if !ok {
			return
		}
		spec.Event = normalizeEvent(event)
		cmd, ok := entry["command"].(string)
		if !ok {
			return
		}
		spec.Command = cmd
		if matcher, ok := entry["matcher"].(string); ok {
			spec.Matcher = strings.TrimSpace(matcher)
		}
		if timeout, ok := entry["timeout"].(float64); ok {
			spec.Timeout = time.Duration(timeout) * time.Second
		}
		if async, ok := entry["async"].(bool); ok {
			spec.Async = async
		}

		// Normalize timeout (mirror Register).
		if spec.Timeout <= 0 {
			spec.Timeout = defaultHookTimeout
		}
		if spec.Timeout > maxHookTimeout {
			spec.Timeout = maxHookTimeout
		}
		if spec.Matcher != "" {
			spec.compiledMatcher = compileMatcher(spec.Matcher)
		}

		key := fmt.Sprintf("%s:%s:%s", spec.Event, spec.Matcher, spec.Command)
		if newRegistered[key] {
			return
		}
		if !m.autoAccept && !m.IsAllowlisted(spec.Event, spec.Command) {
			return
		}
		newHooks[spec.Event] = append(newHooks[spec.Event], spec)
		newRegistered[key] = true
	}

	found := false
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		specs, err := parseHooksFile(data)
		if err != nil {
			return nil, nil, err
		}
		found = true
		for _, entry := range specs {
			register(entry)
		}
	}
	if !found {
		return nil, nil, os.ErrNotExist
	}

	return newHooks, newRegistered, nil
}

// cleanupStaleBreakers removes circuit breakers for hooks that are no longer
// registered. This prevents a previously-failing hook that was deleted and
// re-added from inheriting a stale "open" breaker. Must be called with m.mu
// held so that the registered map cannot be concurrently modified.
func (m *ShellHookManager) cleanupStaleBreakers(registered map[string]bool) {
	m.breakersMu.Lock()
	defer m.breakersMu.Unlock()
	for key := range m.breakers {
		if !registered[key] {
			delete(m.breakers, key)
		}
	}
}

// Stop terminates the hot reload goroutine. Safe to call multiple times.
func (m *ShellHookManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hotReloadStop != nil {
		close(m.hotReloadStop)
		m.hotReloadStop = nil
	}
}

// getBreaker returns (creating if needed) the circuit breaker for a hook.
func (m *ShellHookManager) getBreaker(key string) *circuitBreaker {
	m.breakersMu.Lock()
	defer m.breakersMu.Unlock()
	cb, ok := m.breakers[key]
	if !ok {
		cb = &circuitBreaker{state: cbClosed}
		m.breakers[key] = cb
	}
	return cb
}

func (m *ShellHookManager) loadAllowlist() {
	data, err := os.ReadFile(m.allowlistPath)
	if err != nil {
		return
	}
	json.Unmarshal(data, &m.allowlist)
}

func (m *ShellHookManager) saveAllowlist() error {
	data, err := json.MarshalIndent(m.allowlist, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.allowlistPath, data, 0600)
}

func (m *ShellHookManager) IsAllowlisted(event, command string) bool {
	m.allowlistMu.Lock()
	defer m.allowlistMu.Unlock()
	if cmds, ok := m.allowlist[event]; ok {
		return cmds[command]
	}
	return false
}

func (m *ShellHookManager) RecordAllowlist(event, command string) {
	m.allowlistMu.Lock()
	defer m.allowlistMu.Unlock()
	if m.allowlist[event] == nil {
		m.allowlist[event] = make(map[string]bool)
	}
	m.allowlist[event][command] = true
	m.saveAllowlist()
}

func (m *ShellHookManager) Register(spec *ShellHookSpec) error {
	spec.Event = normalizeEvent(spec.Event)
	if spec.Timeout <= 0 {
		spec.Timeout = defaultHookTimeout
	}
	if spec.Timeout > maxHookTimeout {
		spec.Timeout = maxHookTimeout
	}

	if spec.Matcher != "" {
		spec.compiledMatcher = compileMatcher(spec.Matcher)
	}

	key := fmt.Sprintf("%s:%s:%s", spec.Event, spec.Matcher, spec.Command)
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.registered[key] {
		return nil
	}

	if !m.autoAccept && !m.IsAllowlisted(spec.Event, spec.Command) {
		return fmt.Errorf("shell hook for %s (%s) not allowlisted", spec.Event, spec.Command)
	}

	m.hooks[spec.Event] = append(m.hooks[spec.Event], spec)
	m.registered[key] = true
	return nil
}

func (m *ShellHookManager) RegisterFromConfig(hooks []map[string]any) []*ShellHookSpec {
	var registered []*ShellHookSpec
	for _, entry := range hooks {
		spec := &ShellHookSpec{
			Timeout: defaultHookTimeout,
		}

		if event, ok := entry["event"].(string); ok {
			spec.Event = normalizeEvent(event)
		} else {
			continue
		}
		if cmd, ok := entry["command"].(string); ok {
			spec.Command = cmd
		} else {
			continue
		}
		if matcher, ok := entry["matcher"].(string); ok {
			spec.Matcher = strings.TrimSpace(matcher)
		}
		if timeout, ok := entry["timeout"].(float64); ok {
			spec.Timeout = time.Duration(timeout) * time.Second
		}
		if async, ok := entry["async"].(bool); ok {
			spec.Async = async
		}

		if err := m.Register(spec); err == nil {
			registered = append(registered, spec)
		}
	}
	return registered
}

// LoadProjectHooksFile reads .covo-agent-hooks.json from the working directory
// and registers any hooks found. Returns the number of hooks registered.
func (m *ShellHookManager) LoadProjectHooksFile(workDir string) int {
	path := filepath.Join(workDir, ".covo-agent-hooks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var cfg struct {
		Hooks []map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil || len(cfg.Hooks) == 0 {
		return 0
	}
	registered := m.RegisterFromConfig(cfg.Hooks)
	return len(registered)
}

// claudeHooksPaths returns the Claude Code hooks files in load/hot-reload
// priority order: user-level, project-level, then any $COVO_CLAUDE_HOOKS_PATH
// extra files. Kept as a single source of truth so initial load and hot
// reload watch exactly the same set of files (otherwise a reload would drop
// hooks that came from extra paths).
func claudeHooksPaths(workDir, homeDir string) []string {
	var paths []string
	if homeDir != "" {
		paths = append(paths, filepath.Join(homeDir, ".claude", "hooks.json"))
	}
	if workDir != "" {
		paths = append(paths, filepath.Join(workDir, ".claude", "hooks.json"))
	}
	for _, p := range filepath.SplitList(os.Getenv("COVO_CLAUDE_HOOKS_PATH")) {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// LoadClaudeHooks loads hooks from Claude Code's standard configuration
// locations so existing Claude Code hook scripts work in covo-agent unchanged:
//
//   - <homeDir>/.claude/hooks.json  (user-level, lowest priority)
//   - <workDir>/.claude/hooks.json  (project-level)
//   - $COVO_CLAUDE_HOOKS_PATH       (extra files, colon-separated)
//
// The .claude/hooks.json "Hooks" array format and the settings.json-style
// "hooks" map format are both supported. Set COVO_CLAUDE_HOOKS_DISABLED=true
// to skip loading entirely. Returns the number of hooks registered.
func (m *ShellHookManager) LoadClaudeHooks(workDir, homeDir string) int {
	if envBool("COVO_CLAUDE_HOOKS_DISABLED", false) {
		return 0
	}
	paths := claudeHooksPaths(workDir, homeDir)

	total := 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		specs, err := parseClaudeHooks(data)
		if err != nil {
			continue
		}
		total += len(m.RegisterFromConfig(specs))
	}
	return total
}

// parseClaudeHooks normalizes the various Claude Code hooks file shapes into
// the internal []map[string]any representation consumed by RegisterFromConfig
// (keys: event, command, matcher, timeout, async).
//
// Supported shapes:
//
//  1. .claude/hooks.json array format:
//
//     {
//     "Hooks": [
//     { "Matcher": "Bash", "Hooks": [
//     { "Type": "PreToolUse", "Command": "node check.js", "TimeoutSeconds": 30 }
//     ]}
//     ]
//     }
//
//  2. settings.json-style map format:
//
//     {
//     "hooks": {
//     "PreToolUse": [ { "Matcher": "Bash", "Command": "node check.js" } ]
//     }
//     }
//
//  3. Legacy array format (same shape as .covo-agent-hooks.json):
//
//     { "hooks": [ { "event": "stop", "command": "..." } ] }
func parseClaudeHooks(data []byte) ([]map[string]any, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	// 1. "Hooks" array format.
	if groupRaw, ok := raw["Hooks"]; ok {
		var groups []struct {
			Matcher string `json:"Matcher"`
			Hooks   []struct {
				Type           string  `json:"Type"`
				Command        string  `json:"Command"`
				Matcher        string  `json:"Matcher"`
				TimeoutSeconds float64 `json:"TimeoutSeconds"`
				Async          bool    `json:"Async"`
			} `json:"Hooks"`
		}
		if err := json.Unmarshal(groupRaw, &groups); err != nil {
			return nil, err
		}
		var specs []map[string]any
		for _, g := range groups {
			for _, h := range g.Hooks {
				if h.Type == "" || h.Command == "" {
					continue
				}
				matcher := g.Matcher
				if h.Matcher != "" {
					matcher = h.Matcher
				}
				specs = append(specs, map[string]any{
					"event":   h.Type,
					"command": h.Command,
					"matcher": matcher,
					"timeout": h.TimeoutSeconds,
					"async":   h.Async,
				})
			}
		}
		return specs, nil
	}

	// 2. "hooks" key — either the settings.json map format or the legacy
	// array format. Distinguish by the first non-space byte.
	if hooksRaw, ok := raw["hooks"]; ok {
		if len(hooksRaw) > 0 && hooksRaw[0] == '[' {
			var legacy []map[string]any
			if err := json.Unmarshal(hooksRaw, &legacy); err != nil {
				return nil, err
			}
			return legacy, nil
		}
		var hooks map[string][]struct {
			Matcher        string  `json:"Matcher"`
			Command        string  `json:"Command"`
			TimeoutSeconds float64 `json:"TimeoutSeconds"`
			Async          bool    `json:"Async"`
		}
		if err := json.Unmarshal(hooksRaw, &hooks); err != nil {
			return nil, err
		}
		var specs []map[string]any
		for event, list := range hooks {
			for _, h := range list {
				if h.Command == "" {
					continue
				}
				specs = append(specs, map[string]any{
					"event":   event,
					"command": h.Command,
					"matcher": h.Matcher,
					"timeout": h.TimeoutSeconds,
					"async":   h.Async,
				})
			}
		}
		return specs, nil
	}

	return nil, fmt.Errorf("hooks file contains no supported format")
}

// parseCodexHooks normalizes OpenAI Codex .codex/hooks.json into the internal
// []map[string]any representation (keys: event, command, matcher, timeout,
// async). Codex uses the same camelCase event names as Claude Code
// (PreToolUse/PostToolUse/UserPromptSubmit/Stop/SessionStart), so normalized
// events land in the same buckets and share the decision protocol:
//
//	{
//	  "hooks": {
//	    "PreToolUse": [
//	      {
//	        "matcher": "^Bash$",
//	        "hooks": [
//	          { "type": "command", "command": "node check.js", "timeout": 30 }
//	        ]
//	      }
//	    ]
//	  }
//	}
//
// matcher is a regular expression; "*", "", or omission match all tools.
// UserPromptSubmit and Stop ignore matchers (every handler fires). Only
// "command" handlers are supported; other handler types are skipped. timeout
// (or the timeoutSec alias) is in seconds.
func parseCodexHooks(data []byte) ([]map[string]any, error) {
	var raw struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type       string  `json:"type"`
				Command    string  `json:"command"`
				Timeout    float64 `json:"timeout"`
				TimeoutSec float64 `json:"timeoutSec"`
				Async      bool    `json:"async"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	var specs []map[string]any
	for event, groups := range raw.Hooks {
		for _, g := range groups {
			matcher := strings.TrimSpace(g.Matcher)
			if matcher == "*" {
				matcher = ""
			}
			switch event {
			case "UserPromptSubmit", "Stop":
				matcher = ""
			}
			for _, h := range g.Hooks {
				if h.Type != "" && h.Type != "command" {
					continue
				}
				if h.Command == "" {
					continue
				}
				timeout := h.Timeout
				if h.TimeoutSec > 0 {
					timeout = h.TimeoutSec
				}
				specs = append(specs, map[string]any{
					"event":   event,
					"command": h.Command,
					"matcher": matcher,
					"timeout": timeout,
					"async":   h.Async,
				})
			}
		}
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("hooks file contains no codex command handlers")
	}
	return specs, nil
}

// parseHooksFile parses a hooks file in either the Claude Code or Codex shape
// (used by hot reload, which watches a mix of files). Codex is tried first
// because a Claude map file yields zero specs under the Codex parser; both
// parsers then agree on which shape a file actually is.
func parseHooksFile(data []byte) ([]map[string]any, error) {
	if specs, err := parseCodexHooks(data); err == nil && len(specs) > 0 {
		return specs, nil
	}
	return parseClaudeHooks(data)
}

// codexHooksPaths returns the Codex hooks files in load/hot-reload priority
// order: user-level, project-level, then any $COVO_CODEX_HOOKS_PATH extra
// files. Kept as a single source of truth so initial load and hot reload
// watch exactly the same set of files.
func codexHooksPaths(workDir, homeDir string) []string {
	var paths []string
	if homeDir != "" {
		paths = append(paths, filepath.Join(homeDir, ".codex", "hooks.json"))
	}
	if workDir != "" {
		paths = append(paths, filepath.Join(workDir, ".codex", "hooks.json"))
	}
	for _, p := range filepath.SplitList(os.Getenv("COVO_CODEX_HOOKS_PATH")) {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// LoadCodexHooks loads hooks from OpenAI Codex's standard configuration
// locations so existing Codex hook scripts work in covo-agent unchanged:
//
//   - <homeDir>/.codex/hooks.json  (user-level, lowest priority)
//   - <workDir>/.codex/hooks.json  (project-level)
//   - $COVO_CODEX_HOOKS_PATH       (extra files, colon-separated)
//
// Codex events share names with Claude Code events, so hooks registered here
// land in the same buckets. Set COVO_CODEX_HOOKS_DISABLED=true to skip loading
// entirely. Returns the number of hooks registered.
func (m *ShellHookManager) LoadCodexHooks(workDir, homeDir string) int {
	if envBool("COVO_CODEX_HOOKS_DISABLED", false) {
		return 0
	}
	paths := codexHooksPaths(workDir, homeDir)

	total := 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		specs, err := parseCodexHooks(data)
		if err != nil {
			continue
		}
		total += len(m.RegisterFromConfig(specs))
	}
	return total
}

type HookResult struct {
	Blocked  bool
	Reason   string
	Context  string
	Executed bool
}

// HasEvent reports whether at least one hook is registered for the given
// event (after normalization). Lets callers skip payload building entirely.
func (m *ShellHookManager) HasEvent(event string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.hooks[normalizeEvent(event)]) > 0
}

func (m *ShellHookManager) Invoke(event string, payload *HookEvent) *HookResult {
	event = normalizeEvent(event)
	m.mu.Lock()
	specs := m.hooks[event]
	m.mu.Unlock()

	if len(specs) == 0 {
		return nil
	}

	for _, spec := range specs {
		if spec.Event != event {
			continue
		}
		if payload.ToolName != "" && !spec.MatchesTool(payload.ToolName) {
			continue
		}

		// Circuit breaker: skip hooks that have tripped.
		cbKey := fmt.Sprintf("%s:%s:%s", spec.Event, spec.Matcher, spec.Command)
		cb := m.getBreaker(cbKey)
		if !cb.allowExecution() {
			// Breaker is open — skip this hook (fail open, don't block).
			continue
		}

		// Async hooks run in the background and never block the operation
		// (their decision is intentionally ignored). Used for notification
		// and audit-style hooks.
		if spec.Async {
			spec, payload := spec, payload
			safego.SafeGo(func() {
				m.executeHook(spec, payload, cb)
			}, nil)
			continue
		}

		result := m.executeHook(spec, payload, cb)
		if result != nil && result.Executed {
			return result
		}
	}

	return nil
}

// executeHook runs a single hook command with circuit-breaker-wrapped timeout.
// On failure (execution error or timeout), the breaker is recorded and the
// hook fails open (returns nil — does not block the operation).
func (m *ShellHookManager) executeHook(spec *ShellHookSpec, payload *HookEvent, cb *circuitBreaker) *HookResult {
	if payload.Cwd == "" {
		payload.Cwd, _ = os.Getwd()
	}

	stdinData, err := json.Marshal(payload)
	if err != nil {
		cb.recordFailure()
		return nil
	}

	// Use the shorter of the spec timeout and the circuit breaker timeout.
	timeout := spec.Timeout
	if timeout > cbExecTimeout {
		timeout = cbExecTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", spec.Command)
	cmd.Stdin = bytes.NewReader(stdinData)
	cmd.Env = os.Environ()

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	err = cmd.Run()
	if err != nil {
		// Execution failure or timeout — record failure for circuit breaker
		// and fail open (don't block the operation).
		cb.recordFailure()
		return nil
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		// Empty output is not a failure — the hook ran successfully but
		// produced no decision. Treat as success (no-op).
		cb.recordSuccess()
		return nil
	}

	var resp HookResponse
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		// Invalid JSON output — record failure (the hook is misbehaving).
		cb.recordFailure()
		return nil
	}

	// Successful execution with valid response.
	cb.recordSuccess()

	result := &HookResult{Executed: true}

	// Claude Code protocol: "deny" and "block" both stop the operation.
	if resp.Decision == "block" || resp.Decision == "deny" ||
		resp.Action == "block" || resp.Action == "deny" {
		result.Blocked = true
		reason := resp.Reason
		if reason == "" {
			reason = resp.Message
		}
		if reason == "" {
			reason = defaultBlockMessage
		}
		result.Reason = reason
	}

	if resp.Context != "" {
		result.Context = resp.Context
	}

	return result
}
