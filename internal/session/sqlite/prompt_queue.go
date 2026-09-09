package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/covoyage/covo-agent/internal/promptqueue"
)

// SavePromptQueue replaces the persisted prompt queue for a session.
// An empty entries slice clears any previously stored queue.
func (s *Store) SavePromptQueue(ctx context.Context, sessionID string, entries []promptqueue.Entry) error {
	if sessionID == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin prompt queue tx: %w", err)
	}
	defer tx.Rollback()

	if len(entries) > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO sessions (id, created_at, updated_at)
			VALUES (?, unixepoch(), unixepoch())
		`, sessionID); err != nil {
			return fmt.Errorf("ensure session for prompt queue: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM prompt_queue WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("clear prompt queue: %w", err)
	}

	for i, entry := range entries {
		srcJSON, _ := json.Marshal(entry.SourceIDs)
		combined := 0
		if entry.Combined {
			combined = 1
		}
		queuedAt := entry.QueuedAt
		if queuedAt.IsZero() {
			queuedAt = time.Now()
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO prompt_queue (session_id, seq, id, text, queued_at, combined, source_ids)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, sessionID, i, entry.ID, entry.Text, queuedAt.UnixMilli(), combined, string(srcJSON)); err != nil {
			return fmt.Errorf("insert prompt queue seq=%d: %w", i, err)
		}
	}

	return tx.Commit()
}

// LoadPromptQueue returns the persisted prompt queue for a session, ordered by seq.
func (s *Store) LoadPromptQueue(ctx context.Context, sessionID string) ([]promptqueue.Entry, error) {
	if sessionID == "" {
		return nil, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, text, queued_at, combined, source_ids
		FROM prompt_queue WHERE session_id = ? ORDER BY seq
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query prompt queue: %w", err)
	}
	defer rows.Close()

	var entries []promptqueue.Entry
	for rows.Next() {
		var (
			entry     promptqueue.Entry
			queuedAt  int64
			combined  int
			sourceRaw string
		)
		if err := rows.Scan(&entry.ID, &entry.Text, &queuedAt, &combined, &sourceRaw); err != nil {
			return nil, fmt.Errorf("scan prompt queue: %w", err)
		}
		entry.QueuedAt = time.UnixMilli(queuedAt)
		entry.Combined = combined != 0
		if sourceRaw != "" && sourceRaw != "[]" {
			_ = json.Unmarshal([]byte(sourceRaw), &entry.SourceIDs)
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// PromptQueueCount returns how many queued prompts are stored for a session.
func (s *Store) PromptQueueCount(ctx context.Context, sessionID string) (int, error) {
	if sessionID == "" {
		return 0, nil
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM prompt_queue WHERE session_id = ?`, sessionID).Scan(&count)
	return count, err
}
