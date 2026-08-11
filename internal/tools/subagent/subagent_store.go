package subagent

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// SubagentRecord is the persistent representation of a subagent run.
// Mirrors SubagentRun but with fields needed for crash recovery and
// orphan detection (parent_session_id, last_heartbeat_at, agent_type).
type SubagentRecord struct {
	ID              string    `json:"id"`
	ParentSessionID string    `json:"parent_session_id"`
	Task            string    `json:"task"`
	Status          string    `json:"status"` // running | completed | failed | interrupted | orphaned
	Depth           int       `json:"depth"`
	StartedAt       time.Time `json:"started_at"`
	EndedAt         time.Time `json:"ended_at,omitempty"`
	Error           string    `json:"error,omitempty"`
	AgentType       string    `json:"agent_type,omitempty"`
	LastHeartbeatAt time.Time `json:"last_heartbeat_at,omitempty"`
	PID             int       `json:"pid,omitempty"`
}

// SubagentStore persists subagent records to SQLite, enabling crash recovery
// and orphan detection.
//
// On process restart, Recover() marks all previously-running subagents as
// 'orphaned' so they can be inspected or cleaned up. This prevents lost-track
// of subagents that were running when the parent process died.
type SubagentStore struct {
	mu     sync.Mutex
	db     *sql.DB
	ownsDB bool
}

// NewSubagentStore opens <dir>/subagents.db.
func NewSubagentStore(dir string) (*SubagentStore, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000", dir+"/subagents.db")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("subagent store: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return finishOpenSubagentStore(db, true)
}

// NewSubagentStoreWithDB reuses an existing *sql.DB connection.
func NewSubagentStoreWithDB(db *sql.DB) (*SubagentStore, error) {
	return finishOpenSubagentStore(db, false)
}

func finishOpenSubagentStore(db *sql.DB, ownsDB bool) (*SubagentStore, error) {
	s := &SubagentStore{db: db, ownsDB: ownsDB}
	if err := s.migrate(); err != nil {
		if ownsDB {
			db.Close()
		}
		return nil, fmt.Errorf("subagent store: migrate: %w", err)
	}
	return s, nil
}

// Close closes the database if owned.
func (s *SubagentStore) Close() error {
	if s.ownsDB {
		return s.db.Close()
	}
	return nil
}

