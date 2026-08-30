// Package tools — Subagent Runner
//
// Wraps SpawnRunner with production-grade subagent execution features:
//
//  1. Per-child timeout with diagnostic dump
//  2. Parent heartbeat during long-running children
//  3. Stale detection — kill children that stop making progress
//  4. Nested orchestration — orchestrator children can further delegate
//  5. Depth control — max_spawn_depth limits nesting
//  6. Progress callbacks — relay child activity to parent display
//  7. Subagent interrupt — context cancellation propagates to children
//
// All features are opt-in; zero-config fallback matches existing SpawnRunner
// behaviour exactly.

package subagent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covonaut/agentcore"
)

// truncateRunes truncates s to at most maxRunes Unicode code points. If
// truncation occurs, "…" is appended. It is rune-aware, so it never splits a
// multi-byte UTF-8 sequence (important for CJK text, where each character is
// 3 bytes) and always produces valid UTF-8.
//
// If maxRunes <= 0, the empty string is returned.
func truncateRunes(s string, maxRunes int) string {
	return truncateRunesWithSuffix(s, maxRunes, "…")
}

// truncateRunesWithSuffix is the rune-aware variant of truncateRunes that
// appends an arbitrary suffix (instead of the default ellipsis) when
// truncation occurs. Like truncateRunes it never splits a multi-byte UTF-8
// sequence and always produces valid UTF-8. If maxRunes <= 0, the empty
// string is returned.
func truncateRunesWithSuffix(s string, maxRunes int, suffix string) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + suffix
}

// ---------------------------------------------------------------------------
// Context keys — thread-safe way to pass metadata through opaque SpawnRunner
// ---------------------------------------------------------------------------

type ctxKeySubagentProgress struct{}
type ctxKeySubagentDepth struct{}
type ctxKeySubagentOrchestrator struct{}
type ctxKeySubagentGoal struct{}
type ctxKeyProviderOverride struct{}
type ctxKeyModelOverride struct{}
type ctxKeyIsolation struct{}
type ctxSubagentCredentialPool struct{}
type ctxKeyParentMessages struct{}

// SubagentProgressCallback receives lifecycle events from child execution.
// eventType is one of: "start", "thinking", "tool", "complete", "timeout", "error".
type SubagentProgressCallback func(event SubagentProgressEvent)

// SubagentProgressEvent carries structured progress data.
type SubagentProgressEvent struct {
	Type       string        `json:"type"`
	ChildIndex int           `json:"child_index"`
	ChildCount int           `json:"child_count"`
	Goal       string        `json:"goal,omitempty"`
	Phase      string        `json:"phase,omitempty"`
	ToolName   string        `json:"tool_name,omitempty"`
	Preview    string        `json:"preview,omitempty"`
	Duration   time.Duration `json:"duration_ms"`
	Error      string        `json:"error,omitempty"`
}

// TimeoutPhase is a context key that tracks which execution phase
// a timed-out operation was in when the deadline fired.
type TimeoutPhase struct {
	Phase string
	mu    sync.RWMutex
}

// SetPhase updates the current execution phase.
func (tp *TimeoutPhase) SetPhase(phase string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.Phase = phase
}

// GetPhase returns the current execution phase.
func (tp *TimeoutPhase) GetPhase() string {
	tp.mu.RLock()
	defer tp.mu.RUnlock()
	return tp.Phase
}

type ctxKeyTimeoutPhase struct{}

// WithTimeoutPhase attaches a phase tracker to the context.
func WithTimeoutPhase(ctx context.Context, tp *TimeoutPhase) context.Context {
	return context.WithValue(ctx, ctxKeyTimeoutPhase{}, tp)
}

// TimeoutPhaseFromContext retrieves the phase tracker, if any.
func TimeoutPhaseFromContext(ctx context.Context) *TimeoutPhase {
	tp, _ := ctx.Value(ctxKeyTimeoutPhase{}).(*TimeoutPhase)
	return tp
}

// WithSubagentProgress attaches a progress callback to the context.
func WithSubagentProgress(ctx context.Context, cb SubagentProgressCallback) context.Context {
	return context.WithValue(ctx, ctxKeySubagentProgress{}, cb)
}

