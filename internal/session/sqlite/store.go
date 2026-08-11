package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covo-agent/internal/session/sqlitefs"
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
}

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

		-- FTS5 full-text search across all message content
		CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
			content, session_id UNINDEXED, role UNINDEXED,
			content='messages', content_rowid='rowid'
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
	`)

	// Schema v1 → v2: add parent_id, label, summary columns
	if err == nil {
		_, _ = s.db.Exec(`ALTER TABLE sessions ADD COLUMN parent_id TEXT DEFAULT ''`)
		_, _ = s.db.Exec(`ALTER TABLE sessions ADD COLUMN label TEXT DEFAULT ''`)
		_, _ = s.db.Exec(`ALTER TABLE sessions ADD COLUMN summary TEXT DEFAULT ''`)
	}

	return err
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

// SearchSessions performs FTS5 full-text search across all session messages.
// Returns up to limit results with highlighted snippets.
func (s *Store) SearchSessions(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, role, snippet(messages_fts, 1, '<b>', '</b>', '...', 40) AS snippet
		FROM messages_fts WHERE messages_fts MATCH ? ORDER BY rank LIMIT ?
	`, query, limit)
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
