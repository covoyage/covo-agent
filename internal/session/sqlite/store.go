package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/covoyage/covo-agent/internal/session/sqlitefs"
	"github.com/covoyage/covonaut/agentcore"
	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// Store persists agent sessions and messages to SQLite.
// Each message is appended individually (not full-snapshot), giving:
//   - Crash resilience (each message is committed)
//   - Session-level queries (list, delete, rename)
//   - Foundation for FTS5 full-text search
type Store struct {
	mu  sync.RWMutex
	db  *sql.DB
	dir string

	// ftsLegacy is set when the messages_fts index predates the trigram
	// tokenizer and is too large to rebuild at open time. CJK queries then
	// fall back to LIKE scans (unicode61 cannot segment CJK at all).
	ftsLegacy bool
}

// ftsSyncMigrateMaxRowid caps the message volume that may be re-indexed
// synchronously at open time. The measured rebuild rate is ~560 rows/s on a
// large store, so 2000 rows bounds the one-time migration to a few seconds.
var ftsSyncMigrateMaxRowid int64 = 2000

// NewStore opens (or creates) a SQLite database at the given directory.
func NewStore(dir string) (*Store, error) {
	// Use NFS-safe DSN: detects network filesystems and uses TRUNCATE
	// journal mode + per-host database files to prevent corruption.
	dsn := sqlitefs.BuildDSN(dir)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite single-writer WAL mode
	db.SetMaxIdleConns(1)

	s := &Store{db: db, dir: dir}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying database for advanced queries.
func (s *Store) DB() *sql.DB { return s.db }

// ---------------------------------------------------------------------------
// Schema & migration
// ---------------------------------------------------------------------------

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY
		);

		CREATE TABLE IF NOT EXISTS sessions (
			id          TEXT PRIMARY KEY,
			title       TEXT DEFAULT '',
			cwd         TEXT DEFAULT '',
			status      TEXT DEFAULT 'active',
			parent_id   TEXT DEFAULT '',
			label       TEXT DEFAULT '',
			summary     TEXT DEFAULT '',
			created_at  INTEGER DEFAULT (unixepoch()),
			updated_at  INTEGER DEFAULT (unixepoch()),
			turn        INTEGER DEFAULT 0,
			total_tokens INTEGER DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS messages (
			rowid       INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			seq         INTEGER NOT NULL,
			role        TEXT NOT NULL,
			content     TEXT DEFAULT '',
			tool_name   TEXT DEFAULT '',
			tool_call_id TEXT DEFAULT '',
			tool_calls  TEXT DEFAULT '[]',
			token_count INTEGER DEFAULT 0,
			created_at  INTEGER DEFAULT (unixepoch())
		);

		-- FTS5 full-text search across all message content.
		-- The trigram tokenizer indexes overlapping 3-character sequences, which
		-- handles CJK text correctly (unicode61 merges consecutive CJK characters
		-- into a single token, making Chinese search impossible). Queries shorter
		-- than 3 characters fall back to a LIKE scan in SearchSessions.
		CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
			content, session_id UNINDEXED, role UNINDEXED,
			content='messages', content_rowid='rowid',
			tokenize='trigram'
		);

		-- Triggers to keep FTS index in sync
		CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
			INSERT INTO messages_fts(rowid, content, session_id, role)
			VALUES (new.rowid, new.content, new.session_id, new.role);
		END;

		CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, content, session_id, role)
			VALUES ('delete', old.rowid, old.content, old.session_id, old.role);
		END;

		CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, content, session_id, role)
			VALUES ('delete', old.rowid, old.content, old.session_id, old.role);
			INSERT INTO messages_fts(rowid, content, session_id, role)
			VALUES (new.rowid, new.content, new.session_id, new.role);
		END;

		CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, seq);

		CREATE TABLE IF NOT EXISTS prompt_queue (
			session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			seq         INTEGER NOT NULL,
			id          TEXT NOT NULL,
			text        TEXT NOT NULL,
			queued_at   INTEGER NOT NULL,
			combined    INTEGER NOT NULL DEFAULT 0,
			source_ids  TEXT DEFAULT '[]',
			PRIMARY KEY (session_id, seq)
		);
		CREATE INDEX IF NOT EXISTS idx_prompt_queue_session ON prompt_queue(session_id);
	`)
	if err != nil {
		return err
	}

	// Schema v1 → v2: add parent_id, label, summary columns
	_, _ = s.db.Exec(`ALTER TABLE sessions ADD COLUMN parent_id TEXT DEFAULT ''`)
	_, _ = s.db.Exec(`ALTER TABLE sessions ADD COLUMN label TEXT DEFAULT ''`)
	_, _ = s.db.Exec(`ALTER TABLE sessions ADD COLUMN summary TEXT DEFAULT ''`)

	if err := s.migrateFTSTrigram(); err != nil {
		return fmt.Errorf("migrate fts to trigram: %w", err)
	}

	return nil
}

// migrateFTSTrigram rebuilds messages_fts with the trigram tokenizer when it
// was created by an older schema using the default unicode61 tokenizer, which
// cannot segment CJK text. The FTS table is a derived index, so dropping and
// repopulating it is lossless.
//
// The rebuild cost scales with total message volume (~560 rows/s on a 1GB
// store), so it must stay off the startup critical path for large databases:
// legacy stores above ftsSyncMigrateMaxRowid keep their unicode61 index and
// SearchSessions routes CJK terms through a LIKE scan instead (see ftsLegacy).
func (s *Store) migrateFTSTrigram() error {
	var createSQL string
	err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='messages_fts'`).Scan(&createSQL)
	if err == sql.ErrNoRows {
		return nil // no FTS table yet; migrate() will create it with trigram
	}
	if err != nil {
		return err
	}
	if strings.Contains(createSQL, "trigram") {
		return nil // already on trigram
	}

	var maxRowid int64
	_ = s.db.QueryRow(`SELECT COALESCE(MAX(rowid), 0) FROM messages`).Scan(&maxRowid)
	if maxRowid > ftsSyncMigrateMaxRowid {
		// Too large to rebuild inline: keep serving the legacy index. CJK
		// queries degrade to substring LIKE scans until the index is rebuilt.
		s.ftsLegacy = true
		slog.Debug("fts: legacy unicode61 index kept (too large for inline trigram rebuild)",
			"max_rowid", maxRowid, "threshold", ftsSyncMigrateMaxRowid)
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Dropping the table also drops its triggers; migrate() recreates them
	// with IF NOT EXISTS on the next open, but recreate them here so the
	// store stays consistent within this migration.
	for _, trg := range []string{"messages_ai", "messages_ad", "messages_au"} {
		if _, err := tx.Exec(`DROP TRIGGER IF EXISTS ` + trg); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DROP TABLE messages_fts`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		CREATE VIRTUAL TABLE messages_fts USING fts5(
			content, session_id UNINDEXED, role UNINDEXED,
			content='messages', content_rowid='rowid',
			tokenize='trigram'
		)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO messages_fts(rowid, content, session_id, role)
		SELECT rowid, content, session_id, role FROM messages`); err != nil {
		return err
	}
	for _, trg := range []string{`
		CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
			INSERT INTO messages_fts(rowid, content, session_id, role)
			VALUES (new.rowid, new.content, new.session_id, new.role);
		END`, `
		CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, content, session_id, role)
			VALUES ('delete', old.rowid, old.content, old.session_id, old.role);
		END`, `
		CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, content, session_id, role)
			VALUES ('delete', old.rowid, old.content, old.session_id, old.role);
			INSERT INTO messages_fts(rowid, content, session_id, role)
			VALUES (new.rowid, new.content, new.session_id, new.role);
		END`} {
		if _, err := tx.Exec(trg); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------------
// agentcore.Store implementation
// ---------------------------------------------------------------------------

// Save persists a full state snapshot for a session.
func (s *Store) Save(ctx context.Context, key string, snap agentcore.StateSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Upsert session
	_, err = tx.ExecContext(ctx, `
		INSERT INTO sessions (id, status, turn, total_tokens, updated_at)
		VALUES (?, ?, ?, ?, unixepoch())
		ON CONFLICT(id) DO UPDATE SET
			status      = excluded.status,
			turn        = excluded.turn,
			total_tokens = excluded.total_tokens,
			updated_at  = unixepoch()
	`, key, string(snap.Status), snap.Turn, snap.TotalUsage.TotalTokens)
	if err != nil {
		return fmt.Errorf("upsert session: %w", err)
	}

	// Get current max seq for incremental append
	var maxSeq int64
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), -1) FROM messages WHERE session_id = ?`, key).Scan(&maxSeq)

	// Append new messages
	for i, msg := range snap.Messages {
		seq := maxSeq + int64(i) + 1
		content := msg.Content
		tcJSON, _ := json.Marshal(msg.ToolCalls)
		blocksJSON, _ := json.Marshal(msg.Blocks)

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO messages (session_id, seq, role, content, tool_name, tool_call_id, tool_calls, token_count)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, key, seq, msg.Role, content, msg.Name, msg.ToolCallID, string(tcJSON), len(blocksJSON)); err != nil {
			return fmt.Errorf("insert message seq=%d: %w", seq, err)
		}
	}

	return tx.Commit()
}

// Load restores a full state snapshot for a session.
func (s *Store) Load(ctx context.Context, key string) (agentcore.StateSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var snap agentcore.StateSnapshot

	// Load session metadata
	_ = s.db.QueryRowContext(ctx, `
		SELECT status, turn, total_tokens FROM sessions WHERE id = ?
	`, key).Scan(&snap.Status, &snap.Turn, &snap.TotalUsage.TotalTokens)

	// Load messages ordered by seq
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, role, content, tool_name, tool_call_id, tool_calls, token_count
		FROM messages WHERE session_id = ? ORDER BY seq
	`, key)
	if err != nil {
		return snap, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var m agentcore.Message
		var seq int64
		var tcStr string
		var tokenCount int64
		if err := rows.Scan(&seq, &m.Role, &m.Content, &m.Name, &m.ToolCallID, &tcStr, &tokenCount); err != nil {
			return snap, fmt.Errorf("scan message: %w", err)
		}
		json.Unmarshal([]byte(tcStr), &m.ToolCalls)

		snap.Messages = append(snap.Messages, m)
	}

	return snap, rows.Err()
}

