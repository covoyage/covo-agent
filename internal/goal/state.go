package goal

import (
	"sync"
	"time"
)

// TokenUsage tracks token counts from provider responses.
type TokenUsage struct {
	InputTokens           int64
	CachedInputTokens     int64
	OutputTokens          int64
	ReasoningOutputTokens int64
	TotalTokens           int64
}

// GoalTokenDelta computes the goal-relevant token delta, excluding cached input
// and reasoning output.
func GoalTokenDelta(usage *TokenUsage) int64 {
	if usage == nil {
		return 0
	}
	delta := usage.InputTokens - usage.CachedInputTokens
	if delta < 0 {
		delta = 0
	}
	out := usage.OutputTokens
	if out > 0 {
		delta += out
	}
	return delta
}

// AccountingState is an in-memory layer that tracks per-turn token deltas,
// wall-clock time, budget-limit dedup, and active goal markers —
// independent from the persistent store.
//
// This enables:
//   - Real-time token accounting from provider TokenUsage (not rough estimates)
//   - Per-tool-batch progress accounting (budget enforcement mid-turn)
//   - Turn lifecycle integration (start_turn / finish_turn)
//   - Wall-clock time tracking between accounting events
//   - Budget-limit notification deduplication
type AccountingState struct {
	mu sync.Mutex

	currentTurnID string

	// Per-turn tracking: turnID → turnState
	turns map[string]*turnAccounting

	// Wall-clock time tracking
	wallClock wallClockAccounting

	// Prevents duplicate budget-limit reports for the same goal version
	budgetLimitReportedGoalID string

	// Plan mode turns don't count tokens
	planMode bool

	// Serializes progress accounting
	progressLock chan struct{}

	// Tracks injected steering to deduplicate per-run
	injectedSteering map[string]int
}

type turnAccounting struct {
	currentTokenUsage       TokenUsage
	lastAccountedTokenUsage TokenUsage
	activeGoalID            string
}

type wallClockAccounting struct {
	lastAccountedAt time.Time
	activeGoalID    string
}

// BudgetLimitDisposition controls behavior when budget is exceeded.
type BudgetLimitDisposition int

const (
	// BudgetKeepActive allows the current turn to continue after budget limit.
	BudgetKeepActive BudgetLimitDisposition = iota
	// BudgetClearActive clears the active goal marker on budget limit.
	BudgetClearActive
)

// ProgressSnapshot captures the cumulative deltas since last accounting,
// for persistent storage.
type ProgressSnapshot struct {
	TokenDelta       int64
	TimeDeltaSeconds int64
	ExpectedGoalID   string
}

// NewAccountingState creates a new in-memory accounting state.
func NewAccountingState() *AccountingState {
	return &AccountingState{
		turns:            make(map[string]*turnAccounting),
		progressLock:     make(chan struct{}, 1),
		injectedSteering: make(map[string]int),
		wallClock: wallClockAccounting{
			lastAccountedAt: time.Now(),
		},
	}
}

// StartTurn begins tracking a new agent turn. If planMode is true,
// token counting is disabled for this turn.
func (as *AccountingState) StartTurn(turnID string, tokenUsage TokenUsage, planMode bool) {
	as.mu.Lock()
	defer as.mu.Unlock()

	as.currentTurnID = turnID
	as.planMode = planMode

	as.turns[turnID] = &turnAccounting{
		currentTokenUsage:       tokenUsage,
		lastAccountedTokenUsage: tokenUsage,
	}
}

// RecordTokenUsage updates the cumulative token usage for the current turn.
// Returns the delta since last accounting, or nil if no accounting is needed.
func (as *AccountingState) RecordTokenUsage(turnID string, usage TokenUsage) *TokenUsage {
	as.mu.Lock()
	defer as.mu.Unlock()

	turn, ok := as.turns[turnID]
	if !ok {
		return nil
	}
	turn.currentTokenUsage = usage
	delta := TokenUsage{
		InputTokens:           usage.InputTokens - turn.lastAccountedTokenUsage.InputTokens,
		CachedInputTokens:     usage.CachedInputTokens - turn.lastAccountedTokenUsage.CachedInputTokens,
		OutputTokens:          usage.OutputTokens - turn.lastAccountedTokenUsage.OutputTokens,
		ReasoningOutputTokens: usage.ReasoningOutputTokens - turn.lastAccountedTokenUsage.ReasoningOutputTokens,
		TotalTokens:           usage.TotalTokens - turn.lastAccountedTokenUsage.TotalTokens,
	}
	return &delta
}

