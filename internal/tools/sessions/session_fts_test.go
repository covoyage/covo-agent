package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Test helpers ---

// writeTestSession writes a JSONL session file with the given messages.
func writeTestSession(t *testing.T, sessionsDir, id, name string, messages []sessionMessage) {
	t.Helper()
	path := filepath.Join(sessionsDir, id+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create session file %s: %v", path, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	if err := enc.Encode(sessionEntry{Type: "session", Timestamp: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		t.Fatalf("encode session entry: %v", err)
	}
	if name != "" {
		infoData, _ := json.Marshal(map[string]string{"name": name})
		if err := enc.Encode(sessionEntry{Type: "session_info", Data: infoData}); err != nil {
			t.Fatalf("encode session_info: %v", err)
		}
	}
	for _, msg := range messages {
		msgData, _ := json.Marshal(msg)
		if err := enc.Encode(sessionEntry{Type: "message", Data: msgData}); err != nil {
			t.Fatalf("encode message: %v", err)
		}
	}
}

// newTestFTS creates a FTSSearcher backed by an isolated temp directory.
// The sessions dir is nested under root/sessions so the index dir
// (root/index) is unique per test and doesn't collide with other tests.
func newTestFTS(t *testing.T) (*FTSSearcher, string) {
	t.Helper()
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	fts, err := NewFTSSearcher(sessionsDir)
	if err != nil {
		t.Fatalf("NewFTSSearcher: %v", err)
	}
	t.Cleanup(func() { fts.Close() })
	return fts, sessionsDir
}

// --- toFTSQuery tests (migrated from diffs_test.go) ---