func (s *SubagentStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS subagents (
			id                 TEXT PRIMARY KEY,
			parent_session_id  TEXT DEFAULT '',
			task               TEXT NOT NULL,
			status             TEXT NOT NULL DEFAULT 'running',
			depth              INTEGER DEFAULT 0,
			started_at         INTEGER NOT NULL,
			ended_at           INTEGER DEFAULT 0,
			error              TEXT DEFAULT '',
			agent_type         TEXT DEFAULT '',
			last_heartbeat_at  INTEGER DEFAULT 0,
			pid                INTEGER DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_subagents_status ON subagents(status);
		CREATE INDEX IF NOT EXISTS idx_subagents_parent ON subagents(parent_session_id);
	`)
	return err
}

// Create inserts a new subagent record.
func (s *SubagentStore) Create(r *SubagentRecord) error {
	if r.ID == "" {
		return fmt.Errorf("subagent store: id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO subagents(id, parent_session_id, task, status, depth, started_at, ended_at, error, agent_type, last_heartbeat_at, pid)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ParentSessionID, r.Task, r.Status, r.Depth,
		r.StartedAt.Unix(), endTimeUnix(r.EndedAt), r.Error, r.AgentType,
		r.LastHeartbeatAt.Unix(), r.PID,
	)
	if err != nil {
		return fmt.Errorf("subagent store: create: %w", err)
	}
	return nil
}

// MarkCompleted updates a subagent record to its final state.
func (s *SubagentStore) MarkCompleted(id, status, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`UPDATE subagents SET status = ?, ended_at = ?, error = ? WHERE id = ?`,
		status, time.Now().Unix(), errMsg, id,
	)
	if err != nil {
		return fmt.Errorf("subagent store: mark completed: %w", err)
	}
	return nil
}

// UpdateHeartbeat refreshes the last_heartbeat_at timestamp for a subagent.
// Used by enhanced stuck detection: if last_heartbeat_at is older than
// StaleTimeout, the subagent is considered stuck.
func (s *SubagentStore) UpdateHeartbeat(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`UPDATE subagents SET last_heartbeat_at = ? WHERE id = ?`,
		time.Now().Unix(), id,
	)
	return err
}

// ListByStatus returns all subagents with the given status.
func (s *SubagentStore) ListByStatus(status string) ([]SubagentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(
		`SELECT id, parent_session_id, task, status, depth, started_at, ended_at, error, agent_type, last_heartbeat_at, pid
		 FROM subagents WHERE status = ? ORDER BY started_at DESC`,
		status,
	)
	if err != nil {
		return nil, fmt.Errorf("subagent store: list by status: %w", err)
	}
	defer rows.Close()
	return scanRecords(rows)
}

// ListByParent returns all subagents for a parent session.
func (s *SubagentStore) ListByParent(parentSessionID string) ([]SubagentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(
		`SELECT id, parent_session_id, task, status, depth, started_at, ended_at, error, agent_type, last_heartbeat_at, pid
		 FROM subagents WHERE parent_session_id = ? ORDER BY started_at DESC`,
		parentSessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("subagent store: list by parent: %w", err)
	}
	defer rows.Close()
	return scanRecords(rows)
}

// Recover marks all 'running' subagents as 'orphaned' and returns them.
// Should be called once at process startup. This prevents losing track of
// subagents that were running when the previous process died.
func (s *SubagentStore) Recover() ([]SubagentRecord, error) {
	orphaned, err := s.ListByStatus("running")
	if err != nil {
		return nil, err
	}
	if len(orphaned) == 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.Exec(
		`UPDATE subagents SET status = 'orphaned', ended_at = ? WHERE status = 'running'`,
		time.Now().Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("subagent store: recover: %w", err)
	}
	return orphaned, nil
}

// GC deletes records older than maxAge (regardless of status).
func (s *SubagentStore) GC(maxAge time.Duration) (int64, error) {
	cutoff := time.Now().Add(-maxAge).Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM subagents WHERE started_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("subagent store: gc: %w", err)
	}
	return res.RowsAffected()
}

// NextID allocates the next subagent ID with the given prefix (e.g. "sub").
// It reads the max existing numeric suffix for the prefix and increments.
// This is atomic within the process (mu held) and safe across restarts
// (reads from SQLite).
func (s *SubagentStore) NextID(prefix string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Find max numeric suffix among IDs with this prefix.
	// IDs are like "sub-1", "sub-2", etc. We extract the number after "prefix-".
	pattern := prefix + "-%"
	var maxN int64
	rows, err := s.db.Query(
		`SELECT id FROM subagents WHERE id LIKE ?`,
		pattern,
	)
	if err != nil {
		return "", fmt.Errorf("subagent store: next id query: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", fmt.Errorf("subagent store: next id scan: %w", err)
		}
		suffix := strings.TrimPrefix(id, prefix+"-")
		if n, err := strconv.ParseInt(suffix, 10, 64); err == nil && n > maxN {
			maxN = n
		}
	}
	rows.Close()
	return fmt.Sprintf("%s-%d", prefix, maxN+1), nil
}

func endTimeUnix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func scanRecords(rows *sql.Rows) ([]SubagentRecord, error) {
	var out []SubagentRecord
	for rows.Next() {
		var r SubagentRecord
		var startedAt, endedAt, heartbeat int64
		if err := rows.Scan(
			&r.ID, &r.ParentSessionID, &r.Task, &r.Status, &r.Depth,
			&startedAt, &endedAt, &r.Error, &r.AgentType, &heartbeat, &r.PID,
		); err != nil {
			return nil, fmt.Errorf("subagent store: scan: %w", err)
		}
		r.StartedAt = time.Unix(startedAt, 0)
		if endedAt > 0 {
			r.EndedAt = time.Unix(endedAt, 0)
		}
		if heartbeat > 0 {
			r.LastHeartbeatAt = time.Unix(heartbeat, 0)
		}
		out = append(out, r)
	}
	return out, nil
}