// MarkGoalActive records that a goal is active for the current turn.
func (as *AccountingState) MarkGoalActive(goalID string) {
	as.mu.Lock()
	defer as.mu.Unlock()

	turn, ok := as.turns[as.currentTurnID]
	if ok {
		turn.activeGoalID = goalID
		// Reset token baseline: tokens consumed before this goal was activated
		// should not be double-counted against this goal.
		turn.lastAccountedTokenUsage = turn.currentTokenUsage
	}
	// Also track on wall clock
	if as.wallClock.activeGoalID != goalID {
		as.wallClock.lastAccountedAt = time.Now()
		as.wallClock.activeGoalID = goalID
	}
}

// ClearActiveGoal removes the active goal marker.
func (as *AccountingState) ClearActiveGoal() {
	as.mu.Lock()
	defer as.mu.Unlock()

	as.wallClock.activeGoalID = ""
	turn, ok := as.turns[as.currentTurnID]
	if ok {
		turn.activeGoalID = ""
	}
}

// MarkGoalIdle records that a goal is active but no turn is running
// (for time tracking across idle periods).
func (as *AccountingState) MarkGoalIdle(goalID string) {
	as.mu.Lock()
	defer as.mu.Unlock()

	if as.wallClock.activeGoalID != goalID {
		as.wallClock.lastAccountedAt = time.Now()
		as.wallClock.activeGoalID = goalID
	}
}

// Snapshot atomically captures the current progress deltas for persistent accounting.
// This is READ-ONLY — it does NOT advance the baseline. Call MarkProgressAccounted
// AFTER the DB write succeeds to move the baseline forward.
// Returns nil if no turn is active or token counting is disabled.
func (as *AccountingState) Snapshot(turnID string) *ProgressSnapshot {
	as.mu.Lock()
	defer as.mu.Unlock()

	if as.planMode {
		return nil
	}

	turn, ok := as.turns[turnID]
	if !ok {
		return nil
	}

	usageDelta := TokenUsage{
		InputTokens:           turn.currentTokenUsage.InputTokens - turn.lastAccountedTokenUsage.InputTokens,
		CachedInputTokens:     turn.currentTokenUsage.CachedInputTokens - turn.lastAccountedTokenUsage.CachedInputTokens,
		OutputTokens:          turn.currentTokenUsage.OutputTokens - turn.lastAccountedTokenUsage.OutputTokens,
		ReasoningOutputTokens: turn.currentTokenUsage.ReasoningOutputTokens - turn.lastAccountedTokenUsage.ReasoningOutputTokens,
		TotalTokens:           turn.currentTokenUsage.TotalTokens - turn.lastAccountedTokenUsage.TotalTokens,
	}
	tokenDelta := GoalTokenDelta(&usageDelta)

	// Read-only time delta — don't advance clock yet
	timeDelta := int64(time.Since(as.wallClock.lastAccountedAt).Seconds())

	return &ProgressSnapshot{
		TokenDelta:       tokenDelta,
		TimeDeltaSeconds: timeDelta,
		ExpectedGoalID:   turn.activeGoalID,
	}
}

// MarkProgressAccounted advances the baseline and wall clock AFTER a successful
// persistent DB write. Separated from Snapshot so tokens aren't lost on DB failure.
func (as *AccountingState) MarkProgressAccounted(turnID string, accountedSeconds int64) {
	as.mu.Lock()
	defer as.mu.Unlock()

	turn, ok := as.turns[turnID]
	if ok {
		turn.lastAccountedTokenUsage = turn.currentTokenUsage
	}

	as.wallClock.lastAccountedAt = as.wallClock.lastAccountedAt.Add(
		time.Duration(accountedSeconds) * time.Second,
	)
}

