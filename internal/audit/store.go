// Package audit provides a persistent audit log for agent tool calls and
// lifecycle events. It is the built-in reference implementation of the
// "safety/audit logic plugin-ification" pattern: plugins can subscribe to
// the EventBus to observe events, or implement plugin.LifecycleHook to
// intercept; this package provides a ready-to-use SQLite-backed audit trail.
package audit

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// Entry is a single audit log record.
type Entry struct {
	ID        int64     `json:"id"`
	EventType string    `json:"event_type"`
	SessionID string    `json:"session_id,omitempty"`
	ToolName  string    `json:"tool_name,omitempty"`
	AgentID   string    `json:"agent_id,omitempty"`
	Data      string    `json:"data,omitempty"` // JSON string
	CreatedAt time.Time `json:"created_at"`
}

// Store is a SQLite-backed audit log.
type Store struct {
	mu     sync.Mutex
	db     *sql.DB
	ownsDB bool
}

// NewStore opens (or creates) an audit database at <dir>/audit.db.
func NewStore(dir string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000", dir+"/audit.db")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("audit: open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return finishOpen(db, true)
}

// NewStoreWithDB reuses an existing *sql.DB connection.
func NewStoreWithDB(db *sql.DB) (*Store, error) {
	return finishOpen(db, false)
}

func finishOpen(db *sql.DB, ownsDB bool) (*Store, error) {
	s := &Store{db: db, ownsDB: ownsDB}
	if err := s.migrate(); err != nil {
		if ownsDB {
			db.Close()
		}
		return nil, fmt.Errorf("audit: migrate: %w", err)
	}
	return s, nil
}

// Close closes the database if owned.
func (s *Store) Close() error {
	if s.ownsDB {
		return s.db.Close()
	}
	return nil
}

// DB returns the underlying database for advanced queries.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_log (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type  TEXT NOT NULL,
			session_id  TEXT DEFAULT '',
			tool_name   TEXT DEFAULT '',
			agent_id    TEXT DEFAULT '',
			data        TEXT DEFAULT '',
			created_at  INTEGER DEFAULT (unixepoch())
		);
		CREATE INDEX IF NOT EXISTS idx_audit_event   ON audit_log(event_type, created_at);
		CREATE INDEX IF NOT EXISTS idx_audit_session ON audit_log(session_id, created_at);
		CREATE INDEX IF NOT EXISTS idx_audit_tool     ON audit_log(tool_name, created_at);
	`)
	return err
}

// Log records a single audit event. data is marshaled to JSON if non-nil.
func (s *Store) Log(eventType, sessionID, toolName, agentID string, data any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var dataStr string
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("audit: marshal data: %w", err)
		}
		dataStr = string(b)
	}
	_, err := s.db.Exec(
		`INSERT INTO audit_log(event_type, session_id, tool_name, agent_id, data, created_at) VALUES(?, ?, ?, ?, ?, ?)`,
		eventType, sessionID, toolName, agentID, dataStr, time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("audit: log: %w", err)
	}
	return nil
}

// QueryFilter controls Query results.
type QueryFilter struct {
	EventType string // exact match, empty = all
	SessionID string // exact match, empty = all
	ToolName  string // exact match, empty = all
	Limit     int    // 0 = default 100, max 1000
	Offset    int
	Since     time.Time // zero = no filter
}

// Query returns audit entries matching the filter, newest-first.
func (s *Store) Query(f QueryFilter) ([]Entry, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	q := `SELECT id, event_type, session_id, tool_name, agent_id, data, created_at FROM audit_log WHERE 1=1`
	var args []any
	if f.EventType != "" {
		q += ` AND event_type = ?`
		args = append(args, f.EventType)
	}
	if f.SessionID != "" {
		q += ` AND session_id = ?`
		args = append(args, f.SessionID)
	}
	if f.ToolName != "" {
		q += ` AND tool_name = ?`
		args = append(args, f.ToolName)
	}
	if !f.Since.IsZero() {
		q += ` AND created_at >= ?`
		args = append(args, f.Since.Unix())
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, f.Limit, f.Offset)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("audit: query: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var createdAt int64
		if err := rows.Scan(&e.ID, &e.EventType, &e.SessionID, &e.ToolName, &e.AgentID, &e.Data, &createdAt); err != nil {
			return nil, fmt.Errorf("audit: scan: %w", err)
		}
		e.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, e)
	}
	return out, nil
}

// Count returns the total number of entries matching the filter (ignores Limit/Offset).
func (s *Store) Count(f QueryFilter) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := `SELECT COUNT(*) FROM audit_log WHERE 1=1`
	var args []any
	if f.EventType != "" {
		q += ` AND event_type = ?`
		args = append(args, f.EventType)
	}
	if f.SessionID != "" {
		q += ` AND session_id = ?`
		args = append(args, f.SessionID)
	}
	if f.ToolName != "" {
		q += ` AND tool_name = ?`
		args = append(args, f.ToolName)
	}
	if !f.Since.IsZero() {
		q += ` AND created_at >= ?`
		args = append(args, f.Since.Unix())
	}
	var n int
	if err := s.db.QueryRow(q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("audit: count: %w", err)
	}
	return n, nil
}

// GC deletes entries older than maxAge. Returns the number deleted.
func (s *Store) GC(maxAge time.Duration) (int64, error) {
	cutoff := time.Now().Add(-maxAge).Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM audit_log WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("audit: gc: %w", err)
	}
	return res.RowsAffected()
}
