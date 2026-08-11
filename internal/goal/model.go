package goal

import "time"

// Status represents the goal lifecycle state.
type Status string

const (
	StatusActive        Status = "active"
	StatusPaused        Status = "paused"
	StatusBlocked       Status = "blocked"
	StatusUsageLimited  Status = "usage_limited"
	StatusBudgetLimited Status = "budget_limited"
	StatusComplete      Status = "complete"
)

// IsTerminal returns true for states that cannot transition back to active.
func (s Status) IsTerminal() bool {
	return s == StatusBudgetLimited || s == StatusComplete
}

// IsStopped returns true for non-running states.
func (s Status) IsStopped() bool {
	return s == StatusPaused || s == StatusBlocked || s == StatusUsageLimited || s.IsTerminal()
}

// Goal represents a persisted agent objective. There is exactly one goal per
// session (thread_id is the PRIMARY KEY).
type Goal struct {
	SessionID       string    `json:"session_id"`
	GoalID          string    `json:"goal_id"`
	Objective       string    `json:"objective"`
	Status          Status    `json:"status"`
	TokenBudget     *int64    `json:"token_budget,omitempty"`
	TokensUsed      int64     `json:"tokens_used"`
	TimeUsedSeconds int64     `json:"time_used_seconds"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Update describes a partial goal mutation.
type Update struct {
	Objective      *string // nil = no change
	Status         *Status // nil = no change
	TokenBudget    **int64 // nil = no change; *nil = clear; **non-nil = set
	ExpectedGoalID *string // optimistic concurrency guard
}

// AccountingMode controls which goal statuses can be updated by accounting.
type AccountingMode string

const (
	// AccountingActiveStatusOnly updates only status='active' goals.
	AccountingActiveStatusOnly AccountingMode = "active_status_only"
	// AccountingActiveOnly updates status IN ('active','budget_limited') goals.
	AccountingActiveOnly AccountingMode = "active_only"
	// AccountingActiveOrComplete updates status IN ('active','budget_limited','complete') goals.
	// Used for the completing turn to charge final tokens to a just-completed goal.
	AccountingActiveOrComplete AccountingMode = "active_or_complete"
	// AccountingActiveOrStopped updates any non-complete goal.
	AccountingActiveOrStopped AccountingMode = "active_or_stopped"
)

// AccountingOutcome reports what happened after token/time accounting.
type AccountingOutcome struct {
	BudgetExceeded bool `json:"budget_exceeded"`
	Updated        bool `json:"updated"`
}