// SubagentProgressFromContext retrieves the callback, if any.
func SubagentProgressFromContext(ctx context.Context) SubagentProgressCallback {
	cb, _ := ctx.Value(ctxKeySubagentProgress{}).(SubagentProgressCallback)
	return cb
}

// WithSubagentDepth attaches spawn depth to the context.
func WithSubagentDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, ctxKeySubagentDepth{}, depth)
}

// SubagentDepthFromContext retrieves the current spawn depth (0 = root).
func SubagentDepthFromContext(ctx context.Context) int {
	d, _ := ctx.Value(ctxKeySubagentDepth{}).(int)
	return d
}

// WithSubagentOrchestrator marks the context as belonging to an orchestrator
// child that is allowed to further delegate.
func WithSubagentOrchestrator(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeySubagentOrchestrator{}, true)
}

// IsSubagentOrchestrator checks whether this child can spawn grandchildren.
func IsSubagentOrchestrator(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeySubagentOrchestrator{}).(bool)
	return v
}

// WithSubagentGoal attaches a human-readable goal to the context.
func WithSubagentGoal(ctx context.Context, goal string) context.Context {
	return context.WithValue(ctx, ctxKeySubagentGoal{}, goal)
}

// SubagentGoalFromContext retrieves the goal, if set.
func SubagentGoalFromContext(ctx context.Context) string {
	s, _ := ctx.Value(ctxKeySubagentGoal{}).(string)
	return s
}

// WithSubagentProvider overrides the LLM provider for the child agent.
func WithSubagentProvider(ctx context.Context, provider string) context.Context {
	return context.WithValue(ctx, ctxKeyProviderOverride{}, provider)
}

// SubagentProviderFromContext retrieves the provider override, if set.
func SubagentProviderFromContext(ctx context.Context) string {
	s, _ := ctx.Value(ctxKeyProviderOverride{}).(string)
	return s
}

// WithSubagentModel overrides the model for the child agent.
func WithSubagentModel(ctx context.Context, model string) context.Context {
	return context.WithValue(ctx, ctxKeyModelOverride{}, model)
}

// SubagentModelFromContext retrieves the model override, if set.
func SubagentModelFromContext(ctx context.Context) string {
	s, _ := ctx.Value(ctxKeyModelOverride{}).(string)
	return s
}

// WithSubagentIsolation requests workspace isolation for the child agent.
// "worktree" creates a detached git worktree; anything else is shared.
func WithSubagentIsolation(ctx context.Context, isolation string) context.Context {
	return context.WithValue(ctx, ctxKeyIsolation{}, isolation)
}

// SubagentIsolationFromContext retrieves the isolation mode, if set.
func SubagentIsolationFromContext(ctx context.Context) string {
	s, _ := ctx.Value(ctxKeyIsolation{}).(string)
	return s
}

// CredentialPool is the minimal interface for credential failover.
type CredentialPool interface {
	Next() (string, error)
}

// WithSubagentCredentialPool attaches a credential pool to the context for child agent use.
func WithSubagentCredentialPool(ctx context.Context, pool CredentialPool) context.Context {
	return context.WithValue(ctx, ctxSubagentCredentialPool{}, pool)
}

// SubagentCredentialPoolFromContext retrieves the credential pool, if set.
func SubagentCredentialPoolFromContext(ctx context.Context) CredentialPool {
	p, _ := ctx.Value(ctxSubagentCredentialPool{}).(CredentialPool)
	return p
}

// WithParentMessages attaches the parent agent's conversation messages to the
// context, enabling "state" and "full" context modes for spawned children.
// In "state" mode, a compact summary is generated and prepended to the task.
// In "full" mode, the messages are injected into the child agent's initial state.
func WithParentMessages(ctx context.Context, msgs []agentcore.Message) context.Context {
	return context.WithValue(ctx, ctxKeyParentMessages{}, msgs)
}

// ParentMessagesFromContext retrieves the parent's conversation messages, if set.
func ParentMessagesFromContext(ctx context.Context) []agentcore.Message {
	msgs, _ := ctx.Value(ctxKeyParentMessages{}).([]agentcore.Message)
	return msgs
}

