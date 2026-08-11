package goal

import (
	"context"
	"fmt"
)

// Accounting tracks token and time usage against a goal's budget.
type Accounting struct {
	store *Store
}

// NewAccounting creates an accounting tracker backed by the goal store.
func NewAccounting(store *Store) *Accounting {
	return &Accounting{store: store}
}

// RecordUsage applies token and time deltas to the active goal.
// Only uncached input tokens + output tokens count toward the budget.
// Returns the outcome, including whether the budget was exceeded.
func (a *Accounting) RecordUsage(ctx context.Context, sessionID string,
	tokenDelta int64, timeDeltaSeconds int64, mode AccountingMode,
	expectedGoalID *string) (*AccountingOutcome, error) {

	// Separate WHERE filter vs budget-limit CASE WHEN filter.
	// The WHERE determines WHICH goals to update.
	// The CASE WHEN determines WHICH goals can transition to budget_limited.
	// For ActiveOnly mode: we update active+budget_limited goals, but only
	// active goals can transition to budget_limited (already-limited stay as-is).
	whereFilter := modeToSQL(mode)
	budgetFilter := budgetLimitFilter(mode)

	query := fmt.Sprintf(`
		UPDATE goals SET
			tokens_used = tokens_used + ?,
			time_used_seconds = time_used_seconds + ?,
			status = CASE
				WHEN %s AND token_budget IS NOT NULL AND tokens_used + ? >= token_budget
					THEN 'budget_limited'
				ELSE status
			END,
			updated_at = unixepoch()
		WHERE session_id = ? AND %s
	`, budgetFilter, whereFilter)

	args := []any{tokenDelta, timeDeltaSeconds, tokenDelta, sessionID}
	if expectedGoalID != nil {
		query += ` AND goal_id = ?`
		args = append(args, *expectedGoalID)
	}

	res, err := a.store.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("goal accounting: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return &AccountingOutcome{}, nil
	}

	// Check if the budget was exceeded by re-reading the goal.
	g, err := a.store.Get(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("re-read goal after accounting: %w", err)
	}
	if g == nil {
		return &AccountingOutcome{Updated: true}, nil
	}
	return &AccountingOutcome{
		Updated:        true,
		BudgetExceeded: g.Status == StatusBudgetLimited,
	}, nil
}

// modeToSQL returns the WHERE clause status filter.
func modeToSQL(mode AccountingMode) string {
	switch mode {
	case AccountingActiveStatusOnly:
		return `status = 'active'`
	case AccountingActiveOnly:
		return `status IN ('active','budget_limited')`
	case AccountingActiveOrComplete:
		return `status IN ('active','budget_limited','complete')`
	case AccountingActiveOrStopped:
		return `status IN ('active','paused','blocked','usage_limited','budget_limited')`
	default:
		return `status = 'active'`
	}
}

// budgetLimitFilter returns the CASE WHEN status filter for budget-limit transition.
// Narrower than the WHERE filter: only 'active' goals can be promoted to budget_limited
// for ActiveOnly/ActiveOrComplete modes (already budget_limited stays as-is).
func budgetLimitFilter(mode AccountingMode) string {
	switch mode {
	case AccountingActiveStatusOnly, AccountingActiveOnly, AccountingActiveOrComplete:
		return `status = 'active'`
	case AccountingActiveOrStopped:
		return `status IN ('active','paused','blocked','usage_limited','budget_limited')`
	default:
		return `status = 'active'`
	}
}
