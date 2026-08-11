package approval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// --- Configuration ---

// Config controls approval behavior.
type Config struct {
	// Mode: "manual", "smart", or "off"
	Mode string

	// YoloMode bypasses all approval when true.
	YoloMode bool

	// CronMode: "deny" or "approve". When "deny", the system treats itself
	// as non-interactive (no user present) — see NonInteractive.
	CronMode string

	// NonInteractive should be true for background/cron/subagent sessions
	// where no user is present to approve a prompt. When true, any command
	// that reaches the manual-approval step is auto-denied instead of
	// hanging waiting for a human. Hardline blocks, policy denies, YOLO,
	// allowlists, and smart-approval still apply normally — only the final
	// "ask the user" fallback is replaced with a deny.
	NonInteractive bool

	// StorageDir for the persistent allowlist (default ~/.covo-agent/)
	StorageDir string

	// SmartApprovalFn is called for LLM-based approval when Mode=="smart".
	// Returns "approve", "deny", or "escalate".
	SmartApprovalFn func(ctx context.Context, command, description string) (string, error)

	// Hooks for pre/post approval events (observational only).
	PreApprovalHook  func(ctx context.Context, event ApprovalEvent)
	PostApprovalHook func(ctx context.Context, event ApprovalEvent)

	Logger *slog.Logger
}

// ApprovalEvent carries context for hook invocation.
type ApprovalEvent struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	PatternKey  string `json:"pattern_key"`
	Choice      string `json:"choice,omitempty"` // once, session, always, deny, timeout
}

// --- Decision ---

// Decision is the outcome of an approval check.
type Decision struct {
	Approved      bool   `json:"approved"`
	Hardline      bool   `json:"hardline,omitempty"`
	SmartApproved bool   `json:"smart_approved,omitempty"`
	Message       string `json:"message,omitempty"`
	PatternKey    string `json:"pattern_key,omitempty"`
	Description   string `json:"description,omitempty"`
}

var (
	hardlineBlocked = Decision{
		Approved: false,
		Hardline: true,
		Message: "BLOCKED (hardline): This command is on the unconditional " +
			"blocklist and cannot be executed via the agent, not even with YOLO mode. " +
			"Run it manually in your own terminal.",
	}

	sudoStdinBlocked = Decision{
		Approved: false,
		Message: "BLOCKED: sudo password guessing via stdin (sudo -S). " +
			"Do not pipe passwords to sudo -S. Set SUDO_PASSWORD if the agent " +
			"needs passwordless sudo, or run the sudo command manually.",
	}

	deniedByUser = Decision{
		Approved: false,
		Message:  "BLOCKED: User denied this potentially dangerous command. Do NOT retry.",
	}

	nonInteractiveDenied = Decision{
		Approved: false,
		Message: "BLOCKED: Dangerous command detected but this session is non-interactive " +
			"(no user present to approve it). Find an alternative approach.",
	}
)

// --- System ---

// System manages approval state and orchestrates checks.
type System struct {
	mu     sync.Mutex
	config Config

	// Session-scoped state
	sessionApproved map[string]map[string]bool // sessionKey -> {patternKey -> true}
	sessionYolo     map[string]bool            // sessionKey -> yolo enabled

	// Permanent allowlist (loaded from disk)
	permanentApproved map[string]bool

	logger *slog.Logger
}

// NewSystem creates a new approval system.
func NewSystem(cfg Config) *System {
	if cfg.StorageDir == "" {
		home, _ := os.UserHomeDir()
		cfg.StorageDir = filepath.Join(home, ".covo-agent")
	}
	if cfg.Mode == "" {
		cfg.Mode = "manual"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	// CronMode "deny" means no user is present to approve prompts — treat as
	// non-interactive. CronMode "approve" (or empty) is interactive and has no
	// effect here.
	if cfg.CronMode == "deny" {
		cfg.NonInteractive = true
	}
	s := &System{
		config:            cfg,
		sessionApproved:   make(map[string]map[string]bool),
		sessionYolo:       make(map[string]bool),
		permanentApproved: make(map[string]bool),
		logger:            cfg.Logger,
	}
	s.loadAllowlist()
	return s
}

// --- YOLO ---

// EnableSessionYolo enables YOLO bypass for a session.
func (s *System) EnableSessionYolo(sessionKey string) {
	if sessionKey == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionYolo[sessionKey] = true
}

// DisableSessionYolo disables YOLO bypass for a session.
func (s *System) DisableSessionYolo(sessionKey string) {
	if sessionKey == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessionYolo, sessionKey)
}

// IsSessionYolo returns whether YOLO is enabled for a session.
func (s *System) IsSessionYolo(sessionKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionYolo[sessionKey]
}