// SummarizeParentState generates a compact checkpoint summary from the
// parent agent's conversation messages. This is used by the "state" context
// mode to give the child agent enough context to continue work without
// inheriting the full (and potentially large) conversation history.
//
// The summary includes:
//   - The last few user messages (what was asked)
//   - The last assistant response (what was done)
//   - A list of tools that were called (what actions were taken)
//   - Total message count and compaction count for context
func SummarizeParentState(msgs []agentcore.Message) string {
	if len(msgs) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("[PARENT CONTEXT SUMMARY — for background reference only]\n")
	b.WriteString(fmt.Sprintf("Parent conversation: %d messages.\n\n", len(msgs)))

	// Collect last N user messages and last assistant response.
	const maxUserMsgs = 3
	var userMsgs []string
	var lastAssistant string
	var toolCalls []string

	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == agentcore.RoleUser && m.Type == agentcore.MessageTypeStandard && len(userMsgs) < maxUserMsgs {
			content := truncateRunes(strings.TrimSpace(m.Content), 200)
			if content != "" {
				userMsgs = append([]string{content}, userMsgs...)
			}
		}
		if m.Role == agentcore.RoleAssistant && lastAssistant == "" {
			lastAssistant = truncateRunes(strings.TrimSpace(m.Content), 500)
		}
		// Collect tool call names.
		for _, tc := range m.ToolCalls {
			toolCalls = append(toolCalls, tc.Name)
		}
	}

	if len(userMsgs) > 0 {
		b.WriteString("## Recent User Requests\n")
		for i, msg := range userMsgs {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, msg))
		}
		b.WriteString("\n")
	}

	if lastAssistant != "" {
		b.WriteString("## Last Assistant Action\n")
		b.WriteString(lastAssistant + "\n\n")
	}

	if len(toolCalls) > 0 {
		// Deduplicate and show last 10 tool calls.
		seen := make(map[string]bool)
		var unique []string
		for i := len(toolCalls) - 1; i >= 0 && len(unique) < 10; i-- {
			if !seen[toolCalls[i]] {
				seen[toolCalls[i]] = true
				unique = append([]string{toolCalls[i]}, unique...)
			}
		}
		b.WriteString("## Tools Used\n")
		b.WriteString(strings.Join(unique, ", ") + "\n")
	}

	b.WriteString("\n[END PARENT CONTEXT SUMMARY]")
	return b.String()
}

// ---------------------------------------------------------------------------
// SubagentRunner
// ---------------------------------------------------------------------------

// SubagentRunner wraps a SpawnRunner with timeout, heartbeat, stale detection,
// depth tracking, and progress callbacks.
type SubagentRunner struct {
	cfg      SubagentRunnerConfig
	registry *SubagentRegistry
	mu       sync.Mutex
}

// SetRegistry attaches a SubagentRegistry for tracking spawned subagents.
func (sr *SubagentRunner) SetRegistry(reg *SubagentRegistry) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.registry = reg
}

// SubagentRunnerConfig configures the runner.
type SubagentRunnerConfig struct {
	// DefaultTimeout is the per-child wall-clock timeout. 0 = no timeout.
	DefaultTimeout time.Duration

	// HeartbeatInterval is how often the parent heartbeat fires. 0 = no heartbeat.
	HeartbeatInterval time.Duration

	// HeartbeatFn is called periodically during child execution. Use this to
	// keep a gateway session alive during long-running children.
	HeartbeatFn func()

	// ProgressCallback receives structured events for TUI / SSE relay.
	ProgressCallback SubagentProgressCallback

	// MaxSpawnDepth limits nesting. 0 = flat (children cannot spawn grandchildren).
	// Default: 0 (flat).
	MaxSpawnDepth int

	// StaleTimeout is the wall-clock duration after which a child with no
	// observable progress is considered stuck and its context is cancelled.
	// 0 = no stale detection.
	StaleTimeout time.Duration

	// HomeDir is the agent home directory, used for diagnostic dump files.
	HomeDir string

	// Logger for diagnostic output.
	Logger *slog.Logger

	// PreStopHook is called after each subagent run completes (no error). If it
	// returns ForceRerun=true, the subagent runs again with Feedback appended
	// to the task. Reruns are capped at MaxReruns (default 3). This lets a
	// plugin verify the subagent's work and force additional passes if the
	// output is incomplete or low-quality.
	PreStopHook SubagentPreStopHook

	// PostStopHook is called after the subagent is fully done (all reruns
	// exhausted or PreStopHook satisfied). It is observational only — its
	// return value is ignored.
	PostStopHook SubagentPostStopHook

	// TruthChecker verifies that a subagent's self-reported success is backed
	// by completed work (e.g. all kanban tasks done, all TODOs resolved). If
	// it downgrades the status, the output is annotated and the result status
	// changes from "completed" to "partial" or "failure".
	TruthChecker SubagentTruthChecker

	// MaxReruns caps the number of PreStopHook-forced reruns. 0 disables reruns
	// entirely (PreStopHook is still called once but cannot force a rerun).
	// Callers that want reruns must set this explicitly. A negative value is
	// treated as 0 (disabled).
	MaxReruns int
}