// Has checks if a session exists.
func (s *Store) Has(ctx context.Context, key string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?)`, key).Scan(&exists)
	return exists, err
}

// ResolveID resolves a session ID from a prefix (typically the 8-char prefix
// shown in /sessions). Tries exact match first, then falls back to LIKE prefix.
func (s *Store) ResolveID(ctx context.Context, prefix string) (string, error) {
	if has, err := s.Has(ctx, prefix); err == nil && has {
		return prefix, nil
	}
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM sessions WHERE id LIKE ? LIMIT 1`, prefix+"%").Scan(&id)
	if err != nil {
		short := prefix
		if len(short) > 8 {
			short = short[:8]
		}
		return "", fmt.Errorf("session %s not found", short)
	}
	return id, nil
}

// Delete removes a session and its messages.
func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, key)
	return err
}

// List returns all session IDs.
func (s *Store) List(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM sessions ORDER BY updated_at DESC`)
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
	return ids, rows.Err()
}

// ---------------------------------------------------------------------------
// Session-level queries (replacing FileStore operations)
// ---------------------------------------------------------------------------

// SessionInfo holds metadata for a listed session.
type SessionInfo struct {
	ID          string
	Title       string
	Cwd         string
	Status      string
	ParentID    string
	Label       string
	Summary     string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Turn        int64
	TotalTokens int64
	MsgCount    int
}

// ListSessions returns session metadata ordered by last update.
func (s *Store) ListSessions(ctx context.Context) ([]SessionInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.title, s.cwd, s.status, s.parent_id, s.label, s.summary,
		       s.created_at, s.updated_at, s.turn, s.total_tokens,
		       COALESCE(m.cnt, 0)
		FROM sessions s
		LEFT JOIN (SELECT session_id, COUNT(*) AS cnt FROM messages GROUP BY session_id) m
		       ON m.session_id = s.id
		ORDER BY s.updated_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var infos []SessionInfo
	for rows.Next() {
		var info SessionInfo
		var createdUnix, updatedUnix int64
		if err := rows.Scan(&info.ID, &info.Title, &info.Cwd, &info.Status,
			&info.ParentID, &info.Label, &info.Summary,
			&createdUnix, &updatedUnix, &info.Turn, &info.TotalTokens, &info.MsgCount); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		info.CreatedAt = time.Unix(createdUnix, 0)
		info.UpdatedAt = time.Unix(updatedUnix, 0)
		infos = append(infos, info)
	}
	return infos, rows.Err()
}

// SetTitle updates a session's title.
func (s *Store) SetTitle(ctx context.Context, sessionID, title string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET title = ?, updated_at = unixepoch() WHERE id = ?`, title, sessionID)
	return err
}

