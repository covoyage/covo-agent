package goal

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Store persists goals to SQLite. Goals survive conversation compaction
// because they live in a separate table, not in the message history.
//
// One goal per session (session_id is PK), with a 6-status state machine
// enforced at the SQL level.
type Store struct {
	db *sql.DB
}

// NewStore creates a goal store backed by the given database connection.
func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("goal migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS goals (
			session_id       TEXT PRIMARY KEY,
			goal_id          TEXT NOT NULL,
			objective        TEXT NOT NULL,
			status           TEXT NOT NULL CHECK(status IN (
				'active','paused','blocked','usage_limited','budget_limited','complete'
			)),
			token_budget     INTEGER,
			tokens_used      INTEGER NOT NULL DEFAULT 0,
			time_used_seconds INTEGER NOT NULL DEFAULT 0,
			created_at       INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at       INTEGER NOT NULL DEFAULT (unixepoch())
		);
	`)
	return err
}

// Put inserts or overwrites the goal for a session. Resets usage counters.
func (s *Store) Put(ctx context.Context, g *Goal) error {
	if g.GoalID == "" {
		g.GoalID = uuid.New().String()
	}
	now := time.Now()
	g.CreatedAt = now
	g.UpdatedAt = now
	g.TokensUsed = 0
	g.TimeUsedSeconds = 0

	// Enforce budget_limited immediately if token_budget <= current usage.
	if g.TokenBudget != nil && g.TokensUsed >= *g.TokenBudget {
		g.Status = StatusBudgetLimited
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO goals (session_id, goal_id, objective, status, token_budget,
			tokens_used, time_used_seconds, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, 0, unixepoch(), unixepoch())
		ON CONFLICT(session_id) DO UPDATE SET
			goal_id = excluded.goal_id,
			objective = excluded.objective,
			status = excluded.status,
			token_budget = excluded.token_budget,
			tokens_used = 0,
			time_used_seconds = 0,
			updated_at = unixepoch()
	`, g.SessionID, g.GoalID, g.Objective, string(g.Status), g.TokenBudget)
	return err
}

// PutIfComplete is like Put but only overwrites an existing goal if its
// status is 'complete'. Returns false if the goal exists and is not complete.
func (s *Store) PutIfComplete(ctx context.Context, g *Goal) (bool, error) {
	if g.GoalID == "" {
		g.GoalID = uuid.New().String()
	}
	now := time.Now()
	g.CreatedAt = now
	g.UpdatedAt = now
	g.TokensUsed = 0
	g.TimeUsedSeconds = 0

	// Enforce budget_limited immediately if token_budget <= current usage.
	if g.TokenBudget != nil && g.TokensUsed >= *g.TokenBudget {
		g.Status = StatusBudgetLimited
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO goals (session_id, goal_id, objective, status, token_budget,
			tokens_used, time_used_seconds, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, 0, unixepoch(), unixepoch())
		ON CONFLICT(session_id) DO UPDATE SET
			goal_id = excluded.goal_id,
			objective = excluded.objective,
			status = excluded.status,
			token_budget = excluded.token_budget,
			tokens_used = 0,
			time_used_seconds = 0,
			updated_at = unixepoch()
		WHERE goals.status = 'complete'
	`, g.SessionID, g.GoalID, g.Objective, string(g.Status), g.TokenBudget)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Get retrieves the goal for a session. Returns nil if no goal exists.
func (s *Store) Get(ctx context.Context, sessionID string) (*Goal, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT session_id, goal_id, objective, status, token_budget,
			tokens_used, time_used_seconds, created_at, updated_at
		FROM goals WHERE session_id = ?
	`, sessionID)

	g := &Goal{}
	var budget sql.NullInt64
	var created, updated int64
	err := row.Scan(&g.SessionID, &g.GoalID, &g.Objective, (*string)(&g.Status),
		&budget, &g.TokensUsed, &g.TimeUsedSeconds, &created, &updated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("goal get: %w", err)
	}
	if budget.Valid {
		g.TokenBudget = &budget.Int64
	}
	g.CreatedAt = time.Unix(created, 0)
	g.UpdatedAt = time.Unix(updated, 0)
	return g, nil
}

