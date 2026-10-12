package sqlite

import (
	"database/sql"
	"strings"
	"testing"
)

func TestSessionSchemaRepairIsAtomicAndPreservesExtensions(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE sessions(id TEXT PRIMARY KEY,title TEXT DEFAULT '',cwd TEXT DEFAULT '',status TEXT DEFAULT 'active',created_at INTEGER,updated_at INTEGER,turn INTEGER,total_tokens INTEGER,extra TEXT); INSERT INTO sessions(id,extra) VALUES('keep','operator');`); err != nil {
		t.Fatal(err)
	}
	store := &Store{db: db}
	for index := 0; index < 2; index++ {
		if err := store.migrate(); err != nil {
			t.Fatal(err)
		}
	}
	var extra, summary string
	if err := db.QueryRow(`SELECT extra,summary FROM sessions WHERE id='keep'`).Scan(&extra, &summary); err != nil || extra != "operator" || summary != "" {
		t.Fatal(extra, summary, err)
	}
	if _, err := db.Exec(`DROP TABLE prompt_queue; ALTER TABLE sessions DROP COLUMN summary; DROP TRIGGER messages_ai; DROP TRIGGER messages_ad; DROP TRIGGER messages_au; ALTER TABLE messages DROP COLUMN role`); err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(); err == nil || !strings.Contains(err.Error(), "messages.role") {
		t.Fatal("unsafe repair accepted", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('sessions') WHERE name='summary'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial repair committed", err)
	}
}