// SetLabel updates a session's label.
func (s *Store) SetLabel(ctx context.Context, sessionID, label string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET label = ?, updated_at = unixepoch() WHERE id = ?`, label, sessionID)
	return err
}

// SetSummary updates a session's auto-generated summary.
func (s *Store) SetSummary(ctx context.Context, sessionID, summary string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET summary = ?, updated_at = unixepoch() WHERE id = ?`, summary, sessionID)
	return err
}

// CopyMessages copies all messages from source session to destination session.
func (s *Store) CopyMessages(ctx context.Context, srcID, dstID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (session_id, seq, role, content, tool_name, tool_call_id, tool_calls, token_count)
		SELECT ?, seq, role, content, tool_name, tool_call_id, tool_calls, token_count
		FROM messages WHERE session_id = ?
		ORDER BY seq
	`, dstID, srcID)
	return err
}

// DeleteSession removes a session and cascades to messages.
func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
	return err
}

// AppendMessage appends a single message to an existing session. Used for
// incremental persistence rather than full-snapshot saves.
func (s *Store) AppendMessage(ctx context.Context, sessionID string, msg agentcore.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var maxSeq int64
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), -1) FROM messages WHERE session_id = ?`, sessionID).Scan(&maxSeq)

	content := msg.Content
	tcJSON, _ := json.Marshal(msg.ToolCalls)
	blocksJSON, _ := json.Marshal(msg.Blocks)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Ensure session row exists (lazy creation on first message)
	_, _ = tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO sessions (id, created_at, updated_at)
		VALUES (?, unixepoch(), unixepoch())
	`, sessionID)

	_, err = tx.ExecContext(ctx, `
		INSERT INTO messages (session_id, seq, role, content, tool_name, tool_call_id, tool_calls, token_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, sessionID, maxSeq+1, msg.Role, content, msg.Name, msg.ToolCallID, string(tcJSON), len(blocksJSON))
	if err != nil {
		return fmt.Errorf("append message: %w", err)
	}

	// Touch session timestamp
	_, _ = tx.ExecContext(ctx, `UPDATE sessions SET updated_at = unixepoch() WHERE id = ?`, sessionID)

	return tx.Commit()
}