// SubagentVerificationResult carries the outcome of a single subagent run,
// passed to PreStopHook and TruthChecker for inspection.
type SubagentVerificationResult struct {
	Output     string // The subagent's output text.
	Task       string // The original task prompt.
	Goal       string // Human-readable goal for diagnostics.
	RerunCount int    // How many reruns have occurred so far (0 = first run).
}

// SubagentPreStopDecision is the return value of PreStopHook.
type SubagentPreStopDecision struct {
	// ForceRerun triggers another subagent run with Feedback appended to the
	// task. The rerun count is checked against MaxReruns.
	ForceRerun bool
	// Feedback is appended to the task prompt for the next run. Should describe
	// what was incomplete or wrong with the previous output.
	Feedback string
	// Reason is logged for diagnostics.
	Reason string
}

// SubagentPreStopHook is called after each subagent run completes successfully.
// If it returns ForceRerun=true, the subagent runs again with Feedback appended.
type SubagentPreStopHook func(ctx context.Context, result SubagentVerificationResult) SubagentPreStopDecision

// SubagentPostStopHook is called after the subagent is fully done (all reruns
// exhausted or PreStopHook satisfied). Observational only.
type SubagentPostStopHook func(ctx context.Context, result SubagentVerificationResult)

// SubagentTruthChecker verifies that a subagent's self-reported success is
// backed by completed work. Returns a verified status and optional reason.
//
// Status values:
//   - "success"  — work verified, no downgrade
//   - "partial"  — some work incomplete, downgraded from success
//   - "failure"  — work fundamentally incomplete
//
// If the status is downgraded, reason is included in the output annotation.
type SubagentTruthChecker func(ctx context.Context, output, task string) (status, reason string)

// VerifiedStatus constants for TruthChecker return values.
const (
	StatusSuccess = "success"
	StatusPartial = "partial"
	StatusFailure = "failure"
)

// NewSubagentRunner creates a new runner with the given config.
func NewSubagentRunner(cfg SubagentRunnerConfig) *SubagentRunner {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))
	}
	if cfg.HeartbeatInterval == 0 && cfg.HeartbeatFn != nil {
		cfg.HeartbeatInterval = 30 * time.Second
	}
	if cfg.StaleTimeout == 0 && cfg.HeartbeatFn != nil {
		cfg.StaleTimeout = 15 * cfg.HeartbeatInterval // 15 idle cycles
	}
	// MaxReruns == 0 means "reruns disabled" (PreStopHook is still called once
	// but cannot force a rerun). Do not override it here — see Run.
	return &SubagentRunner{cfg: cfg}
}

// RunOptions control per-call behaviour.
type SubagentRunOptions struct {
	Timeout      time.Duration // Override default timeout.
	Goal         string        // Human-readable goal for diagnostics and progress.
	ChildIndex   int           // 0-based index in a batch.
	ChildCount   int           // Total batch size.
	Orchestrator bool          // Whether the child can further delegate.
	Depth        int           // Current spawn depth.
	MaxTurns     int           // Passed through to SpawnRunner.
}