// Update applies a partial mutation. Returns the updated goal, or nil if
// no row matched (e.g. optimistic concurrency failure).
//
// SQL-level state machine rules:
//   - budget_limited goals cannot be transitioned to paused/blocked
//   - setting status=active when tokens_used >= token_budget keeps budget_limited
func (s *Store) Update(ctx context.Context, sessionID string, u Update) (*Goal, error) {
	// Build dynamic SQL with CASE-based state machine enforcement.
	query := `
		UPDATE goals SET
			updated_at = unixepoch()
	`
	var args []any

	if u.Objective != nil {
		query += `, objective = ?`
		args = append(args, *u.Objective)
	}

	if u.Status != nil {
		// State machine: budget_limited cannot move to paused/blocked;
		// trying to set active when over budget keeps budget_limited.
		// When token_budget is also being changed, use the new value as a bound
		// parameter (not the column reference) so the check uses the updated budget.
		statusStr := string(*u.Status)
		if u.TokenBudget != nil && *u.TokenBudget != nil {
			// Both status and budget changing: use ? for the new budget
			query += `, status = CASE
				WHEN status = 'budget_limited' AND ? IN ('paused','blocked') THEN status
				WHEN ? = 'active' AND ? IS NOT NULL AND tokens_used >= ? THEN 'budget_limited'
				ELSE ?
			END`
			args = append(args, statusStr, statusStr, *u.TokenBudget, *u.TokenBudget, statusStr)
		} else {
			query += `, status = CASE
				WHEN status = 'budget_limited' AND ? IN ('paused','blocked') THEN status
				WHEN ? = 'active' AND token_budget IS NOT NULL AND tokens_used >= token_budget THEN 'budget_limited'
				ELSE ?
			END`
			args = append(args, statusStr, statusStr, statusStr)
		}
	}

	if u.TokenBudget != nil {
		query += `, token_budget = ?`
		args = append(args, *u.TokenBudget)
		// When only budget changes (no status change), still enforce budget_limited.
		// Use the NEW budget value as a bound parameter.
		if u.Status == nil {
			query += `, status = CASE
				WHEN status = 'active' AND ? IS NOT NULL AND tokens_used >= ? THEN 'budget_limited'
				ELSE status
			END`
			args = append(args, *u.TokenBudget, *u.TokenBudget)
		}
	}

	query += ` WHERE session_id = ?`
	args = append(args, sessionID)

	if u.ExpectedGoalID != nil {
		query += ` AND goal_id = ?`
		args = append(args, *u.ExpectedGoalID)
	}

	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("goal update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, nil
	}
	return s.Get(ctx, sessionID)
}

// Pause transitions an active goal to paused. Returns nil if the goal is not active.
func (s *Store) Pause(ctx context.Context, sessionID string) (*Goal, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE goals SET status = 'paused', updated_at = unixepoch()
		WHERE session_id = ? AND status = 'active'
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("goal pause: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, nil
	}
	return s.Get(ctx, sessionID)
}

// Resume transitions a stopped goal back to active.
// Allowed from: paused, blocked, usage_limited (any non-terminal).
func (s *Store) Resume(ctx context.Context, sessionID string) (*Goal, error) {
	// If over budget, stays budget_limited.
	res, err := s.db.ExecContext(ctx, `
		UPDATE goals SET
			status = CASE
				WHEN token_budget IS NOT NULL AND tokens_used >= token_budget THEN 'budget_limited'
				ELSE 'active'
			END,
			updated_at = unixepoch()
		WHERE session_id = ? AND status IN ('paused','blocked','usage_limited')
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("goal resume: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, nil
	}
	return s.Get(ctx, sessionID)
}

// Complete marks a goal as complete.
func (s *Store) Complete(ctx context.Context, sessionID string) (*Goal, error) {
	_, err := s.db.ExecContext(ctx, `
		UPDATE goals SET status = 'complete', updated_at = unixepoch()
		WHERE session_id = ? AND status NOT IN ('complete','budget_limited')
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("goal complete: %w", err)
	}
	return s.Get(ctx, sessionID)
}

// Block marks a goal as blocked.
func (s *Store) Block(ctx context.Context, sessionID string) (*Goal, error) {
	_, err := s.db.ExecContext(ctx, `
		UPDATE goals SET status = 'blocked', updated_at = unixepoch()
		WHERE session_id = ? AND status IN ('active','paused')
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("goal block: %w", err)
	}
	return s.Get(ctx, sessionID)
}

// UsageLimit transitions a goal to usage_limited (rate limit hit).
// Can transition from active or budget_limited.
func (s *Store) UsageLimit(ctx context.Context, sessionID string) (*Goal, error) {
	_, err := s.db.ExecContext(ctx, `
		UPDATE goals SET status = 'usage_limited', updated_at = unixepoch()
		WHERE session_id = ? AND status IN ('active','budget_limited')
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("goal usage_limit: %w", err)
	}
	return s.Get(ctx, sessionID)
}

// Delete removes and returns the goal for a session.
func (s *Store) Delete(ctx context.Context, sessionID string) (*Goal, error) {
	g, err := s.Get(ctx, sessionID)
	if err != nil || g == nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM goals WHERE session_id = ?`, sessionID)
	if err != nil {
		return nil, err
	}
	return g, nil
}

// ListActive returns all goals that are currently active or paused.
func (s *Store) ListActive(ctx context.Context) ([]Goal, error) {
	return s.query(ctx, `WHERE status IN ('active','paused') ORDER BY created_at DESC`)
}

func (s *Store) query(ctx context.Context, clause string, args ...any) ([]Goal, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, goal_id, objective, status, token_budget,
			tokens_used, time_used_seconds, created_at, updated_at
		FROM goals `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var goals []Goal
	for rows.Next() {
		g := Goal{}
		var budget sql.NullInt64
		var created, updated int64
		if err := rows.Scan(&g.SessionID, &g.GoalID, &g.Objective, (*string)(&g.Status),
			&budget, &g.TokensUsed, &g.TimeUsedSeconds, &created, &updated); err != nil {
			return nil, err
		}
		if budget.Valid {
			g.TokenBudget = &budget.Int64
		}
		g.CreatedAt = time.Unix(created, 0)
		g.UpdatedAt = time.Unix(updated, 0)
		goals = append(goals, g)
	}
	return goals, rows.Err()
}
