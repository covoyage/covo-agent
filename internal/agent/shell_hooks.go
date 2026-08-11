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

type HookEvent struct {
	EventName string         `json:"hook_event_name"`
	ToolName  string         `json:"tool_name,omitempty"`
	ToolInput map[string]any `json:"tool_input,omitempty"`
	SessionID string         `json:"session_id"`
	Cwd       string         `json:"cwd"`
	Extra     map[string]any `json:"extra,omitempty"`
}

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
	compiledMatcher *regexp.Regexp `json:"-"`
}

func (s *ShellHookSpec) MatchesTool(toolName string) bool {
	if s.Matcher == "" {
		return true
	}
	if s.compiledMatcher != nil {
		return s.compiledMatcher.MatchString(toolName)
	}
	return toolName == s.Matcher
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
	hotReloadStop chan struct{}
	hotReloadPath string
	lastMtime     time.Time

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

// StartHotReload launches a background goroutine that stats the hooks file
// every 500ms and reloads it if the mtime changed. Call Stop to terminate
// the goroutine. Safe to call multiple times (no-op if already running).
func (m *ShellHookManager) StartHotReload(workDir string) {
	m.mu.Lock()
	if m.hotReloadStop != nil {
		m.mu.Unlock()
		return // already running
	}
	m.hotReloadStop = make(chan struct{})
	m.hotReloadPath = filepath.Join(workDir, ".covo-agent-hooks.json")
	// Record initial mtime so we don't reload on first check.
	if fi, err := os.Stat(m.hotReloadPath); err == nil {
		m.lastMtime = fi.ModTime()
	}
	stop := m.hotReloadStop
	path := m.hotReloadPath
	m.mu.Unlock()

	safego.SafeGo(func() {
		ticker := time.NewTicker(hotReloadInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				m.checkAndReload(path)
			}
		}
	}, nil)
}

// checkAndReload stats the hooks file and reloads if the mtime changed.
//
// To avoid clearing hooks on a transient load failure (e.g. the file is
// mid-write and yields invalid JSON), the new hooks are loaded into temporary
// structures and only swapped in atomically on success. lastMtime is updated
// only after a successful load so that a failed reload retries on the next
// poll.
func (m *ShellHookManager) checkAndReload(path string) {
	fi, err := os.Stat(path)
	if err != nil {
		return // file may have been deleted; nothing to do
	}

	m.mu.Lock()
	prev := m.lastMtime
	m.mu.Unlock()

	if !fi.ModTime().After(prev) {
		return // no change
	}

	// Load into temporary structures first; only swap on success.
	newHooks, newRegistered, err := m.loadHooksForReload(filepath.Dir(path))
	if err != nil {
		// Load failed (e.g. invalid JSON, file being written). Keep old hooks,
		// do not update mtime — the next poll will retry.
		return
	}

	m.mu.Lock()
	m.hooks = newHooks
	m.registered = newRegistered
	m.lastMtime = fi.ModTime()
	// Clean up circuit breakers for hooks that no longer exist after reload.
	m.cleanupStaleBreakers(newRegistered)
	m.mu.Unlock()
}

// loadHooksForReload parses the project hooks file into fresh maps without
// mutating the manager's state. It mirrors the normalization and allowlist
// checks performed by Register/RegisterFromConfig, but writes into local
// maps so the caller can atomically swap them in on success.
func (m *ShellHookManager) loadHooksForReload(workDir string) (map[string][]*ShellHookSpec, map[string]bool, error) {
	path := filepath.Join(workDir, ".covo-agent-hooks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var cfg struct {
		Hooks []map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, nil, err
	}

	newHooks := make(map[string][]*ShellHookSpec)
	newRegistered := make(map[string]bool)

	for _, entry := range cfg.Hooks {
		spec := &ShellHookSpec{Timeout: defaultHookTimeout}
		if event, ok := entry["event"].(string); ok {
			spec.Event = event
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

		// Normalize timeout (mirror Register).
		if spec.Timeout <= 0 {
			spec.Timeout = defaultHookTimeout
		}
		if spec.Timeout > maxHookTimeout {
			spec.Timeout = maxHookTimeout
		}
		if spec.Matcher != "" {
			if re, err := regexp.Compile(spec.Matcher); err == nil {
				spec.compiledMatcher = re
			}
		}

		key := fmt.Sprintf("%s:%s:%s", spec.Event, spec.Matcher, spec.Command)
		if newRegistered[key] {
			continue
		}
		if !m.autoAccept && !m.IsAllowlisted(spec.Event, spec.Command) {
			continue
		}
		newHooks[spec.Event] = append(newHooks[spec.Event], spec)
		newRegistered[key] = true
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
	if spec.Timeout <= 0 {
		spec.Timeout = defaultHookTimeout
	}
	if spec.Timeout > maxHookTimeout {
		spec.Timeout = maxHookTimeout
	}

	if spec.Matcher != "" {
		re, err := regexp.Compile(spec.Matcher)
		if err != nil {
			re = nil
		}
		spec.compiledMatcher = re
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
			spec.Event = event
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

type HookResult struct {
	Blocked  bool
	Reason   string
	Context  string
	Executed bool
}

func (m *ShellHookManager) Invoke(event string, payload *HookEvent) *HookResult {
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

	if resp.Decision == "block" || resp.Action == "block" {
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