// Run executes a single subagent task through the wrapped SpawnRunner.
func (sr *SubagentRunner) Run(ctx context.Context, spawn SpawnRunner, task string, toolsets []string, opts SubagentRunOptions) (output string, err error) {
	// --- Registry tracking with cancellable context ---
	var subID string
	if sr.registry != nil {
		ctx, subID = sr.registry.StartWithCancel(ctx, opts.Goal, opts.Depth)

		// Store subagent run in context for send_input message delivery
		if run, ok := sr.registry.Get(subID); ok && run.inputChan != nil {
			ctx = WithSubagentInput(ctx, run)
		}
	}
	defer func() {
		if sr.registry != nil && subID != "" {
			sr.registry.Complete(subID, err != nil)
		}
	}()

	// --- Timeout ---
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = sr.cfg.DefaultTimeout
	}
	tp := &TimeoutPhase{Phase: "init"}
	ctx = WithTimeoutPhase(ctx, tp)
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// --- Depth + orchestrator ---
	if opts.Depth > 0 {
		ctx = WithSubagentDepth(ctx, opts.Depth)
	}
	if opts.Orchestrator {
		ctx = WithSubagentOrchestrator(ctx)
	}
	if opts.Goal != "" {
		ctx = WithSubagentGoal(ctx, opts.Goal)
	}

	// --- Progress ---
	cb := sr.cfg.ProgressCallback
	startTime := time.Now()

	emit := func(typ, tool, preview string, errStr string) {
		if cb == nil {
			return
		}
		cb(SubagentProgressEvent{
			Type:       typ,
			ChildIndex: opts.ChildIndex,
			ChildCount: max(opts.ChildCount, 1),
			Goal:       opts.Goal,
			Phase:      tp.GetPhase(),
			ToolName:   tool,
			Preview:    preview,
			Duration:   time.Since(startTime),
			Error:      errStr,
		})
	}

	emit("start", "", task, "")
	defer func() {
		if err != nil {
			emit("error", "", "", err.Error())
		} else {
			emit("complete", "", "", "")
		}
	}()

	// --- Heartbeat ---
	var hbStop chan struct{}
	if sr.cfg.HeartbeatFn != nil && sr.cfg.HeartbeatInterval > 0 {
		hbStop = make(chan struct{})
		go sr.runHeartbeat(opts.Goal, subID, hbStop)
		defer func() { close(hbStop) }()
	}

	// --- Stale detection ---
	var staleStop chan struct{}
	var staleFired chan struct{}
	if sr.cfg.StaleTimeout > 0 {
		staleStop = make(chan struct{})
		staleFired = make(chan struct{})
		go sr.detectStale(ctx, opts.Goal, startTime, staleStop, staleFired)
		defer close(staleStop)
	}

	// --- Run ---
	done := make(chan struct{})
	var runErr error
	var runOutput string

	tp.SetPhase("running")
	safego.SafeGo(func() {
		defer close(done)
		runOutput, runErr = spawn(ctx, task, toolsets, opts.MaxTurns)
	}, nil)

	// Wait for completion, timeout, or stale detection
	select {
	case <-done:
		output = runOutput
		err = runErr
	case <-staleFired:
		output = ""
		err = fmt.Errorf("subagent timed out after %v (stale — no progress)", time.Since(startTime).Round(time.Second))
		sr.writeDiagnostic(opts.Goal, task, toolsets, startTime)
	case <-ctx.Done():
		output = ""
		phase := tp.GetPhase()
		if ctx.Err() == context.DeadlineExceeded {
			err = fmt.Errorf("subagent timed out after %v (phase: %s): %w", time.Since(startTime).Round(time.Second), phase, ctx.Err())
			sr.writeDiagnostic(opts.Goal, task, toolsets, startTime)
		} else {
			err = fmt.Errorf("subagent cancelled after %v (phase: %s): %w", time.Since(startTime).Round(time.Second), phase, ctx.Err())
		}
	}

	// --- PreStop hook + rerun loop ---
	// If the initial run succeeded and a PreStopHook is configured, the hook
	// can force additional runs with feedback. Reruns use a lighter execution
	// path (same ctx timeout, no separate heartbeat/stale). Reruns are capped
	// at MaxReruns.
	//
	// rerunCount is declared at function scope so it stays in scope for the
	// PostStopHook call below, which must receive the actual number of reruns
	// that occurred (not always 0).
	var rerunCount int
	if err == nil && output != "" && sr.cfg.PreStopHook != nil {
		currentTask := task
		maxReruns := sr.cfg.MaxReruns
		// 0 (and any negative sentinel) disables reruns entirely: the
		// PreStopHook is still invoked once below, but it cannot force a rerun
		// because rerunCount(0) >= maxReruns(0) is already true.
		if maxReruns < 0 {
			maxReruns = 0
		}

		for rerunCount <= maxReruns {
			decision := sr.cfg.PreStopHook(ctx, SubagentVerificationResult{
				Output:     output,
				Task:       task,
				Goal:       opts.Goal,
				RerunCount: rerunCount,
			})
			if !decision.ForceRerun {
				break
			}
			if rerunCount >= maxReruns {
				sr.cfg.Logger.Warn("subagent preStop: max reruns exhausted",
					"reruns", rerunCount, "reason", decision.Reason)
				break
			}

			rerunCount++
			sr.cfg.Logger.Info("subagent preStop: forcing rerun",
				"rerun", rerunCount, "reason", decision.Reason)

			// Append feedback to task for the next run.
			rerunTask := currentTask + "\n\n---\nFeedback from verification (rerun " +
				fmt.Sprintf("%d/%d", rerunCount, maxReruns) + "):\n" + decision.Feedback +
				"\n---\nPlease address the feedback and provide a complete result."

			rerunOutput, rerunErr := sr.runOnce(ctx, spawn, rerunTask, toolsets, opts.MaxTurns)
			if rerunErr != nil {
				// Rerun failed — keep the original output but log the failure.
				sr.cfg.Logger.Warn("subagent preStop: rerun failed, keeping original output",
					"rerun", rerunCount, "error", rerunErr)
				break
			}
			output = rerunOutput
			currentTask = rerunTask
		}
	}

	// --- TruthChecker (DB truth degradation) ---
	// If the subagent self-reported success but the truth checker finds
	// incomplete work, downgrade the status and annotate the output.
	if err == nil && output != "" && sr.cfg.TruthChecker != nil {
		status, reason := sr.cfg.TruthChecker(ctx, output, task)
		switch status {
		case StatusSuccess:
			// Verified — no downgrade.
		case StatusPartial:
			sr.cfg.Logger.Warn("subagent truth check: downgraded to partial",
				"reason", reason, "goal", opts.Goal)
			output = fmt.Sprintf("[⚠️ PARTIAL — %s]\n\n%s", reason, output)
		case StatusFailure:
			sr.cfg.Logger.Warn("subagent truth check: downgraded to failure",
				"reason", reason, "goal", opts.Goal)
			output = fmt.Sprintf("[❌ FAILED — %s]\n\n%s", reason, output)
		default:
			// Unknown status from a buggy/custom TruthChecker — do not silently
			// ignore; treat as failure so the caller is aware something is off.
			sr.cfg.Logger.Warn("subagent truth check: unknown status, treating as failure",
				"status", status, "reason", reason, "goal", opts.Goal)
			reasonMsg := reason
			if reasonMsg == "" {
				reasonMsg = fmt.Sprintf("unknown truth-check status %q", status)
			}
			output = fmt.Sprintf("[❌ FAILED — %s]\n\n%s", reasonMsg, output)
		}
	}

	// --- PostStop hook (observational only) ---
	if sr.cfg.PostStopHook != nil {
		sr.cfg.PostStopHook(ctx, SubagentVerificationResult{
			Output:     output,
			Task:       task,
			Goal:       opts.Goal,
			RerunCount: rerunCount,
		})
	}

	return output, err
}

