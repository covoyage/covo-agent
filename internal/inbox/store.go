// Package inbox provides a persistent, crash-resilient message queue for
// cross-session (typically sub-agent → parent) asynchronous notification.
//
// SQLite-backed with 7-day GC, send/drain pattern, decoupled delivery so the
// recipient need not be active at send time. Survives process restarts — a
// sub-agent that completes while the parent is down can still notify it on
// next drain.
package inbox

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// Message is a single inbox entry.
type Message struct {
	ID               int64     `json:"id"`
	RecipientSession string    `json:"recipient_session_id"`
	SenderSession    string    `json:"sender_session_id,omitempty"`
	Message          string    `json:"message"`
	CreatedAt        time.Time `json:"created_at"`
	Status           string    `json:"status"` // pending | drained
}

// Store is a SQLite-backed inbox queue.
type Store struct {
	mu     sync.Mutex
	db     *sql.DB
	dir    string
	ownsDB bool
	gcStop chan struct{}
	gcDone chan struct{}
}

// NewStore opens (or creates) an inbox database at <dir>/inbox.db and starts
// a background GC goroutine that purges drained entries older than maxAge.
// Pass maxAge <= 0 to use the default 7-day retention.
func NewStore(dir string, maxAge time.Duration) (*Store, error) {
	if maxAge <= 0 {
		maxAge = 7 * 24 * time.Hour
	}
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000", dir+"/inbox.db")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("inbox: open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return finishOpen(db, dir, maxAge, true)
}

// NewStoreWithDB reuses an existing *sql.DB connection (e.g. the session
// store's database) and starts a background GC goroutine. The caller retains
// ownership of db.Close(); the inbox store only stops its GC goroutine on
// Close. Follows the goal.NewStore(db) pattern.
func NewStoreWithDB(db *sql.DB, maxAge time.Duration) (*Store, error) {
	if maxAge <= 0 {
		maxAge = 7 * 24 * time.Hour
	}
	return finishOpen(db, "", maxAge, false)
}

func finishOpen(db *sql.DB, dir string, maxAge time.Duration, ownsDB bool) (*Store, error) {
	s := &Store{
		db:     db,
		dir:    dir,
		ownsDB: ownsDB,
		gcStop: make(chan struct{}),
		gcDone: make(chan struct{}),
	}
	if err := s.migrate(); err != nil {
		if ownsDB {
			db.Close()
		}
		return nil, fmt.Errorf("inbox: migrate: %w", err)
	}
	go s.gcLoop(maxAge)
	return s, nil
}

// Close stops the GC goroutine and closes the database if owned.
func (s *Store) Close() error {
	close(s.gcStop)
	<-s.gcDone
	if s.ownsDB {
		return s.db.Close()
	}
	return nil
}

// DB returns the underlying database for advanced queries.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS inbox (
			id                   INTEGER PRIMARY KEY AUTOINCREMENT,
			recipient_session_id TEXT NOT NULL,
			sender_session_id    TEXT DEFAULT '',
			message              TEXT NOT NULL,
			created_at           INTEGER DEFAULT (unixepoch()),
			status               TEXT DEFAULT 'pending'
		);
		CREATE INDEX IF NOT EXISTS idx_inbox_recipient ON inbox(recipient_session_id, status);
		CREATE INDEX IF NOT EXISTS idx_inbox_created   ON inbox(created_at);
	`)
	return err
}

// Send enqueues a message for the recipient session. Returns the message ID.
// The recipient need not exist or be active — delivery is decoupled.
func (s *Store) Send(recipientSessionID, senderSessionID, message string) (int64, error) {
	if recipientSessionID == "" {
		return 0, fmt.Errorf("inbox: recipient_session_id is required")
	}
	if message == "" {
		return 0, fmt.Errorf("inbox: message is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(
		`INSERT INTO inbox(recipient_session_id, sender_session_id, message, created_at, status) VALUES(?, ?, ?, ?, 'pending')`,
		recipientSessionID, senderSessionID, message, time.Now().Unix(),
	)
	if err != nil {
		return 0, fmt.Errorf("inbox: send: %w", err)
	}
	return res.LastInsertId()
}

// Drain returns all pending messages for a session and atomically marks them
// drained. Returns messages in oldest-first order. Returns nil (no error) if
// the inbox is empty.
func (s *Store) Drain(sessionID string) ([]Message, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("inbox: session_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("inbox: drain begin: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(
		`SELECT id, recipient_session_id, sender_session_id, message, created_at, status
		 FROM inbox WHERE recipient_session_id = ? AND status = 'pending'
		 ORDER BY created_at ASC, id ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("inbox: drain query: %w", err)
	}
	var msgs []Message
	for rows.Next() {
		var m Message
		var createdAt int64
		if err := rows.Scan(&m.ID, &m.RecipientSession, &m.SenderSession, &m.Message, &createdAt, &m.Status); err != nil {
			rows.Close()
			return nil, fmt.Errorf("inbox: drain scan: %w", err)
		}
		m.CreatedAt = time.Unix(createdAt, 0)
		msgs = append(msgs, m)
	}
	rows.Close()

	if len(msgs) == 0 {
		return nil, nil
	}

	if _, err := tx.Exec(
		`UPDATE inbox SET status = 'drained' WHERE recipient_session_id = ? AND status = 'pending'`,
		sessionID,
	); err != nil {
		return nil, fmt.Errorf("inbox: drain mark: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("inbox: drain commit: %w", err)
	}
	return msgs, nil
}

// Peek returns pending messages without marking them drained. Read-only preview.
func (s *Store) Peek(sessionID string) ([]Message, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("inbox: session_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(
		`SELECT id, recipient_session_id, sender_session_id, message, created_at, status
		 FROM inbox WHERE recipient_session_id = ? AND status = 'pending'
		 ORDER BY created_at ASC, id ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("inbox: peek: %w", err)
	}
	defer rows.Close()
	var msgs []Message
	for rows.Next() {
		var m Message
		var createdAt int64
		if err := rows.Scan(&m.ID, &m.RecipientSession, &m.SenderSession, &m.Message, &createdAt, &m.Status); err != nil {
			return nil, fmt.Errorf("inbox: peek scan: %w", err)
		}
		m.CreatedAt = time.Unix(createdAt, 0)
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// Count returns the number of pending messages for a session.
func (s *Store) Count(sessionID string) (int, error) {
	if sessionID == "" {
		return 0, fmt.Errorf("inbox: session_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM inbox WHERE recipient_session_id = ? AND status = 'pending'`,
		sessionID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("inbox: count: %w", err)
	}
	return n, nil
}

// GC deletes drained messages older than maxAge and pending messages older
// than 2*maxAge (grace period for undelivered). Returns the number deleted.
func (s *Store) GC(maxAge time.Duration) (int64, error) {
	cutoffDrained := time.Now().Add(-maxAge).Unix()
	cutoffPending := time.Now().Add(-2 * maxAge).Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(
		`DELETE FROM inbox WHERE
		   (status = 'drained' AND created_at < ?) OR
		   (status = 'pending' AND created_at < ?)`,
		cutoffDrained, cutoffPending,
	)
	if err != nil {
		return 0, fmt.Errorf("inbox: gc: %w", err)
	}
	return res.RowsAffected()
}

func (s *Store) gcLoop(maxAge time.Duration) {
	defer close(s.gcDone)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-s.gcStop:
			return
		case <-ticker.C:
			_, _ = s.GC(maxAge)
		}
	}
}