func TestToFTSQuery(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello world", `"hello" OR "world"`},
		{"hello", `"hello"`},
		{"", ""},
		{"  ", ""},
		// Inner quotes are consumed by the Unicode tokenizer (they are not
		// word characters), so the term is normalized to a clean phrase.
		{`say "hello"`, `"say" OR "hello"`},
		// CJK with spaces splits into separate terms.
		{"数据库 索引", `"数据库" OR "索引"`},
		// Latin+CJK without a space stays one token (both are \p{L}); matching
		// it as a single phrase is more precise than splitting.
		{"db索引", `"db索引"`},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := toFTSQuery(tt.input)
			if got != tt.want {
				t.Errorf("toFTSQuery(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Search: basic keyword ---

func TestFTSSearcher_Search_BasicKeyword(t *testing.T) {
	fts, dir := newTestFTS(t)

	writeTestSession(t, dir, "sess-golang", "Go Intro", []sessionMessage{
		{Role: "user", Content: "How do I learn golang from scratch?"},
		{Role: "assistant", Content: "Start with the official Go tour."},
	})
	writeTestSession(t, dir, "sess-python", "Python Tips", []sessionMessage{
		{Role: "user", Content: "How to set up Python venv?"},
		{Role: "assistant", Content: "Use python -m venv myenv."},
	})

	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	results, err := fts.Search(context.Background(), "golang", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result for 'golang'")
	}
	if results[0].SessionID != "sess-golang" {
		t.Errorf("expected top result 'sess-golang', got %q", results[0].SessionID)
	}
	for _, r := range results {
		if r.SessionID == "sess-python" {
			t.Error("sess-python should not match 'golang'")
		}
	}
}

// --- Search: OR-join returns results matching any term ---

func TestFTSSearcher_Search_ORJoin(t *testing.T) {
	fts, dir := newTestFTS(t)

	writeTestSession(t, dir, "sess-go", "Go", []sessionMessage{
		{Role: "user", Content: "Tell me about golang"},
	})
	writeTestSession(t, dir, "sess-rust", "Rust", []sessionMessage{
		{Role: "user", Content: "Tell me about rust"},
	})
	writeTestSession(t, dir, "sess-python", "Python", []sessionMessage{
		{Role: "user", Content: "Tell me about python"},
	})

	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// OR query: should match both golang and rust sessions, but not python
	results, err := fts.Search(context.Background(), "golang rust", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	matched := make(map[string]bool)
	for _, r := range results {
		matched[r.SessionID] = true
	}
	if !matched["sess-go"] {
		t.Error("expected sess-go to match 'golang rust'")
	}
	if !matched["sess-rust"] {
		t.Error("expected sess-rust to match 'golang rust'")
	}
	if matched["sess-python"] {
		t.Error("sess-python should not match 'golang rust'")
	}
}

// --- Search: relative score floor filters common-term noise ---
//
// This is the core innovation of P0-1: OR-join is only viable because the
// relative score floor discards rows whose BM25 score is below 15% of the
// top hit. Without the floor, OR-join would flood results with common-term
// matches.
//
// Setup:
//   - 1 "rare" session containing "golang" (rare term, positive IDF) repeated
//     many times, plus one occurrence of "testing" (common term).
//   - 19 "common" sessions containing only "testing" — with "testing" in all
//     20 messages, its IDF is negative, so these rows get a positive (bad)
//     BM25 rank.
//
// Searching "golang testing" (OR-join) matches all 20 sessions, but the floor
// should filter out the 19 common-only sessions (positive rank > floorRank).

func TestFTSSearcher_Search_RelativeScoreFloor(t *testing.T) {
	fts, dir := newTestFTS(t)

	// Rare session: "golang" repeated 20 times + one "testing"
	rareContent := strings.Repeat("golang ", 20) + "testing"
	writeTestSession(t, dir, "sess-rare", "Rare", []sessionMessage{
		{Role: "user", Content: rareContent},
	})

	// 19 common sessions: only "testing" (plus padding)
	for i := 0; i < 19; i++ {
		id := fmt.Sprintf("sess-common-%02d", i)
		writeTestSession(t, dir, id, "", []sessionMessage{
			{Role: "user", Content: "testing some general content here with various words"},
		})
	}

	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	results, err := fts.Search(context.Background(), "golang testing", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}

	// The rare session must be the top hit.
	if results[0].SessionID != "sess-rare" {
		t.Errorf("expected top result 'sess-rare', got %q", results[0].SessionID)
	}

	// The floor should have filtered most common-only sessions.
	// All 20 sessions match the OR query, but only those whose BM25 score
	// is within 15% of the top hit should survive.
	t.Logf("got %d results out of 20 matching sessions", len(results))
	if len(results) >= 20 {
		t.Error("expected floor to filter common-only sessions, got all 20")
	}

	// No common session should appear before the rare one.
	for i, r := range results {
		if r.SessionID == "sess-rare" {
			break
		}
		if strings.HasPrefix(r.SessionID, "sess-common-") {
			t.Errorf("common session %q ranked above rare session at position %d", r.SessionID, i)
		}
	}
}

// --- Search: empty query lists recent sessions ---

func TestFTSSearcher_Search_EmptyQueryListsRecent(t *testing.T) {
	fts, dir := newTestFTS(t)

	writeTestSession(t, dir, "sess-a", "Alpha", []sessionMessage{
		{Role: "user", Content: "hello"},
	})
	writeTestSession(t, dir, "sess-b", "Beta", []sessionMessage{
		{Role: "user", Content: "world"},
	})

	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	results, err := fts.Search(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(results))
	}
}

// --- Search: no matches returns nil ---

func TestFTSSearcher_Search_NoMatches(t *testing.T) {
	fts, dir := newTestFTS(t)

	writeTestSession(t, dir, "sess-1", "Test", []sessionMessage{
		{Role: "user", Content: "hello world"},
	})

	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	results, err := fts.Search(context.Background(), "nonexistentterm", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if results != nil && len(results) > 0 {
		t.Errorf("expected no results, got %d", len(results))
	}
}

// --- Search: limit is enforced ---

func TestFTSSearcher_Search_LimitEnforced(t *testing.T) {
	fts, dir := newTestFTS(t)

	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("sess-%d", i)
		writeTestSession(t, dir, id, "", []sessionMessage{
			{Role: "user", Content: fmt.Sprintf("golang project number %d", i)},
		})
	}

	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	results, err := fts.Search(context.Background(), "golang", 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) > 3 {
		t.Errorf("expected at most 3 results, got %d", len(results))
	}
}

// --- Search: CJK content ---

func TestFTSSearcher_Search_CJK(t *testing.T) {
	fts, dir := newTestFTS(t)

	writeTestSession(t, dir, "sess-cjk", "中文会话", []sessionMessage{
		{Role: "user", Content: "如何在 Go 中实现数据库索引优化"},
	})
	writeTestSession(t, dir, "sess-en", "English", []sessionMessage{
		{Role: "user", Content: "How to optimize database index in Go"},
	})

	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	results, err := fts.Search(context.Background(), "数据库", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 CJK result")
	}

	found := false
	for _, r := range results {
		if r.SessionID == "sess-cjk" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected sess-cjk in results for '数据库'")
	}
}

// --- Search: multiple matches grouped by session ---

func TestFTSSearcher_Search_GroupsBySession(t *testing.T) {
	fts, dir := newTestFTS(t)

	writeTestSession(t, dir, "sess-multi", "Multi", []sessionMessage{
		{Role: "user", Content: "golang question one"},
		{Role: "assistant", Content: "golang answer one"},
		{Role: "user", Content: "golang question two"},
		{Role: "assistant", Content: "golang answer two"},
	})

	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	results, err := fts.Search(context.Background(), "golang", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 grouped session, got %d", len(results))
	}
	if results[0].SessionID != "sess-multi" {
		t.Errorf("expected 'sess-multi', got %q", results[0].SessionID)
	}
	if len(results[0].Snippets) < 2 {
		t.Errorf("expected at least 2 snippets, got %d", len(results[0].Snippets))
	}
}

// --- Search: max 3 snippets per session ---

func TestFTSSearcher_Search_MaxSnippetsPerSession(t *testing.T) {
	fts, dir := newTestFTS(t)

	writeTestSession(t, dir, "sess-multi", "Multi", []sessionMessage{
		{Role: "user", Content: "golang question one"},
		{Role: "assistant", Content: "golang answer one"},
		{Role: "user", Content: "golang question two"},
		{Role: "assistant", Content: "golang answer two"},
		{Role: "user", Content: "golang question three"},
	})

	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	results, err := fts.Search(context.Background(), "golang", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 session, got %d", len(results))
	}
	if len(results[0].Snippets) > 3 {
		t.Errorf("expected at most 3 snippets, got %d", len(results[0].Snippets))
	}
}

// --- Search: snippet has role prefix ---

func TestFTSSearcher_Search_SnippetHasRole(t *testing.T) {
	fts, dir := newTestFTS(t)

	writeTestSession(t, dir, "sess-1", "Test", []sessionMessage{
		{Role: "user", Content: "tell me about golang programming"},
	})

	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	results, err := fts.Search(context.Background(), "golang", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if len(results[0].Snippets) == 0 {
		t.Fatal("expected at least 1 snippet")
	}
	if !strings.HasPrefix(results[0].Snippets[0], "[user]") {
		t.Errorf("expected snippet to start with '[user]', got %q", results[0].Snippets[0])
	}
}

// --- Search: case-insensitive query + snippet extraction ---
//
// FTS5's unicode61 tokenizer is case-insensitive, so "GOLANG" matches
// "golang" in content. The snippet extraction must also be case-insensitive
// — previously extractSnippet received the raw (uppercase) query but only
// lowercased the content, causing snippet lookup to fail.

func TestFTSSearcher_Search_CaseInsensitive(t *testing.T) {
	fts, dir := newTestFTS(t)

	writeTestSession(t, dir, "sess-1", "Test", []sessionMessage{
		{Role: "user", Content: "learning golang is fun and productive"},
	})

	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	results, err := fts.Search(context.Background(), "GOLANG", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for uppercase query 'GOLANG'")
	}
	if results[0].SessionID != "sess-1" {
		t.Errorf("expected 'sess-1', got %q", results[0].SessionID)
	}

	// Bug fix verification: snippet should contain the matched term from
	// content, not just "..." from a failed lookup.
	if len(results[0].Snippets) > 0 {
		snippet := results[0].Snippets[0]
		if !strings.Contains(strings.ToLower(snippet), "golang") {
			t.Errorf("snippet should contain 'golang' (case-insensitive match), got %q", snippet)
		}
	}
}

// --- Search: session metadata preserved ---

func TestFTSSearcher_Search_PreservesMetadata(t *testing.T) {
	fts, dir := newTestFTS(t)

	writeTestSession(t, dir, "sess-meta", "My Session", []sessionMessage{
		{Role: "user", Content: "golang testing"},
		{Role: "assistant", Content: "here is an answer"},
		{Role: "user", Content: "another question"},
	})

	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	results, err := fts.Search(context.Background(), "golang", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Name != "My Session" {
		t.Errorf("expected name 'My Session', got %q", r.Name)
	}
	if r.MsgCount != 3 {
		t.Errorf("expected msg_count 3, got %d", r.MsgCount)
	}
	if r.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
}

// --- Rebuild: indexes all messages ---

func TestFTSSearcher_Rebuild(t *testing.T) {
	fts, dir := newTestFTS(t)

	writeTestSession(t, dir, "sess-1", "First", []sessionMessage{
		{Role: "user", Content: "hello world"},
		{Role: "assistant", Content: "hi there"},
	})
	writeTestSession(t, dir, "sess-2", "Second", []sessionMessage{
		{Role: "user", Content: "foo bar"},
	})

	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// Both sessions are searchable
	results, _ := fts.Search(context.Background(), "hello", 10)
	if len(results) != 1 || results[0].SessionID != "sess-1" {
		t.Errorf("expected sess-1 for 'hello', got %+v", results)
	}

	results, _ = fts.Search(context.Background(), "foo", 10)
	if len(results) != 1 || results[0].SessionID != "sess-2" {
		t.Errorf("expected sess-2 for 'foo', got %+v", results)
	}

	// Session metadata is preserved
	results, _ = fts.Search(context.Background(), "", 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(results))
	}
	names := make(map[string]string)
	for _, r := range results {
		names[r.SessionID] = r.Name
	}
	if names["sess-1"] != "First" {
		t.Errorf("expected sess-1 name 'First', got %q", names["sess-1"])
	}
	if names["sess-2"] != "Second" {
		t.Errorf("expected sess-2 name 'Second', got %q", names["sess-2"])
	}
}

// --- Rebuild: idempotent (no duplicates) ---

func TestFTSSearcher_Rebuild_Idempotent(t *testing.T) {
	fts, dir := newTestFTS(t)

	writeTestSession(t, dir, "sess-1", "Test", []sessionMessage{
		{Role: "user", Content: "golang testing"},
	})

	if err := fts.Rebuild(); err != nil {
		t.Fatalf("first Rebuild: %v", err)
	}
	results1, _ := fts.Search(context.Background(), "golang", 10)

	if err := fts.Rebuild(); err != nil {
		t.Fatalf("second Rebuild: %v", err)
	}
	results2, _ := fts.Search(context.Background(), "golang", 10)

	if len(results1) != len(results2) {
		t.Errorf("expected same result count after rebuild, got %d then %d",
			len(results1), len(results2))
	}
}

// --- Rebuild: handles corrupt JSONL lines gracefully ---

func TestFTSSearcher_Rebuild_SkipsCorruptLines(t *testing.T) {
	fts, dir := newTestFTS(t)

	// Write a session file with some corrupt lines
	path := filepath.Join(dir, "sess-corrupt.jsonl")
	content := strings.Join([]string{
		`{"type":"session","timestamp":"2026-01-01T00:00:00Z"}`,
		`this is not valid json`,
		`{"type":"message","data":{"role":"user","content":"golang valid"}}`,
		`{"type":"message","data":{"role":"assistant","content":"missing closing brace"`,
		`{"type":"message","data":{"role":"assistant","content":"also valid golang"}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// The valid messages should still be indexed
	results, _ := fts.Search(context.Background(), "golang", 10)
	if len(results) == 0 {
		t.Fatal("expected results despite corrupt lines")
	}
	if results[0].SessionID != "sess-corrupt" {
		t.Errorf("expected 'sess-corrupt', got %q", results[0].SessionID)
	}
}

// --- AutoRebuild: detects newly added session files ---

func TestFTSSearcher_AutoRebuild_DetectsStale(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	// Create FTS with empty dir — autoRebuild indexes nothing
	fts, err := NewFTSSearcher(sessionsDir)
	if err != nil {
		t.Fatalf("NewFTSSearcher: %v", err)
	}
	results, _ := fts.Search(context.Background(), "golang", 10)
	if len(results) != 0 {
		t.Fatalf("expected 0 results initially, got %d", len(results))
	}
	fts.Close()

	// Write a new session file
	writeTestSession(t, sessionsDir, "sess-new", "New", []sessionMessage{
		{Role: "user", Content: "learning golang today"},
	})
	// Sleep briefly so mtime is detectably newer than last_indexed
	time.Sleep(50 * time.Millisecond)

	// New FTS searcher — autoRebuild should detect the stale index
	fts2, err := NewFTSSearcher(sessionsDir)
	if err != nil {
		t.Fatalf("NewFTSSearcher (2): %v", err)
	}
	defer fts2.Close()

	results, _ = fts2.Search(context.Background(), "golang", 10)
	if len(results) == 0 {
		t.Fatal("expected autoRebuild to pick up new session")
	}
	if results[0].SessionID != "sess-new" {
		t.Errorf("expected 'sess-new', got %q", results[0].SessionID)
	}
}

// --- AutoRebuild: fresh index skips rebuild ---

func TestFTSSearcher_AutoRebuild_FreshSkips(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	// Write session before creating FTS
	writeTestSession(t, sessionsDir, "sess-1", "Test", []sessionMessage{
		{Role: "user", Content: "golang test"},
	})

	// Create FTS — autoRebuild indexes the session
	fts, err := NewFTSSearcher(sessionsDir)
	if err != nil {
		t.Fatalf("NewFTSSearcher: %v", err)
	}
	defer fts.Close()

	results, _ := fts.Search(context.Background(), "golang", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// Create a second FTS searcher — should NOT rebuild (index is fresh)
	fts2, err := NewFTSSearcher(sessionsDir)
	if err != nil {
		t.Fatalf("NewFTSSearcher (2): %v", err)
	}
	defer fts2.Close()

	results2, _ := fts2.Search(context.Background(), "golang", 10)
	if len(results2) != 1 {
		t.Errorf("expected 1 result after fresh skip, got %d", len(results2))
	}
}

// --- extractSnippet ---

func TestExtractSnippet(t *testing.T) {
	t.Run("query in middle", func(t *testing.T) {
		content := "The quick brown fox jumps over the lazy dog"
		snippet := extractSnippet(content, "fox", 10)
		if !strings.Contains(snippet, "fox") {
			t.Errorf("snippet should contain 'fox', got %q", snippet)
		}
		if !strings.HasPrefix(snippet, "...") {
			t.Error("snippet should start with '...' when match is not at start")
		}
		if !strings.HasSuffix(snippet, "...") {
			t.Error("snippet should end with '...' when match is not at end")
		}
	})

	t.Run("query at start", func(t *testing.T) {
		content := "golang is a programming language"
		snippet := extractSnippet(content, "golang", 10)
		if !strings.HasPrefix(snippet, "golang") {
			t.Errorf("snippet should start with 'golang', got %q", snippet)
		}
		if strings.HasPrefix(snippet, "...") {
			t.Error("snippet should not start with '...' when match is at start")
		}
	})

	t.Run("query at end", func(t *testing.T) {
		content := "a programming language called golang"
		snippet := extractSnippet(content, "golang", 10)
		if !strings.Contains(snippet, "golang") {
			t.Errorf("snippet should contain 'golang', got %q", snippet)
		}
		// Match extends to end of content, so no trailing "..."
		if strings.HasSuffix(snippet, "...") {
			t.Errorf("snippet should not end with '...' when match is at end, got %q", snippet)
		}
	})

	t.Run("query not found short content", func(t *testing.T) {
		content := "short content"
		snippet := extractSnippet(content, "missing", 50)
		if snippet != content {
			t.Errorf("expected full content when query not found, got %q", snippet)
		}
	})

	t.Run("query not found long content", func(t *testing.T) {
		content := strings.Repeat("abcdefghij", 100) // 1000 chars
		snippet := extractSnippet(content, "missing", 50)
		if !strings.HasSuffix(snippet, "...") {
			t.Error("expected '...' suffix for truncated content")
		}
		// 50*2 = 100 chars + "..." = 103
		if len(snippet) > 103 {
			t.Errorf("expected truncated to ~100 chars, got %d", len(snippet))
		}
	})

	t.Run("multi-term query finds first term", func(t *testing.T) {
		content := "the first term is golang and the second is rust"
		snippet := extractSnippet(content, "golang rust", 10)
		if !strings.Contains(snippet, "golang") {
			t.Errorf("snippet should contain 'golang' (first matchable term), got %q", snippet)
		}
	})

	t.Run("empty query returns content start", func(t *testing.T) {
		content := "some content here"
		snippet := extractSnippet(content, "", 5)
		if snippet == "" {
			t.Error("expected non-empty snippet for empty query")
		}
	})

	t.Run("content shorter than context returns full", func(t *testing.T) {
		content := "hi"
		snippet := extractSnippet(content, "hi", 100)
		if snippet != "hi" {
			t.Errorf("expected 'hi', got %q", snippet)
		}
	})
}

// --- IndexSpill: spill metadata + preview becomes searchable ---

func TestFTSSearcher_IndexSpill(t *testing.T) {
	fts, dir := newTestFTS(t)

	// Write a normal session so the test isn't empty
	writeTestSession(t, dir, "sess-1", "Test", []sessionMessage{
		{Role: "user", Content: "hello world"},
	})
	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// Index a spill directly via the API
	spillContent := "This is a very long webpack config dump with lots of stuff\nmodule.exports = { entry: './src/index.js' }"
	if err := fts.IndexSpill("sess-1", "webpack_config", "parse later", spillContent, "/tmp/spill.txt", 2000); err != nil {
		t.Fatalf("IndexSpill: %v", err)
	}

	// Search for a term in the spill content — should find it
	results, err := fts.Search(context.Background(), "webpack", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected spill to be searchable via IndexSpill")
	}

	found := false
	for _, r := range results {
		if r.SessionID == "sess-1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected sess-1 to appear in results for spill content 'webpack'")
	}

	// Search for the spill name (also indexed)
	results, err = fts.Search(context.Background(), "webpack_config", 10)
	if err != nil {
		t.Fatalf("Search by name: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected spill name 'webpack_config' to be searchable")
	}
}

// --- IndexSpill: preview truncation ---

func TestFTSSearcher_IndexSpill_Truncation(t *testing.T) {
	fts, dir := newTestFTS(t)
	writeTestSession(t, dir, "sess-1", "Test", []sessionMessage{
		{Role: "user", Content: "hello"},
	})
	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// Content 5000 chars; previewLen=100 → only first 100 chars indexed
	longContent := strings.Repeat("a", 4900) + "UNIQUEMARKER"
	if err := fts.IndexSpill("sess-1", "big-spill", "test", longContent, "/tmp/spill.txt", 100); err != nil {
		t.Fatalf("IndexSpill: %v", err)
	}

	// "UNIQUEMARKER" should NOT be found (it's past the 100-char preview)
	results, err := fts.Search(context.Background(), "UNIQUEMARKER", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.SessionID == "sess-1" {
			t.Error("UNIQUEMARKER should not appear in search results (beyond preview window)")
		}
	}

	// "big-spill" (the name) should still be found
	results, err = fts.Search(context.Background(), "big-spill", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected spill name 'big-spill' to be found")
	}
}

// --- IndexSpill: snippet prefix is [spill] ---

func TestFTSSearcher_IndexSpill_SnippetRole(t *testing.T) {
	fts, dir := newTestFTS(t)
	writeTestSession(t, dir, "sess-1", "Test", []sessionMessage{
		{Role: "user", Content: "hello"},
	})
	if err := fts.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if err := fts.IndexSpill("sess-1", "my-spill", "for later", "searchable spill text here", "/tmp/spill.txt", 2000); err != nil {
		t.Fatalf("IndexSpill: %v", err)
	}

	results, err := fts.Search(context.Background(), "searchable", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}

	// Find the snippet that contains "searchable"
	found := false
	for _, s := range results[0].Snippets {
		if strings.Contains(s, "searchable") {
			if !strings.HasPrefix(s, "[spill]") {
				t.Errorf("expected spill snippet to start with '[spill]', got prefix %q", s[:min(20, len(s))])
			}
			found = true
			break
		}
	}
	if !found {
		t.Error("expected snippet containing 'searchable'")
	}
}