// runOnce executes a single spawn call and waits for completion or context
// cancellation. Used for PreStopHook-forced reruns (lighter than the full
// Run path — no separate heartbeat/stale detection).
func (sr *SubagentRunner) runOnce(ctx context.Context, spawn SpawnRunner, task string, toolsets []string, maxTurns int) (string, error) {
	done := make(chan struct{})
	var runOutput string
	var runErr error

	safego.SafeGo(func() {
		defer close(done)
		runOutput, runErr = spawn(ctx, task, toolsets, maxTurns)
	}, nil)

	select {
	case <-done:
		return runOutput, runErr
	case <-ctx.Done():
		return "", fmt.Errorf("rerun cancelled: %w", ctx.Err())
	}
}

func (sr *SubagentRunner) runHeartbeat(goal, subID string, stop <-chan struct{}) {
	ticker := time.NewTicker(sr.cfg.HeartbeatInterval)
	defer ticker.Stop()

	label := "subagent"
	if goal != "" {
		label = truncateRunes(goal, 37)
	}

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			sr.cfg.Logger.Debug("delegate heartbeat", "subagent", label)
			if sr.cfg.HeartbeatFn != nil {
				sr.cfg.HeartbeatFn()
			}
			// Refresh persisted heartbeat timestamp for stuck detection
			// and crash recovery diagnostics.
			if subID != "" && sr.registry != nil {
				sr.registry.UpdateHeartbeat(subID)
			}
		}
	}
}

