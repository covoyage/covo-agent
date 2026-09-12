package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
	_ "modernc.org/sqlite"
)

// TestSearchSessionsTrigram verifies CJK and Latin search against the
// trigram-indexed store FTS, including the LIKE fallback for short CJK terms.
func TestSearchSessionsTrigram(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.AppendMessage(ctx, "sess-1", agentcore.Message{Role: "user", Content: "帮我修复登录问题，这关系到用户的记忆功能"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.AppendMessage(ctx, "sess-2", agentcore.Message{Role: "user", Content: "please refactor the database migration code"}); err != nil {
		t.Fatalf("append: %v", err)
	}

	// 2-char CJK term: not matchable by trigram, must come from the LIKE fallback.
	results, err := s.SearchSessions(ctx, "记忆", 20)
	if err != nil {
		t.Fatalf("SearchSessions(记忆): %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for short CJK term 记忆")
	}
	if results[0].SessionID != "sess-1" {
		t.Fatalf("expected sess-1, got %s", results[0].SessionID)
	}
	if !strings.Contains(results[0].Snippet, "记忆") {
		t.Fatalf("expected snippet containing 记忆, got %q", results[0].Snippet)
	}

	// 4-char CJK phrase: trigram path.
	results, err = s.SearchSessions(ctx, "登录问题", 20)
	if err != nil {
		t.Fatalf("SearchSessions(登录问题): %v", err)
	}
	if len(results) == 0 || results[0].SessionID != "sess-1" {
		t.Fatalf("expected trigram hit on sess-1, got %+v", results)
	}

	// Latin query: trigram path.
	results, err = s.SearchSessions(ctx, "migration", 20)
	if err != nil {
		t.Fatalf("SearchSessions(migration): %v", err)
	}
	if len(results) == 0 || results[0].SessionID != "sess-2" {
		t.Fatalf("expected trigram hit on sess-2, got %+v", results)
	}
}

// TestMigrateFTSToTrigram opens a database created with the legacy unicode61
// FTS schema and verifies the automatic rebuild to trigram.
func TestMigrateFTSToTrigram(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Legacy schema: default (unicode61) tokenizer, external content.
	if _, err := db.Exec(`
		CREATE TABLE sessions (id TEXT PRIMARY KEY);
		CREATE TABLE messages (
			rowid INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			seq INTEGER NOT NULL,
			role TEXT NOT NULL,
			content TEXT DEFAULT '',
			tool_name TEXT DEFAULT '',
			tool_call_id TEXT DEFAULT '',
			tool_calls TEXT DEFAULT '[]',
			token_count INTEGER DEFAULT 0,
			created_at INTEGER DEFAULT (unixepoch())
		);
		CREATE VIRTUAL TABLE messages_fts USING fts5(
			content, session_id UNINDEXED, role UNINDEXED,
			content='messages', content_rowid='rowid'
		);
		CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
			INSERT INTO messages_fts(rowid, content, session_id, role)
			VALUES (new.rowid, new.content, new.session_id, new.role);
		END;
	`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id) VALUES ('sess-1')`); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO messages (session_id, seq, role, content) VALUES ('sess-1', 0, 'user', '这是一段中文记忆功能的讨论')`); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	db.Close()

	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore (migration): %v", err)
	}
	defer s.Close()

	var createSQL string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='messages_fts'`).Scan(&createSQL); err != nil {
		t.Fatalf("read fts schema: %v", err)
	}
	if !strings.Contains(createSQL, "trigram") {
		t.Fatalf("expected migrated FTS to use trigram, got: %s", createSQL)
	}

	results, err := s.SearchSessions(context.Background(), "记忆功能", 20)
	if err != nil {
		t.Fatalf("SearchSessions after migration: %v", err)
	}
	if len(results) == 0 || results[0].SessionID != "sess-1" {
		t.Fatalf("expected migrated index to match sess-1, got %+v", results)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestLargeLegacyDBKeepsUnicode61 verifies that a legacy store above the
// inline-migration threshold keeps its unicode61 index (no startup stall)
// and that CJK queries of any length are answered through the LIKE path.
func TestLargeLegacyDBKeepsUnicode61(t *testing.T) {
	prevThreshold := ftsSyncMigrateMaxRowid
	ftsSyncMigrateMaxRowid = 5 // force the "too large" branch for this seed
	t.Cleanup(func() { ftsSyncMigrateMaxRowid = prevThreshold })

	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE sessions (id TEXT PRIMARY KEY);
		CREATE TABLE messages (
			rowid INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			seq INTEGER NOT NULL,
			role TEXT NOT NULL,
			content TEXT DEFAULT '',
			tool_name TEXT DEFAULT '',
			tool_call_id TEXT DEFAULT '',
			tool_calls TEXT DEFAULT '[]',
			token_count INTEGER DEFAULT 0,
			created_at INTEGER DEFAULT (unixepoch())
		);
		CREATE VIRTUAL TABLE messages_fts USING fts5(
			content, session_id UNINDEXED, role UNINDEXED,
			content='messages', content_rowid='rowid'
		);
		CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
			INSERT INTO messages_fts(rowid, content, session_id, role)
			VALUES (new.rowid, new.content, new.session_id, new.role);
		END;
	`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id) VALUES ('sess-1')`); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := db.Exec(`INSERT INTO messages (session_id, seq, role, content) VALUES ('sess-1', ?, 'user', ?)`,
			i, fmt.Sprintf("第%d条：这是一段中文记忆功能的讨论 migration", i)); err != nil {
			t.Fatalf("seed message: %v", err)
		}
	}
	db.Close()

	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	if !s.ftsLegacy {
		t.Fatal("expected store to run in legacy FTS mode above the threshold")
	}
	var createSQL string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='messages_fts'`).Scan(&createSQL); err != nil {
		t.Fatalf("read fts schema: %v", err)
	}
	if strings.Contains(createSQL, "trigram") {
		t.Fatal("legacy store must not be rebuilt inline")
	}

	// Multi-char CJK term: legacy FTS cannot match it; the LIKE path must.
	results, err := s.SearchSessions(context.Background(), "记忆功能", 20)
	if err != nil {
		t.Fatalf("SearchSessions(记忆功能): %v", err)
	}
	if len(results) == 0 || results[0].SessionID != "sess-1" {
		t.Fatalf("expected LIKE fallback hit on sess-1, got %+v", results)
	}

	// Latin term: legacy unicode61 FTS still answers word queries.
	results, err = s.SearchSessions(context.Background(), "migration", 20)
	if err != nil {
		t.Fatalf("SearchSessions(migration): %v", err)
	}
	if len(results) == 0 || results[0].SessionID != "sess-1" {
		t.Fatalf("expected legacy FTS hit on sess-1, got %+v", results)
	}
}
