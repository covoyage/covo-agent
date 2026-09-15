package imessage

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
)

type testDB struct {
	path string
	db   *sql.DB
	t    *testing.T
}

func setupTestDB(t *testing.T) *testDB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chat.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	schema := []string{
		`CREATE TABLE handle (ROWID INTEGER PRIMARY KEY, id TEXT)`,
		`CREATE TABLE message (
			ROWID INTEGER PRIMARY KEY,
			text TEXT,
			date INTEGER,
			handle_id INTEGER,
			is_from_me INTEGER,
			service TEXT
		)`,
		`CREATE TABLE chat (
			ROWID INTEGER PRIMARY KEY,
			guid TEXT,
			chat_identifier TEXT,
			room_name TEXT
		)`,
		`CREATE TABLE chat_message_join (chat_id INTEGER, message_id INTEGER)`,
		`INSERT INTO handle (ROWID, id) VALUES (1, '+15550001')`,
		`INSERT INTO handle (ROWID, id) VALUES (2, 'friend@example.com')`,
	}
	for _, s := range schema {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec schema: %v", err)
		}
	}
	return &testDB{path: path, db: db, t: t}
}

func (tdb *testDB) insertMessage(rowid int64, text string, date int64, handleID int64, isFromMe int, service string) {
	tdb.t.Helper()
	_, err := tdb.db.Exec(
		`INSERT INTO message (ROWID, text, date, handle_id, is_from_me, service) VALUES (?, ?, ?, ?, ?, ?)`,
		rowid, text, date, handleID, isFromMe, service)
	if err != nil {
		tdb.t.Fatalf("insert message %d: %v", rowid, err)
	}
}

func (tdb *testDB) insertChat(rowid int64, guid, chatIdentifier, roomName string) {
	tdb.t.Helper()
	_, err := tdb.db.Exec(
		`INSERT INTO chat (ROWID, guid, chat_identifier, room_name) VALUES (?, ?, ?, ?)`,
		rowid, guid, chatIdentifier, roomName)
	if err != nil {
		tdb.t.Fatalf("insert chat %d: %v", rowid, err)
	}
}

func (tdb *testDB) linkChatMessage(chatID, messageID int64) {
	tdb.t.Helper()
	_, err := tdb.db.Exec(
		`INSERT INTO chat_message_join (chat_id, message_id) VALUES (?, ?)`,
		chatID, messageID)
	if err != nil {
		tdb.t.Fatalf("link chat %d message %d: %v", chatID, messageID, err)
	}
}

