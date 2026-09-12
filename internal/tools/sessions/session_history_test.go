package sessions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListAndReadSessionHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abc.jsonl")
	content := `{"type":"session","timestamp":"2026-01-01T00:00:00Z"}
{"type":"session_info","data":{"name":"demo"}}
{"type":"message","data":{"role":"user","content":"hello world"}}
{"type":"message","data":{"role":"assistant","content":"hi"}}
{"type":"message","data":{"role":"tool","content":"secret tool"}}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	list := ListSessionSummaries(dir, 10)
	if len(list) != 1 || list[0]["session_id"] != "abc" {
		t.Fatalf("%#v", list)
	}
	msgs, err := ReadSessionHistory(dir, "abc", 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 non-tool messages, got %#v", msgs)
	}
	msgs, err = ReadSessionHistory(dir, "abc", 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("want 3 with tools, got %#v", msgs)
	}
}