// EnsureSession creates a session row if it doesn't already exist.
func (s *Store) EnsureSession(ctx context.Context, sessionID, cwd string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO sessions (id, cwd, created_at, updated_at)
		VALUES (?, ?, unixepoch(), unixepoch())
	`, sessionID, cwd)
	return err
}

// EnsureSessionWithParent creates a session row with a parent_id reference.
func (s *Store) EnsureSessionWithParent(ctx context.Context, sessionID, parentID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO sessions (id, parent_id, created_at, updated_at)
		VALUES (?, ?, unixepoch(), unixepoch())
	`, sessionID, parentID)
	return err
}

// MessageCount returns the number of messages in a session.
func (s *Store) MessageCount(ctx context.Context, sessionID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE session_id = ?`, sessionID).Scan(&count)
	return count, err
}

// SearchResult holds a single FTS5 match.
type SearchResult struct {
	SessionID string
	Role      string
	Snippet   string
}

// ftsTokenRe splits a query into terms using a Unicode-aware pattern that
// covers CJK and mixed Latin/CJK input (e.g. "db索引").
var ftsTokenRe = regexp.MustCompile(`[\p{L}\p{N}_]+`)

// SearchSessions performs FTS5 full-text search across all session messages.
// Returns up to limit results with highlighted snippets.
//
// Two index layouts are supported:
//   - trigram (current): matches terms of 3+ characters; shorter CJK terms
//     fall back to a LIKE scan.
//   - unicode61 (legacy, kept when the store is too large to re-index at
//     open time): cannot segment CJK at all, so every CJK term goes through
//     a LIKE scan; Latin terms still use the FTS index.
func (s *Store) SearchSessions(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	results, err := s.searchFTS(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		seen[r.SessionID+"\x00"+r.Snippet] = true
	}

	for _, term := range s.likeFallbackTerms(query) {
		likeResults, err := s.searchLike(ctx, term, limit)
		if err != nil {
			continue // LIKE is best-effort; keep FTS results on failure
		}
		for _, r := range likeResults {
			key := r.SessionID + "\x00" + r.Snippet
			if !seen[key] {
				seen[key] = true
				results = append(results, r)
			}
		}
		if len(results) >= limit {
			break
		}
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// likeFallbackTerms returns the query terms the FTS index cannot answer and
// that need a LIKE scan: under trigram that is 1-2 rune CJK terms; under the
// legacy unicode61 index it is every CJK term.
func (s *Store) likeFallbackTerms(query string) []string {
	all := cjkSearchTerms(query)
	if s.ftsLegacy {
		return all
	}
	var short []string
	for _, w := range all {
		if utf8.RuneCountInString(w) <= 2 {
			short = append(short, w)
		}
	}
	return short
}

// searchFTS runs a sanitized MATCH query. Under trigram, terms shorter than
// 3 characters are skipped (trigram cannot index them). Under legacy
// unicode61, CJK terms are skipped entirely (they are single merged tokens
// that substring queries can never match); Latin terms of any length work.
func (s *Store) searchFTS(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	var parts []string
	for _, w := range ftsTokenRe.FindAllString(strings.TrimSpace(query), -1) {
		if !s.ftsLegacy {
			if utf8.RuneCountInString(w) < 3 {
				continue
			}
		} else if hasCJK([]rune(w)) {
			continue
		}
		parts = append(parts, `"`+strings.ReplaceAll(w, `"`, `""`)+`"`)
	}
	if len(parts) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, role, snippet(messages_fts, 0, '<b>', '</b>', '...', 40) AS snippet
		FROM messages_fts WHERE messages_fts MATCH ? ORDER BY rank LIMIT ?
	`, strings.Join(parts, " OR "), limit)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.SessionID, &r.Role, &r.Snippet); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// searchLike scans the messages table for a short term (1-2 CJK runes) that
// the trigram index cannot match.
func (s *Store) searchLike(ctx context.Context, term string, limit int) ([]SearchResult, error) {
	pattern := "%" + likeEscape(term) + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, role, content FROM messages
		WHERE content LIKE ? ESCAPE '\'
		ORDER BY created_at DESC LIMIT ?
	`, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var role, content string
		var r SearchResult
		if err := rows.Scan(&r.SessionID, &role, &content); err != nil {
			continue
		}
		r.Role = role
		r.Snippet = likeSnippet(content, term, 120)
		results = append(results, r)
	}
	return results, rows.Err()
}

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// likeSnippet builds a plain-text window around the first occurrence of term.
func likeSnippet(content, term string, contextLen int) string {
	idx := strings.Index(content, term)
	if idx < 0 {
		if len(content) > contextLen*2 {
			return content[:contextLen*2] + "..."
		}
		return content
	}
	start := idx - contextLen
	if start < 0 {
		start = 0
	}
	end := idx + len(term) + contextLen
	if end > len(content) {
		end = len(content)
	}
	snippet := content[start:end]
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(content) {
		snippet += "..."
	}
	return snippet
}