func newPollTestAdapter(t *testing.T, dbPath string) (*Adapter, chan plugin.IncomingMessage) {
	t.Helper()
	a := &Adapter{
		dbPath:      dbPath,
		service:     "iMessage",
		pollSeconds: 1,
		logger:      testLogger(),
		seenPath:    filepath.Join(t.TempDir(), "imessage_seen.json"),
	}
	callbacks := make(chan plugin.IncomingMessage, 16)
	a.OnMessage(func(msg plugin.IncomingMessage) { callbacks <- msg })
	return a, callbacks
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPollIncremental(t *testing.T) {
	tdb := setupTestDB(t)

	// ROWID 1: deliverable. ROWID 2: from me (skip). ROWID 3: NULL text
	// (skip). ROWID 4: wrong service (skip).
	tdb.insertMessage(1, "hello there", 100*1e9, 1, 0, "iMessage")
	tdb.insertMessage(2, "from me", 101*1e9, 1, 1, "iMessage")
	tdb.insertMessage(3, "", 102*1e9, 1, 0, "iMessage") // will be set NULL below
	if _, err := tdb.db.Exec(`UPDATE message SET text = NULL WHERE ROWID = 3`); err != nil {
		t.Fatalf("null text: %v", err)
	}
	tdb.insertMessage(4, "sms message", 103*1e9, 1, 0, "SMS")
	// ROWID 5: from email handle, still iMessage service → deliverable.
	tdb.insertMessage(5, "email msg", 104*1e9, 2, 0, "iMessage")

	a, callbacks := newPollTestAdapter(t, tdb.path)

	// Simulate "just started, nothing seen yet": lastSeenRowID = 0.
	ctx := context.Background()
	if err := a.pollOnce(ctx); err != nil {
		t.Fatalf("pollOnce: %v", err)
	}

	// Expect ROWID 1 and 5 in ascending order.
	for _, want := range []struct{ channel, text string }{
		{"+15550001", "hello there"},
		{"friend@example.com", "email msg"},
	} {
		select {
		case msg := <-callbacks:
			if msg.Platform != "imessage" || msg.ChannelID != want.channel || msg.Text != want.text {
				t.Fatalf("unexpected callback: %+v", msg)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("expected callback %q", want.text)
		}
	}
	if got := a.lastSeen(); got != 5 {
		t.Fatalf("lastSeen = %d, want 5", got)
	}
	select {
	case msg := <-callbacks:
		t.Fatalf("unexpected extra callback: %+v", msg)
	case <-time.After(200 * time.Millisecond):
	}

	// New message arrives → only that one is delivered.
	tdb.insertMessage(6, "second round", 105*1e9, 1, 0, "iMessage")
	if err := a.pollOnce(ctx); err != nil {
		t.Fatalf("pollOnce 2: %v", err)
	}
	select {
	case msg := <-callbacks:
		if msg.Text != "second round" || msg.ChannelID != "+15550001" {
			t.Fatalf("unexpected callback: %+v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected second-round callback")
	}
	if got := a.lastSeen(); got != 6 {
		t.Fatalf("lastSeen = %d, want 6", got)
	}

	// No new messages → no callbacks.
	if err := a.pollOnce(ctx); err != nil {
		t.Fatalf("pollOnce 3: %v", err)
	}
	select {
	case msg := <-callbacks:
		t.Fatalf("unexpected callback: %+v", msg)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestSeenPersistence(t *testing.T) {
	tdb := setupTestDB(t)
	a, callbacks := newPollTestAdapter(t, tdb.path)
	tdb.insertMessage(1, "persist me", 100*1e9, 1, 0, "iMessage")

	if err := a.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce: %v", err)
	}
	drain(t, callbacks)
	if a.lastSeen() != 1 {
		t.Fatalf("lastSeen = %d, want 1", a.lastSeen())
	}

	// Fresh adapter loads the persisted high-water mark and does not replay.
	b := &Adapter{
		dbPath:   tdb.path,
		service:  "iMessage",
		logger:   testLogger(),
		seenPath: a.seenPath,
	}
	b.loadSeen()
	if b.lastSeen() != 1 {
		t.Fatalf("fresh adapter lastSeen = %d, want 1", b.lastSeen())
	}
}

func TestStartFailsOnMissingDB(t *testing.T) {
	a := &Adapter{
		dbPath:      filepath.Join(t.TempDir(), "does-not-exist.db"),
		service:     "iMessage",
		pollSeconds: 1,
		logger:      testLogger(),
		seenPath:    "",
	}
	err := a.Start(context.Background())
	if err == nil {
		a.Stop()
		t.Fatal("expected Start error for missing chat.db")
	}
	if runtime.GOOS != "darwin" {
		// The OS guard fires before the chat.db probe on non-macOS platforms.
		if !strings.Contains(err.Error(), "only supported on macOS") {
			t.Fatalf("error should mention the macOS-only guard: %v", err)
		}
		return
	}
	if !strings.Contains(err.Error(), "cannot open chat.db") || !strings.Contains(err.Error(), "Full Disk Access") {
		t.Fatalf("error should mention chat.db and Full Disk Access: %v", err)
	}
}

func TestCoreDataToTime(t *testing.T) {
	// Nanoseconds since 2001-01-01: ~22 years after the epoch.
	got := coreDataToTime(700_000_000 * 1e9)
	want := time.Unix(coreDataEpoch+700_000_000, 0).UTC()
	if !got.Equal(want) {
		t.Fatalf("ns conversion: got %v want %v", got, want)
	}
	// Seconds since 2001-01-01 (older macOS format).
	got = coreDataToTime(50)
	want = time.Unix(coreDataEpoch+50, 0).UTC()
	if !got.Equal(want) {
		t.Fatalf("s conversion: got %v want %v", got, want)
	}
}

func TestAppleScriptEscaping(t *testing.T) {
	if got := escapeAppleScript(`say "hi" \ now`); got != `say \"hi\" \\ now` {
		t.Fatalf("escape mismatch: %q", got)
	}
}

func TestGroupChatRouting(t *testing.T) {
	tdb := setupTestDB(t)

	// Group chat: guid and chat_identifier both of the "iMessage;+chatXXXX"
	// form; room_name set.
	tdb.insertChat(10, "iMessage;+chatABC123", "iMessage;+chatABC123", "The Gang")
	// DM chat: chat_identifier is the buddy handle.
	tdb.insertChat(11, "iMessage;-chatDEF456", "+15550001", "")

	tdb.insertMessage(1, "group hi", 100*1e9, 1, 0, "iMessage")
	tdb.linkChatMessage(10, 1)
	tdb.insertMessage(2, "dm hi", 101*1e9, 1, 0, "iMessage")
	tdb.linkChatMessage(11, 2)
	// Orphan message without a chat join falls back to the sender handle.
	tdb.insertMessage(3, "orphan hi", 102*1e9, 1, 0, "iMessage")

	a, callbacks := newPollTestAdapter(t, tdb.path)
	if err := a.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce: %v", err)
	}

	// Group message: channelID uses the chat guid and text is prefixed with
	// the sender handle.
	select {
	case msg := <-callbacks:
		if msg.ChannelID != "iMessage;+chatABC123" {
			t.Errorf("group ChannelID = %q, want iMessage;+chatABC123", msg.ChannelID)
		}
		if msg.Text != "+15550001: group hi" {
			t.Errorf("group Text = %q, want sender-prefixed text", msg.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected group message callback")
	}

	// DM via chat: channelID is chat_identifier, no prefix.
	select {
	case msg := <-callbacks:
		if msg.ChannelID != "+15550001" {
			t.Errorf("dm ChannelID = %q, want +15550001", msg.ChannelID)
		}
		if msg.Text != "dm hi" {
			t.Errorf("dm Text = %q, want unprefixed", msg.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected dm message callback")
	}

	// Orphan: falls back to the handle.
	select {
	case msg := <-callbacks:
		if msg.ChannelID != "+15550001" || msg.Text != "orphan hi" {
			t.Errorf("orphan = %+v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected orphan message callback")
	}
}

func TestResolveChatTarget(t *testing.T) {
	cases := []struct {
		name           string
		chatIdentifier string
		chatGUID       string
		roomName       string
		wantChannel    string
		wantGroup      bool
	}{
		{"group +chat identifier", "iMessage;+chatXYZ", "iMessage;+chatXYZ", "Gang", "iMessage;+chatXYZ", true},
		{"group room_name only", "+15550001", "iMessage;-chat123", "Gang", "+15550001", true},
		{"dm", "+15550001", "iMessage;-chat123", "", "+15550001", false},
		{"no chat", "", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			channel, group := resolveChatTarget(tc.chatIdentifier, tc.chatGUID, tc.roomName)
			if channel != tc.wantChannel || group != tc.wantGroup {
				t.Errorf("resolveChatTarget(%q,%q,%q) = (%q,%v), want (%q,%v)",
					tc.chatIdentifier, tc.chatGUID, tc.roomName, channel, group, tc.wantChannel, tc.wantGroup)
			}
		})
	}
}

func TestBuildSendScript(t *testing.T) {
	// Chat guid target: service-independent chat path.
	chatScript := buildSendScript(`iMessage;+chatABC123`, "hello group")
	if !contains(chatScript, `to chat "iMessage;+chatABC123"`) {
		t.Errorf("chat script missing chat send: %q", chatScript)
	}
	if contains(chatScript, "participant") {
		t.Errorf("chat script must not use participant path: %q", chatScript)
	}

	// Bare "chatXXXX" guid target also routes to the chat path.
	if s := buildSendScript("chat987654", "hi"); !contains(s, `to chat "chat987654"`) {
		t.Errorf("bare chat guid script: %q", s)
	}

	// Buddy handle: existing participant path. The adapter escapes target
	// and text before calling buildSendScript, mirroring that flow here.
	buddyScript := buildSendScript("+15550001", escapeAppleScript(`say "hi"`))
	if !contains(buddyScript, `participant "+15550001" of svc`) {
		t.Errorf("buddy script missing participant: %q", buddyScript)
	}
	if !contains(buddyScript, `send "say \"hi\"" to buddy`) {
		t.Errorf("buddy script missing escaped send: %q", buddyScript)
	}
	if contains(buddyScript, `to chat`) {
		t.Errorf("buddy script must not use chat path: %q", buddyScript)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func drain(t *testing.T, ch chan plugin.IncomingMessage) {
	t.Helper()
	for {
		select {
		case <-ch:
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}