// IsYolo returns true when YOLO mode is active via any mechanism.
func (s *System) IsYolo() bool {
	return s.config.YoloMode || s.IsSessionYolo("cli")
}

// --- Session approvals ---

// ApproveSession grants session-scoped approval for a pattern.
func (s *System) ApproveSession(sessionKey, patternKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionApproved[sessionKey] == nil {
		s.sessionApproved[sessionKey] = make(map[string]bool)
	}
	s.sessionApproved[sessionKey][patternKey] = true
}

// ApprovePermanent grants permanent approval and saves to disk.
func (s *System) ApprovePermanent(patternKey string) {
	s.mu.Lock()
	s.permanentApproved[patternKey] = true
	s.mu.Unlock()
	s.saveAllowlist()
}

// IsApproved checks if a pattern is approved (session or permanent).
func (s *System) IsApproved(sessionKey, patternKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.permanentApproved[patternKey] {
		return true
	}
	if session, ok := s.sessionApproved[sessionKey]; ok {
		return session[patternKey]
	}
	return false
}

// ClearSession removes all approval state for a session.
func (s *System) ClearSession(sessionKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessionApproved, sessionKey)
	delete(s.sessionYolo, sessionKey)
}

// SetSmartApprovalFn updates the smart approval function.
func (s *System) SetSmartApprovalFn(fn func(ctx context.Context, command, description string) (string, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.SmartApprovalFn = fn
}

// SetPreApprovalHook sets the pre-approval plugin hook.
func (s *System) SetPreApprovalHook(hook func(ctx context.Context, event ApprovalEvent)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.PreApprovalHook = hook
}

// SetPostApprovalHook sets the post-approval plugin hook.
func (s *System) SetPostApprovalHook(hook func(ctx context.Context, event ApprovalEvent)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.PostApprovalHook = hook
}

// SetNonInteractive toggles non-interactive mode at runtime. When true, any
// command that reaches the manual-approval fallback (step 9) is auto-denied
// instead of hanging waiting for a human. Use this for oneshot, cron, and
// subagent sessions where no user is present.
func (s *System) SetNonInteractive(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.NonInteractive = v
}

// IsNonInteractive returns whether the system is in non-interactive mode.
func (s *System) IsNonInteractive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config.NonInteractive
}

// --- Allowlist persistence ---

const allowlistFile = "approval_allowlist.json"

func (s *System) allowlistPath() string {
	return filepath.Join(s.config.StorageDir, allowlistFile)
}

