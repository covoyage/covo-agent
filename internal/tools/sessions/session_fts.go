package sessions

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type FTSSearcher struct {
	db          *sql.DB
	mu          sync.RWMutex
	sessionsDir string
}

func NewFTSSearcher(sessionsDir string) (*FTSSearcher, error) {
	idxDir := filepath.Join(filepath.Dir(sessionsDir), "index")
	if err := os.MkdirAll(idxDir, 0755); err != nil {
		return nil, fmt.Errorf("create index dir: %w", err)
	}
	dbPath := filepath.Join(idxDir, "sessions.fts")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}

	// WAL mode + FTS5
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=OFF",
		"PRAGMA cache_size=-8000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %q: %w", p, err)
		}
	}

	// Sessions table
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		name TEXT,
		created_at TEXT,
		updated_at TEXT,
		msg_count INTEGER DEFAULT 0
	)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create sessions table: %w", err)
	}

	// FTS5 virtual table for message content.
	// The trigram tokenizer indexes overlapping 3-character sequences,
	// which handles CJK text correctly (unicode61 merges consecutive CJK
	// characters into a single token, making substring search impossible).
	// Trigram also provides substring matching for Latin text. The trade-off
	// is that queries shorter than 3 characters return no results.
	if _, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
		session_id, role, content,
		tokenize='trigram'
	)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create fts table: %w", err)
	}

	// Index tracking
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS index_meta (
		key TEXT PRIMARY KEY,
		value TEXT
	)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create index_meta table: %w", err)
	}

	s := &FTSSearcher{
		db:          db,
		sessionsDir: sessionsDir,
	}

	// Auto-rebuild if stale
	if err := s.autoRebuild(); err != nil {
		db.Close()
		return nil, fmt.Errorf("auto rebuild: %w", err)
	}

	return s, nil
}

func (s *FTSSearcher) Close() error {
	return s.db.Close()
}

func (s *FTSSearcher) autoRebuild() error {
	// Check if any JSONL file is newer than last index time
	var lastIndexed string
	err := s.db.QueryRow("SELECT value FROM index_meta WHERE key='last_indexed'").Scan(&lastIndexed)
	if err != nil {
		// No index yet — full rebuild
		return s.Rebuild()
	}

	lastTime, err := time.Parse(time.RFC3339, lastIndexed)
	if err != nil {
		return s.Rebuild()
	}

	entries, err := os.ReadDir(s.sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(lastTime) {
			return s.Rebuild()
		}
	}

	return nil
}