// FinishTurn cleans up turn tracking state.
func (as *AccountingState) FinishTurn(turnID string) {
	as.mu.Lock()
	defer as.mu.Unlock()

	delete(as.turns, turnID)
	if as.currentTurnID == turnID {
		as.currentTurnID = ""
	}
}

// ActiveGoalID returns the goal ID active for the current turn.
func (as *AccountingState) ActiveGoalID() string {
	as.mu.Lock()
	defer as.mu.Unlock()

	turn, ok := as.turns[as.currentTurnID]
	if !ok || turn.activeGoalID == "" {
		return as.wallClock.activeGoalID
	}
	return turn.activeGoalID
}

// BudgetLimitReported checks if the budget limit has already been reported
// for the given goal version. If not, records it and returns true (go ahead).
func (as *AccountingState) BudgetLimitReported(goalID string) bool {
	as.mu.Lock()
	defer as.mu.Unlock()

	if as.budgetLimitReportedGoalID == goalID {
		return false // already reported, skip
	}
	as.budgetLimitReportedGoalID = goalID
	return true // first time, go ahead
}

// ClearBudgetLimitReported clears the dedup marker (on goal reset).
func (as *AccountingState) ClearBudgetLimitReported() {
	as.mu.Lock()
	as.budgetLimitReportedGoalID = ""
	as.mu.Unlock()
}

// ShouldClearActiveGoal determines if the active goal should be cleared
// based on its status and the current disposition.
func ShouldClearActiveGoal(status Status, disp BudgetLimitDisposition) bool {
	switch status {
	case StatusActive:
		return false // NEVER clear active
	case StatusBudgetLimited:
		return disp == BudgetClearActive
	default: // Paused, Blocked, UsageLimited, Complete
		return true // ALWAYS clear
	}
}

// PlanMode returns whether the current turn is in plan mode (tokens not counted).
func (as *AccountingState) PlanMode() bool {
	as.mu.Lock()
	defer as.mu.Unlock()
	return as.planMode
}

// TurnIsCurrentActiveGoal returns true if the given turn's active goal matches.
func (as *AccountingState) TurnIsCurrentActiveGoal(turnID, goalID string) bool {
	as.mu.Lock()
	defer as.mu.Unlock()
	turn, ok := as.turns[turnID]
	return ok && turn.activeGoalID == goalID
}

// IdleSnapshot captures wall-clock progress when no turn is active.
func (as *AccountingState) IdleSnapshot() *ProgressSnapshot {
	as.mu.Lock()
	defer as.mu.Unlock()
	elapsed := int64(time.Since(as.wallClock.lastAccountedAt).Seconds())
	if elapsed <= 0 {
		return nil
	}
	return &ProgressSnapshot{
		TimeDeltaSeconds: elapsed,
		ExpectedGoalID:   as.wallClock.activeGoalID,
	}
}

// MarkIdleProgressAccounted advances wall clock after persistent idle accounting.
func (as *AccountingState) MarkIdleProgressAccounted(accountedSeconds int64) {
	as.mu.Lock()
	defer as.mu.Unlock()
	as.wallClock.lastAccountedAt = as.wallClock.lastAccountedAt.Add(
		time.Duration(accountedSeconds) * time.Second,
	)
}

// SteeringInjected records steering injection for dedup (max 1 per kind per run).
func (as *AccountingState) SteeringInjected(kind string) bool {
	as.mu.Lock()
	defer as.mu.Unlock()
	if as.injectedSteering[kind] > 0 {
		return false
	}
	as.injectedSteering[kind] = 1
	return true
}

// ResetSteering clears steering dedup for a new run.
func (as *AccountingState) ResetSteering() {
	as.mu.Lock()
	defer as.mu.Unlock()
	as.injectedSteering = make(map[string]int)
}