// cjkSearchTerms returns the distinct query terms that contain CJK
// characters (Han, Hiragana, Katakana, or Hangul).
func cjkSearchTerms(query string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, w := range ftsTokenRe.FindAllString(strings.TrimSpace(query), -1) {
		if !seen[w] && hasCJK([]rune(w)) {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

func hasCJK(runes []rune) bool {
	for _, r := range runes {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

// ExportSession returns all messages for a session as formatted text.
func (s *Store) ExportSession(ctx context.Context, sessionID string) (string, error) {
	var sb strings.Builder

	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, role, content, tool_name FROM messages
		WHERE session_id = ? ORDER BY seq
	`, sessionID)
	if err != nil {
		return "", fmt.Errorf("export session: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var seq int64
		var role, content, toolName string
		if err := rows.Scan(&seq, &role, &content, &toolName); err != nil {
			return "", fmt.Errorf("scan: %w", err)
		}
		prefix := ""
		switch role {
		case "user":
			prefix = "👤 "
		case "assistant":
			prefix = "🤖 "
		case "tool":
			prefix = fmt.Sprintf("🔧 [%s] ", toolName)
		case "system":
			prefix = "⚙️  "
		}
		sb.WriteString(prefix)
		sb.WriteString(content)
		sb.WriteString("\n\n")
	}

	return sb.String(), rows.Err()
}

// ExportSessionMarkdown returns all messages for a session formatted as Markdown.
func (s *Store) ExportSessionMarkdown(ctx context.Context, sessionID string) (string, error) {
	var title string
	var createdUnix int64
	err := s.db.QueryRowContext(ctx, `SELECT title, created_at FROM sessions WHERE id = ?`, sessionID).Scan(&title, &createdUnix)
	if err != nil {
		return "", fmt.Errorf("query session: %w", err)
	}
	createdAt := time.Unix(createdUnix, 0)

	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, role, content, tool_name FROM messages
		WHERE session_id = ? ORDER BY seq
	`, sessionID)
	if err != nil {
		return "", fmt.Errorf("export markdown: %w", err)
	}
	defer rows.Close()

	var msgs []struct {
		seq      int64
		role     string
		content  string
		toolName string
	}
	for rows.Next() {
		var m struct {
			seq      int64
			role     string
			content  string
			toolName string
		}
		if err := rows.Scan(&m.seq, &m.role, &m.content, &m.toolName); err != nil {
			return "", fmt.Errorf("scan: %w", err)
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	rows.Close()

	var sb strings.Builder
	sessionName := title
	if sessionName == "" {
		sessionName = sessionID
	}
	fmt.Fprintf(&sb, "# Session: %s\n\n", sessionName)
	fmt.Fprintf(&sb, "**Date**: %s\n\n", createdAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&sb, "**Messages**: %d\n\n", len(msgs))

	for _, m := range msgs {
		sb.WriteString("---\n\n")
		switch m.role {
		case "user":
			sb.WriteString("## User\n\n")
		case "assistant":
			sb.WriteString("## Assistant\n\n")
		case "tool":
			fmt.Fprintf(&sb, "## Tool: %s\n\n", m.toolName)
		case "system":
			sb.WriteString("## System\n\n")
		}
		sb.WriteString(m.content)
		sb.WriteString("\n\n")
	}

	return sb.String(), nil
}

// ExportSessionHTML returns all messages for a session formatted as self-contained HTML.
func (s *Store) ExportSessionHTML(ctx context.Context, sessionID string) (string, error) {
	var title string
	var createdUnix int64
	err := s.db.QueryRowContext(ctx, `SELECT title, created_at FROM sessions WHERE id = ?`, sessionID).Scan(&title, &createdUnix)
	if err != nil {
		return "", fmt.Errorf("query session: %w", err)
	}
	createdAt := time.Unix(createdUnix, 0)

	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, role, content, tool_name FROM messages
		WHERE session_id = ? ORDER BY seq
	`, sessionID)
	if err != nil {
		return "", fmt.Errorf("export html: %w", err)
	}
	defer rows.Close()

	var msgs []struct {
		role, content, toolName string
	}
	var msgCount int
	for rows.Next() {
		var seq int64
		var role, content, toolName string
		if err := rows.Scan(&seq, &role, &content, &toolName); err != nil {
			return "", fmt.Errorf("scan: %w", err)
		}
		msgs = append(msgs, struct {
			role, content, toolName string
		}{role, content, toolName})
		msgCount++
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	var sb strings.Builder

	sb.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Session: `)
	sessionName := title
	if sessionName == "" {
		sessionName = sessionID
	}
	sb.WriteString(htmlEsc(sessionName))
	sb.WriteString(`</title>
<style>
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
    max-width: 800px;
    margin: 0 auto;
    padding: 20px;
    background: #fff;
    color: #333;
    line-height: 1.6;
  }
  h1 { font-size: 1.5em; border-bottom: 2px solid #eee; padding-bottom: 8px; }
  .meta { color: #666; font-size: 0.9em; margin-bottom: 20px; }
  .message {
    margin: 12px 0;
    padding: 12px 16px;
    border-radius: 6px;
    border-left: 4px solid #ccc;
    background: #fafafa;
  }
  .message.user { border-left-color: #4a9eff; }
  .message.assistant { border-left-color: #34c759; }
  .message.tool { border-left-color: #ff9500; }
  .message.system { border-left-color: #8e8e93; }
  .message-header {
    font-weight: 600;
    font-size: 0.85em;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    margin-bottom: 8px;
    color: #555;
  }
  pre {
    background: #1e1e1e;
    color: #d4d4d4;
    padding: 12px;
    border-radius: 4px;
    overflow-x: auto;
    font-size: 0.9em;
  }
  code {
    font-family: "SF Mono", Monaco, "Cascadia Code", "Liberation Mono", monospace;
  }
  p { margin: 0 0 8px 0; }
  p:last-child { margin-bottom: 0; }
</style>
</head>
<body>
<h1>Session: `)
	sb.WriteString(htmlEsc(sessionName))
	sb.WriteString(`</h1>
<div class="meta">Date: `)
	sb.WriteString(createdAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&sb, ` &middot; Messages: %d</div>`, msgCount)

	for _, msg := range msgs {
		roleClass := msg.role
		if roleClass == "" {
			roleClass = "unknown"
		}
		sb.WriteString(`<div class="message `)
		sb.WriteString(roleClass)
		sb.WriteString(`"><div class="message-header">`)
		switch msg.role {
		case "user":
			sb.WriteString("User")
		case "assistant":
			sb.WriteString("Assistant")
		case "tool":
			sb.WriteString("Tool: ")
			sb.WriteString(htmlEsc(msg.toolName))
		case "system":
			sb.WriteString("System")
		}
		sb.WriteString(`</div>`)
		writeHTMLContent(&sb, msg.content)
		sb.WriteString(`</div>`)
	}

	sb.WriteString(`</body>
</html>`)
	return sb.String(), nil
}

var htmlReplacer = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
)

func htmlEsc(s string) string {
	return htmlReplacer.Replace(s)
}

func writeHTMLContent(sb *strings.Builder, content string) {
	lines := strings.Split(content, "\n")
	inCode := false
	started := false

	openPara := func() {
		if !started {
			sb.WriteString("<p>")
			started = true
		}
	}
	closePara := func() {
		if started {
			sb.WriteString("</p>")
			started = false
		}
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			if inCode {
				sb.WriteString("</code></pre>\n")
				inCode = false
				started = false
			} else {
				closePara()
				sb.WriteString("<pre><code>")
				inCode = true
			}
			continue
		}
		if inCode {
			sb.WriteString(htmlEsc(line))
			sb.WriteString("\n")
		} else {
			if line == "" {
				closePara()
			} else {
				openPara()
				escaped := htmlEsc(line)
				escaped = strings.ReplaceAll(escaped, "  ", " &nbsp;")
				sb.WriteString(escaped)
				sb.WriteString("<br>\n")
			}
		}
	}
	if inCode {
		sb.WriteString("</code></pre>")
	} else {
		closePara()
	}
}