func (s *FTSSearcher) Rebuild() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear existing data
	if _, err := tx.Exec("DELETE FROM sessions"); err != nil {
		return fmt.Errorf("clear sessions: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM messages_fts"); err != nil {
		return fmt.Errorf("clear fts: %w", err)
	}

	entries, err := os.ReadDir(s.sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	insSession, err := tx.Prepare("INSERT OR REPLACE INTO sessions (id, name, created_at, updated_at, msg_count) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer insSession.Close()

	insMsg, err := tx.Prepare("INSERT INTO messages_fts (session_id, role, content) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer insMsg.Close()

	now := time.Now().UTC().Format(time.RFC3339)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		sessionName := ""
		createdAt := now
		msgCount := 0

		f, err := os.Open(filepath.Join(s.sessionsDir, entry.Name()))
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			var se sessionEntry
			if err := json.Unmarshal([]byte(line), &se); err != nil {
				continue
			}

			switch se.Type {
			case "session":
				if se.Timestamp != "" {
					createdAt = se.Timestamp
				}
			case "session_info":
				var info struct {
					Name string `json:"name"`
				}
				_ = json.Unmarshal(se.Data, &info)
				sessionName = info.Name
			case "message":
				msgCount++
				var msg struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				}
				if err := json.Unmarshal(se.Data, &msg); err != nil {
					continue
				}
				if msg.Content == "" {
					continue
				}
				if _, err := insMsg.Exec(id, msg.Role, msg.Content); err != nil {
					continue
				}
			}
		}
		f.Close()

		if _, err := insSession.Exec(id, sessionName, createdAt, now, msgCount); err != nil {
			continue
		}
	}

	if _, err := tx.Exec("INSERT OR REPLACE INTO index_meta (key, value) VALUES ('last_indexed', ?)", now); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *FTSSearcher) Search(ctx context.Context, query string, limit int) ([]sessionResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	// Sanitize query for FTS5
	ftsQuery := toFTSQuery(query)
	if ftsQuery == "" {
		// Fallback to listing recent sessions
		return s.listRecent(limit)
	}

	// Over-fetch 3x so the relative score floor can discard common-term
	// noise without starving the result list. Capped at 50 for cost.
	fetchLimit := limit * 3
	if fetchLimit > 50 {
		fetchLimit = 50
	}

	sqlStr := `SELECT m.session_id, s.name, s.updated_at, s.msg_count, m.role, m.content, m.rank
		FROM messages_fts m
		LEFT JOIN sessions s ON s.id = m.session_id
		WHERE messages_fts MATCH ?
		ORDER BY m.rank
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, sqlStr, ftsQuery, fetchLimit)
	if err != nil {
		return nil, fmt.Errorf("fts query: %w", err)
	}
	defer rows.Close()

	sessionHits := make(map[string]struct {
		name     string
		ts       string
		msgCount int
		snippets []string
	})
	sessionOrder := make([]string, 0)

	// Relative score floor: FTS5 rank is negative BM25 (more negative = more
	// relevant). The first row is the top hit; we keep rows whose score is
	// within ftsFloorRatio of it and drop the rest as noise. The #1 hit is
	// always kept regardless. floorRank stays 0 (passes all) until the first
	// row sets it.
	var topRank float64
	topSet := false
	floorRank := 0.0

	for rows.Next() {
		var sessionID, role, content string
		var name, updatedAt sql.NullString
		var msgCount sql.NullInt64
		var rank float64

		if err := rows.Scan(&sessionID, &name, &updatedAt, &msgCount, &role, &content, &rank); err != nil {
			continue
		}

		if !topSet {
			topRank = rank
			floorRank = topRank * ftsFloorRatio
			topSet = true
		} else if topRank < 0 && rank > floorRank {
			// Lower relevance than the floor (rank closer to 0). Skip.
			continue
		}

		entry, ok := sessionHits[sessionID]
		if !ok {
			entry.name = name.String
			entry.ts = updatedAt.String
			if msgCount.Valid {
				entry.msgCount = int(msgCount.Int64)
			}
			sessionOrder = append(sessionOrder, sessionID)
		}

		if len(entry.snippets) < 3 {
			snippet := extractSnippet(content, strings.ToLower(query), 200)
			entry.snippets = append(entry.snippets, fmt.Sprintf("[%s] %s", role, snippet))
		}

		sessionHits[sessionID] = entry
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	var results []sessionResult
	for _, id := range sessionOrder {
		entry := sessionHits[id]
		if len(results) >= limit {
			break
		}
		r := sessionResult{
			SessionID:  id,
			Name:       entry.name,
			MatchCount: len(entry.snippets),
			Snippets:   entry.snippets,
			MsgCount:   entry.msgCount,
		}
		if entry.ts != "" {
			r.Timestamp = entry.ts
		}
		results = append(results, r)
	}

	if len(results) == 0 {
		return nil, nil
	}

	return results, nil
}

func (s *FTSSearcher) listRecent(limit int) ([]sessionResult, error) {
	rows, err := s.db.Query(
		"SELECT id, COALESCE(name,''), updated_at, msg_count FROM sessions ORDER BY updated_at DESC LIMIT ?",
		limit,
	)
	if err != nil {
		// Table may be empty
		return nil, nil
	}
	defer rows.Close()

	var results []sessionResult
	for rows.Next() {
		var id, name, ts string
		var msgCount int
		if err := rows.Scan(&id, &name, &ts, &msgCount); err != nil {
			continue
		}
		results = append(results, sessionResult{
			SessionID: id,
			Name:      name,
			Timestamp: ts,
			MsgCount:  msgCount,
		})
	}
	return results, nil
}

// ftsFloorRatio is the relative score floor: hits whose BM25 score falls
// below this fraction of the top hit's score are discarded as noise. The
// #1 hit is always kept. A relative (not absolute) floor adapts to varying
// corpus sizes — BM25 magnitudes shift with the index, so an absolute
// threshold would mis-kill real hits on small corpora.
const ftsFloorRatio = 0.15

// ftsTokenRe splits a query into terms using a Unicode-aware pattern that
// covers CJK and mixed Latin/CJK input (e.g. "db索引"). \p{L} matches any
// Unicode letter, \p{N} any number, plus underscore.
var ftsTokenRe = regexp.MustCompile(`[\p{L}\p{N}_]+`)

func toFTSQuery(s string) string {
	// Clean the query for FTS5.
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// Tokenize with a Unicode-aware splitter so CJK and mixed Latin/CJK
	// input are broken into terms. Each term is wrapped as a quoted phrase
	// to neutralize FTS5 special characters.
	words := ftsTokenRe.FindAllString(s, -1)
	if len(words) == 0 {
		return ""
	}

	var parts []string
	for _, w := range words {
		// Escape double-quotes inside the phrase.
		w = strings.ReplaceAll(w, `"`, `""`)
		parts = append(parts, `"`+w+`"`)
	}
	// OR-join (not AND): AND requires every term to appear, which often
	// returns zero results for multi-word queries. OR lets BM25 rank by
	// term rarity, and the relative score floor in Search() weeds out the
	// common-term noise that OR would otherwise surface.
	return strings.Join(parts, " OR ")
}