func (sr *SubagentRunner) detectStale(ctx context.Context, goal string, start time.Time, stop <-chan struct{}, fired chan<- struct{}) {
	interval := sr.cfg.HeartbeatInterval
	if interval == 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if time.Since(start) > sr.cfg.StaleTimeout {
				close(fired)
				return
			}
			// Check if parent context is already done
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}
}

// writeDiagnostic writes a timeout diagnostic file to help debug stuck subagents.
func (sr *SubagentRunner) writeDiagnostic(goal, task string, toolsets []string, startTime time.Time) {
	if sr.cfg.HomeDir == "" {
		return
	}

	logsDir := filepath.Join(sr.cfg.HomeDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return
	}

	ts := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("subagent-timeout-%s.log", ts)
	path := filepath.Join(logsDir, filename)

	var buf []byte
	buf = append(buf, "# Subagent Timeout Diagnostic\n"...)
	buf = append(buf, fmt.Sprintf("# Generated: %s\n", time.Now().Format(time.RFC3339))...)
	buf = append(buf, "\n"...)
	buf = append(buf, fmt.Sprintf("Configured timeout: %v\n", sr.cfg.DefaultTimeout)...)
	buf = append(buf, fmt.Sprintf("Actual duration:   %v\n", time.Since(startTime).Round(time.Millisecond))...)
	buf = append(buf, fmt.Sprintf("Stale threshold:   %v\n", sr.cfg.StaleTimeout)...)
	buf = append(buf, "\n## Goal\n"...)

	// Rune-aware truncation so CJK / multi-byte content is not split mid-codepoint.
	trunc := func(s string, n int) string {
		if n <= 0 {
			return ""
		}
		r := []rune(s)
		if len(r) <= n {
			return s
		}
		return string(r[:n]) + "...[truncated]"
	}

	buf = append(buf, trunc(goal, 2000)+"\n"...)
	buf = append(buf, "\n## Task\n"...)
	buf = append(buf, trunc(task, 2000)+"\n"...)
	buf = append(buf, "\n## Toolsets\n"...)
	for _, ts := range toolsets {
		buf = append(buf, fmt.Sprintf("  - %s\n", ts)...)
	}
	buf = append(buf, "\n## Runtime\n"...)
	buf = append(buf, fmt.Sprintf("  GOMAXPROCS: %s\n", os.Getenv("GOMAXPROCS"))...)
	buf = append(buf, fmt.Sprintf("  NumCPU:     %d\n", runtime.NumCPU())...)
	buf = append(buf, fmt.Sprintf("  NumGoroutine: %d\n", runtime.NumGoroutine())...)
	buf = append(buf, "\n## Stack Trace\n"...)
	stack := make([]byte, 8192)
	n := runtime.Stack(stack, true)
	buf = append(buf, stack[:n]...)
	buf = append(buf, "\n"...)
	buf = append(buf, "# Common causes: oversized prompt rejected by provider, transport hang,\n"...)
	buf = append(buf, "# credential resolution stuck, or runaway loop in child agent.\n"...)

	_ = os.WriteFile(path, buf, 0o644)
	sr.cfg.Logger.Warn("subagent timeout diagnostic written", "path", path)
}

// DelegateToolsetsForRole computes child toolsets based on the orchestration role.
// When orchestrator is true, the "delegation" toolset is retained so the child
// can spawn grandchildren (subject to depth limits).
func DelegateToolsetsForRole(parentToolsets []string, childRequested []string, restrictToParent bool, orchestrator bool) []string {
	var result []string

	if restrictToParent {
		parentSet := make(map[string]bool)
		for _, ts := range parentToolsets {
			parentSet[ts] = true
		}
		for _, ts := range childRequested {
			if parentSet[ts] {
				result = append(result, ts)
			}
		}
	} else {
		result = append(result, childRequested...)
	}

	// Strip dangerous toolsets unless orchestrator retains "delegation"
	filtered := make([]string, 0, len(result))
	for _, ts := range result {
		if ts == "delegation" && orchestrator {
			filtered = append(filtered, ts)
		} else if ts != "delegation" && ts != "clarify" {
			filtered = append(filtered, ts)
		}
	}
	return filtered
}
