package rollout

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/session/sqlitefs"
	_ "modernc.org/sqlite"
)

// Store persists rollout traces to SQLite for querying, export, and replay.
type Store struct {
	mu  sync.RWMutex
	db  *sql.DB
	dir string
}

// NewStore opens or creates a rollout database at the given directory.
func NewStore(dir string) (*Store, error) {
	dsn := sqlitefs.BuildDSN(dir)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open rollout sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	s := &Store{db: db, dir: dir}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate rollout: %w", err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS rollouts (
			id          TEXT PRIMARY KEY,
			session_id  TEXT NOT NULL,
			provider    TEXT DEFAULT '',
			model       TEXT DEFAULT '',
			cwd         TEXT DEFAULT '',
			started_at  INTEGER NOT NULL,
			ended_at    INTEGER DEFAULT 0,
			turn_count  INTEGER DEFAULT 0,
			config_json TEXT DEFAULT '{}',
			metadata    TEXT DEFAULT '{}',
			turns_json  TEXT DEFAULT '[]',
			parent_id   TEXT DEFAULT '',
			edges_json  TEXT DEFAULT '[]',
			created_at  INTEGER DEFAULT (unixepoch())
		);
	`); err != nil {
		return err
	}

	// Idempotently add columns to databases created before they existed.
	// Missing table/column errors are ignored on purpose.
	_, _ = s.db.Exec(`ALTER TABLE rollouts ADD COLUMN parent_id TEXT DEFAULT ''`)
	_, _ = s.db.Exec(`ALTER TABLE rollouts ADD COLUMN edges_json TEXT DEFAULT '[]'`)

	if _, err := s.db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_rollouts_session ON rollouts(session_id);
		CREATE INDEX IF NOT EXISTS idx_rollouts_model ON rollouts(model);
		CREATE INDEX IF NOT EXISTS idx_rollouts_started ON rollouts(started_at);
		CREATE INDEX IF NOT EXISTS idx_rollouts_parent ON rollouts(parent_id);
	`); err != nil {
		return err
	}
	return nil
}

// Save persists a complete rollout to the database.
func (s *Store) Save(ctx context.Context, r *Rollout) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	turnsJSON, err := json.Marshal(r.Turns)
	if err != nil {
		return fmt.Errorf("marshal turns: %w", err)
	}
	configJSON, err := json.Marshal(r.Config)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	metaJSON, err := json.Marshal(r.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	edgesJSON, err := json.Marshal(r.Edges)
	if err != nil {
		return fmt.Errorf("marshal edges: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO rollouts
			(id, session_id, provider, model, cwd, started_at, ended_at,
			 turn_count, config_json, metadata, turns_json, parent_id, edges_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.SessionID, r.Provider, r.Model, r.CWD,
		r.StartedAt.Unix(), r.EndedAt.Unix(),
		len(r.Turns), string(configJSON), string(metaJSON), string(turnsJSON),
		r.ParentID, string(edgesJSON), time.Now().Unix(),
	)
	return err
}

// Get retrieves a rollout by ID.
func (s *Store) Get(ctx context.Context, id string) (*Rollout, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRowContext(ctx, `
		SELECT id, session_id, provider, model, cwd, started_at, ended_at,
		       turn_count, config_json, metadata, turns_json, parent_id, edges_json
		FROM rollouts WHERE id = ?`, id)

	return s.scanRollout(row)
}

// List returns rollouts matching the filter, most recent first.
func (s *Store) List(ctx context.Context, filter ListFilter) ([]RolloutSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, session_id, provider, model, started_at, ended_at, turn_count, parent_id FROM rollouts WHERE 1=1`
	args := []any{}

	if filter.SessionID != "" {
		query += ` AND session_id = ?`
		args = append(args, filter.SessionID)
	}
	if filter.Model != "" {
		query += ` AND model = ?`
		args = append(args, filter.Model)
	}
	if filter.Since.IsZero() == false {
		query += ` AND started_at >= ?`
		args = append(args, filter.Since.Unix())
	}
	if filter.Before.IsZero() == false {
		query += ` AND started_at <= ?`
		args = append(args, filter.Before.Unix())
	}

	query += ` ORDER BY started_at DESC`
	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
	}
	if filter.Offset > 0 {
		query += ` OFFSET ?`
		args = append(args, filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []RolloutSummary
	for rows.Next() {
		var r RolloutSummary
		var startedAt, endedAt int64
		if err := rows.Scan(&r.ID, &r.SessionID, &r.Provider, &r.Model,
			&startedAt, &endedAt, &r.TurnCount, &r.ParentID); err != nil {
			return nil, err
		}
		r.StartedAt = time.Unix(startedAt, 0)
		r.EndedAt = time.Unix(endedAt, 0)
		results = append(results, r)
	}
	return results, rows.Err()
}

// Descendants returns all rollouts that were spawned (directly or transitively)
// by the given rollout, via parent_id links. The result is unordered.
func (s *Store) Descendants(ctx context.Context, parentID string) ([]*Rollout, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx, `SELECT id FROM rollouts WHERE parent_id = ?`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []*Rollout
	for _, id := range ids {
		r, err := s.Get(ctx, id)
		if err != nil {
			continue
		}
		out = append(out, r)
		grand, err := s.Descendants(ctx, id)
		if err != nil {
			continue
		}
		out = append(out, grand...)
	}
	return out, nil
}

// Delete removes a rollout by ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM rollouts WHERE id = ?`, id)
	return err
}

// Count returns the total number of stored rollouts.
func (s *Store) Count(ctx context.Context) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rollouts`).Scan(&count)
	return count, err
}

// ListFilter defines filters for listing rollouts.
type ListFilter struct {
	SessionID string
	Model     string
	Since     time.Time
	Before    time.Time
	Limit     int
	Offset    int
}

// RolloutSummary is a lightweight view of a rollout for listing.
type RolloutSummary struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	ParentID  string    `json:"parent_id,omitempty"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	TurnCount int       `json:"turn_count"`
}

func (s *Store) scanRollout(row *sql.Row) (*Rollout, error) {
	var r Rollout
	var configJSON, metaJSON, turnsJSON, edgesJSON string
	var startedAt, endedAt int64
	var turnCount int // DB column only, actual count derived from turns slice

	err := row.Scan(&r.ID, &r.SessionID, &r.Provider, &r.Model, &r.CWD,
		&startedAt, &endedAt, &turnCount, &configJSON, &metaJSON, &turnsJSON, &r.ParentID, &edgesJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("rollout not found")
		}
		return nil, err
	}

	r.StartedAt = time.Unix(startedAt, 0)
	r.EndedAt = time.Unix(endedAt, 0)

	if err := json.Unmarshal([]byte(turnsJSON), &r.Turns); err != nil {
		return nil, fmt.Errorf("unmarshal turns: %w", err)
	}
	if err := json.Unmarshal([]byte(configJSON), &r.Config); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if err := json.Unmarshal([]byte(metaJSON), &r.Metadata); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}
	if err := json.Unmarshal([]byte(edgesJSON), &r.Edges); err != nil {
		return nil, fmt.Errorf("unmarshal edges: %w", err)
	}

	return &r, nil
}