func (s *System) loadAllowlist() {
	path := s.allowlistPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return // file doesn't exist or can't be read, start fresh
	}
	var entries []string
	if err := json.Unmarshal(data, &entries); err != nil {
		s.logger.Warn("approval: failed to parse allowlist, starting fresh", "error", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range entries {
		s.permanentApproved[entry] = true
	}
}

func (s *System) saveAllowlist() {
	s.mu.Lock()
	entries := make([]string, 0, len(s.permanentApproved))
	for k := range s.permanentApproved {
		entries = append(entries, k)
	}
	s.mu.Unlock()

	if err := os.MkdirAll(s.config.StorageDir, 0o700); err != nil {
		s.logger.Warn("approval: failed to create storage dir", "error", err)
		return
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		s.logger.Warn("approval: failed to marshal allowlist", "error", err)
		return
	}
	if err := os.WriteFile(s.allowlistPath(), data, 0o600); err != nil {
		s.logger.Warn("approval: failed to save allowlist", "error", err)
		return
	}
}

// --- Hooks ---

func (s *System) firePreApproval(event ApprovalEvent) {
	if s.config.PreApprovalHook != nil {
		s.config.PreApprovalHook(context.Background(), event)
	}
}

func (s *System) firePostApproval(event ApprovalEvent) {
	if s.config.PostApprovalHook != nil {
		s.config.PostApprovalHook(context.Background(), event)
	}
}

// --- Main check entry point ---

// CheckCommand evaluates a command and returns whether it's approved.
// This is the main entry point for the approval pipeline.
//
// Pipeline:
//  1. Hardline check (unconditional block)
//  2. Sudo stdin guard (unconditional block)
//  3. Policy deny (explicit deny rules, overrides YOLO)
//  4. YOLO / mode=off bypass
//  5. Policy allow (explicit allow rules skip pattern detection)
//  6. Pattern detection
//  7. Session/permanent allowlist check
//  8. Smart approval (if mode=smart)
//  9. Non-interactive guard: auto-deny if no user is present
//  10. Fall through: reports "approval required" to caller
func (s *System) CheckCommand(ctx context.Context, command, sessionKey string) *Decision {
	// 1. Hardline floor: always blocked, even with YOLO
	if isHardline, desc := DetectHardline(command); isHardline {
		s.logger.Warn("approval: hardline block", "description", desc, "command_preview", truncate(command, 200))
		d := hardlineBlocked
		d.Message = fmt.Sprintf("%s Reason: %s.", hardlineBlocked.Message, desc)
		return &d
	}

	// 2. Sudo stdin guard
	if isBlocked, desc := CheckSudoStdin(command); isBlocked {
		d := sudoStdinBlocked
		d.Message = fmt.Sprintf("%s Reason: %s.", sudoStdinBlocked.Message, desc)
		return &d
	}

	// 3. Policy deny (explicit deny rules — blocks even in YOLO mode)
	if d := s.CheckPolicy(command); d != nil && !d.Approved {
		return d
	}

	// 4. YOLO or mode=off bypass
	if s.config.YoloMode || s.IsSessionYolo(sessionKey) || s.config.Mode == "off" {
		return &Decision{Approved: true}
	}

	// 5. Policy allow (explicit allow rules skip regex pattern detection)
	if d := s.CheckPolicy(command); d != nil && d.Approved {
		return d
	}

	// 6. Pattern detection
	isDangerous, patternKey, description := DetectDangerous(command)
	if !isDangerous {
		return &Decision{Approved: true}
	}

	// 7. Allowlist check (session-scoped or permanent)
	if s.IsApproved(sessionKey, patternKey) {
		return &Decision{Approved: true}
	}

	// 8. Smart approval
	if s.config.Mode == "smart" && s.config.SmartApprovalFn != nil {
		result, err := s.config.SmartApprovalFn(ctx, command, description)
		if err != nil {
			s.logger.Debug("approval: smart approval LLM call failed, escalating", "error", err)
		} else {
			switch result {
			case "approve":
				s.ApproveSession(sessionKey, patternKey)
				return &Decision{Approved: true, SmartApproved: true}
			case "deny":
				return &Decision{
					Approved:    false,
					PatternKey:  patternKey,
					Description: description,
					Message:     fmt.Sprintf("BLOCKED by smart approval: %s", description),
				}
			}
			// "escalate" falls through to manual
		}
	}

	// 9. Non-interactive guard: if no user is present to approve, auto-deny
	// instead of hanging. This covers oneshot, cron (CronMode="deny"), and
	// subagent sessions.
	if s.IsNonInteractive() {
		s.logger.Warn("approval: auto-denied in non-interactive mode", "pattern_key", patternKey, "command_preview", truncate(command, 200))
		d := nonInteractiveDenied
		d.PatternKey = patternKey
		d.Description = description
		d.Message = fmt.Sprintf("%s (pattern: %s)", nonInteractiveDenied.Message, description)
		return &d
	}

	// 10. Approval required — caller must prompt the user
	return &Decision{
		Approved:    false,
		PatternKey:  patternKey,
		Description: description,
		Message:     fmt.Sprintf("⚠️ This command is potentially dangerous (%s). Approval required.", description),
	}
}

// HandleUserChoice processes the user's approval decision.
// choice: "once" (approve this once), "session" (approve for session),
//
//	"always" (approve permanently), "deny" (reject)
func (s *System) HandleUserChoice(sessionKey, patternKey, description, choice string) *Decision {
	switch choice {
	case "once":
		return &Decision{Approved: true}
	case "session":
		s.ApproveSession(sessionKey, patternKey)
		s.firePostApproval(ApprovalEvent{PatternKey: patternKey, Description: description, Choice: "session"})
		return &Decision{Approved: true}
	case "always":
		s.ApproveSession(sessionKey, patternKey)
		s.ApprovePermanent(patternKey)
		s.firePostApproval(ApprovalEvent{PatternKey: patternKey, Description: description, Choice: "always"})
		return &Decision{Approved: true}
	case "deny":
		s.firePostApproval(ApprovalEvent{PatternKey: patternKey, Description: description, Choice: "deny"})
		return &deniedByUser
	default:
		// timeout or unknown — deny
		return &deniedByUser
	}
}

// FirePreApproval fires the pre-approval hook with the given command context.
func (s *System) FirePreApproval(command, patternKey, description string) {
	s.firePreApproval(ApprovalEvent{
		Command:     command,
		PatternKey:  patternKey,
		Description: description,
	})
}

// FirePostApproval fires the post-approval hook.
func (s *System) FirePostApproval(command, patternKey, description, choice string) {
	s.firePostApproval(ApprovalEvent{
		Command:     command,
		PatternKey:  patternKey,
		Description: description,
		Choice:      choice,
	})
}

func truncate(s string, maxLen int) string {
	if maxLen < 3 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-3]) + "..."
}
