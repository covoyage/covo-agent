package evolution

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

type SQLiteMemoryProvider struct {
	mu   sync.RWMutex
	db   *sql.DB
	path string
}

func NewSQLiteMemoryProvider(path string) *SQLiteMemoryProvider {
	return &SQLiteMemoryProvider{path: path}
}

func (s *SQLiteMemoryProvider) Name() string { return "sqlite" }

func (s *SQLiteMemoryProvider) Init() error {
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS memories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		store TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		db.Close()
		return fmt.Errorf("create memories table: %w", err)
	}

	s.db = db
	return nil
}

func (s *SQLiteMemoryProvider) Ping() error {
	if s.db == nil {
		return fmt.Errorf("sqlite not initialized")
	}
	return s.db.Ping()
}

func (s *SQLiteMemoryProvider) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *SQLiteMemoryProvider) Read(store MemoryStore) ([]MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query("SELECT id, content FROM memories WHERE store = ? ORDER BY id", string(store))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []MemoryEntry
	for rows.Next() {
		var e MemoryEntry
		if err := rows.Scan(&e.Index, &e.Content); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *SQLiteMemoryProvider) Add(store MemoryStore, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	if len(content) > maxEntryChars {
		content = content[:maxEntryChars]
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec("INSERT INTO memories (store, content) VALUES (?, ?)", string(store), content)
	return err
}

func (s *SQLiteMemoryProvider) Replace(store MemoryStore, oldSubstr, newContent string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var id int
	err = tx.QueryRow(
		"SELECT id FROM memories WHERE store = ? AND content LIKE ? ORDER BY id LIMIT 1",
		string(store), "%"+oldSubstr+"%",
	).Scan(&id)
	if err == sql.ErrNoRows {
		return fmt.Errorf("substring not found in memory")
	}
	if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE memories SET content = ? WHERE id = ?", strings.TrimSpace(newContent), id)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *SQLiteMemoryProvider) Remove(store MemoryStore, substr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec("DELETE FROM memories WHERE store = ? AND content LIKE ?", string(store), "%"+substr+"%")
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("substring not found in memory")
	}
	return nil
}

func (s *SQLiteMemoryProvider) Snapshot(store MemoryStore) string {
	entries, err := s.Read(store)
	if err != nil || len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.Content)
		b.WriteString("\n")
	}
	return b.String()
}
