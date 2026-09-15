package imessage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"

	_ "modernc.org/sqlite"
)

const (
	PlatformName  = "imessage"
	coreDataEpoch = int64(978307200) // 2001-01-01T00:00:00Z in Unix seconds
	defaultDBPath = "~/Library/Messages/chat.db"
	seenFileName  = "imessage_seen.json"
)

type Adapter struct {
	dbPath      string
	service     string
	pollSeconds int
	seenPath    string
	logger      *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)

	seenMu        sync.Mutex
	lastSeenRowID int64
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	dbPath := strings.TrimSpace(os.Getenv("IMESSIAGE_DB_PATH"))
	if dbPath == "" {
		dbPath = defaultDBPath
	}
	dbPath = expandHome(dbPath)

	pollSeconds := 3
	if v := strings.TrimSpace(os.Getenv("IMESSIAGE_POLL_SECONDS")); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &pollSeconds); err != nil || n != 1 || pollSeconds < 1 {
			pollSeconds = 3
		}
	}

	service := strings.TrimSpace(os.Getenv("IMESSIAGE_SERVICE"))
	if service == "" {
		service = "iMessage"
	}

	return &Adapter{
		dbPath:      dbPath,
		service:     service,
		pollSeconds: pollSeconds,
		seenPath:    defaultSeenPath(),
		logger:      logger,
	}
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
		}
	}
	return path
}

func defaultSeenPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".covo-agent", seenFileName)
}

func (a *Adapter) Name() string                      { return PlatformName }
func (a *Adapter) GetName() string                   { return PlatformName }
func (a *Adapter) GetID() string                     { return PlatformName }
func (a *Adapter) Category() plugin.Category         { return plugin.CategoryPlatform }
func (a *Adapter) GetCategory() plugin.Category      { return plugin.CategoryPlatform }
func (a *Adapter) ID() string                        { return PlatformName }
func (a *Adapter) Platform() plugin.PlatformProvider { return a }

func (a *Adapter) Validate() error {
	return nil
}

func (a *Adapter) OnMessage(callback func(plugin.IncomingMessage)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onMessage = callback
}

func (a *Adapter) Start(ctx context.Context) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("imessage: only supported on macOS (current OS: %s)", runtime.GOOS)
	}

	// Fail fast if chat.db cannot be opened read-only (typically missing
	// Full Disk Access permission).
	db, err := openDB(a.dbPath)
	if err != nil {
		return fmt.Errorf("imessage: cannot open chat.db at %s: %w (grant Full Disk Access to this process and make sure iMessage is enabled)", a.dbPath, err)
	}
	_ = db.Close()

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("imessage: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	a.loadSeen()
	if a.lastSeen() == 0 {
		// First run: start from the newest existing row so we don't replay
		// the entire chat history.
		db, err := openDB(a.dbPath)
		if err != nil {
			a.mu.Lock()
			a.running = false
			a.mu.Unlock()
			return fmt.Errorf("imessage: cannot open chat.db at %s: %w", a.dbPath, err)
		}
		maxID, err := maxRowID(db)
		_ = db.Close()
		if err != nil {
			a.mu.Lock()
			a.running = false
			a.mu.Unlock()
			return fmt.Errorf("imessage: query max ROWID: %w", err)
		}
		a.setLastSeen(maxID)
	}
	a.logger.Info("imessage: polling started", "db", a.dbPath, "service", a.service, "last_seen_rowid", a.lastSeen())

	go a.pollLoop(ctx)
	return nil
}

func (a *Adapter) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.running {
		return nil
	}
	if a.cancel != nil {
		a.cancel()
	}
	a.running = false
	a.logger.Info("imessage: adapter stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" {
		return nil
	}
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("imessage: only supported on macOS (current OS: %s)", runtime.GOOS)
	}

	// Escape backslashes and double quotes to prevent AppleScript/string
	// injection via target or text.
	target := escapeAppleScript(channelID)
	text := escapeAppleScript(msg.Text)

	cmd := exec.CommandContext(ctx, "osascript", "-e", buildSendScript(target, text))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("imessage: osascript send failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// isChatTarget reports whether a target is a chat guid (e.g. "iMessage;+chatXXXX"
// for group chats) rather than a buddy handle. Chat guids are service
// independent, matching the reference implementation's chat_id/chat_identifier routing.
func isChatTarget(target string) bool {
	return strings.Contains(target, "+chat") || strings.HasPrefix(target, "chat")
}

// buildSendScript constructs the AppleScript used to deliver text. Chat guid
// targets use `send ... to chat ...` (service independent); everything else
// goes through the iMessage buddy path.
func buildSendScript(target, text string) string {
	if isChatTarget(target) {
		return fmt.Sprintf(`tell application "Messages" to send "%s" to chat "%s"`, text, target)
	}
	return fmt.Sprintf(`tell application "Messages"
set svc to 1st service whose service type = iMessage
set buddy to participant "%s" of svc
send "%s" to buddy
end tell`, target, text)
}

func escapeAppleScript(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

func openDB(dbPath string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func maxRowID(db *sql.DB) (int64, error) {
	var maxID sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(message.ROWID) FROM message`).Scan(&maxID); err != nil {
		return 0, err
	}
	return maxID.Int64, nil
}

func (a *Adapter) pollLoop(ctx context.Context) {
	interval := time.Duration(a.pollSeconds) * time.Second
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.pollOnce(ctx); err != nil {
				a.logger.Warn("imessage: poll failed", "error", err)
			}
		}
	}
}

// pollOnce fetches messages newer than the last seen ROWID, dispatches them
// to the callback, and persists the new high-water mark.
func (a *Adapter) pollOnce(ctx context.Context) error {
	db, err := openDB(a.dbPath)
	if err != nil {
		return fmt.Errorf("open chat.db: %w", err)
	}
	defer db.Close()

	rows, err := a.queryNew(ctx, db, a.lastSeen())
	if err != nil {
		return fmt.Errorf("query new messages: %w", err)
	}

	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()

	for _, row := range rows {
		if callback != nil {
			channelID, isGroup := resolveChatTarget(row.ChatIdentifier, row.ChatGUID, row.RoomName)
			if channelID == "" {
				channelID = row.HandleID
			}
			text := row.Text
			// Prefix group messages with the sender handle so participants
			// can be told apart.
			if isGroup && row.HandleID != "" {
				text = row.HandleID + ": " + text
			}
			callback(plugin.IncomingMessage{
				Platform:  PlatformName,
				ChannelID: channelID,
				UserID:    row.HandleID,
				UserName:  row.HandleID,
				Text:      text,
				Timestamp: coreDataToTime(row.Date),
				Raw: map[string]any{
					"rowid":      row.RowID,
					"date":       row.Date,
					"service":    a.service,
					"account_id": row.HandleID,
					"chat_guid":  row.ChatGUID,
					"is_group":   isGroup,
				},
			})
		}
		if row.RowID > a.lastSeen() {
			a.setLastSeen(row.RowID)
		}
	}
	if len(rows) > 0 {
		a.saveSeen()
	}
	return nil
}

type messageRow struct {
	RowID          int64
	Text           string
	Date           int64
	HandleID       string
	ChatIdentifier string
	ChatGUID       string
	RoomName       string
}

// resolveChatTarget picks the channel ID for a message's chat. Direct chats
// are addressed by chat_identifier (the buddy handle); group chats whose
// chat_identifier is of the "iMessage;+chatXXXX" form are addressed by the
// chat guid instead, matching the reference implementation's target kind handling.
func resolveChatTarget(chatIdentifier, chatGUID, roomName string) (channelID string, isGroup bool) {
	id := chatIdentifier
	if strings.Contains(id, "+chat") && chatGUID != "" {
		id = chatGUID
	}
	if id == "" {
		id = chatGUID
	}
	isGroup = roomName != "" || strings.Contains(chatIdentifier, "+chat") || strings.Contains(chatGUID, "+chat")
	return id, isGroup
}

// queryNew returns up to 50 messages with ROWID greater than after, oldest
// first, so they can be delivered in order. Messages are joined with their
// chat (via chat_message_join) to resolve group targets.
func (a *Adapter) queryNew(ctx context.Context, db *sql.DB, after int64) ([]messageRow, error) {
	query := `SELECT message.ROWID, message.text, message.date, handle.id,
	       chat.chat_identifier, chat.guid, chat.room_name
FROM message
LEFT JOIN handle ON message.handle_id = handle.ROWID
LEFT JOIN chat_message_join cmj ON cmj.message_id = message.ROWID
LEFT JOIN chat ON chat.ROWID = cmj.chat_id
WHERE message.ROWID > ? AND message.is_from_me = 0 AND message.text IS NOT NULL AND message.service = ?
ORDER BY message.date ASC, message.ROWID ASC LIMIT 50`
	rows, err := db.QueryContext(ctx, query, after, a.service)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []messageRow
	for rows.Next() {
		var row messageRow
		var handleID, chatIdentifier, chatGUID, roomName sql.NullString
		if err := rows.Scan(&row.RowID, &row.Text, &row.Date, &handleID, &chatIdentifier, &chatGUID, &roomName); err != nil {
			return nil, err
		}
		row.HandleID = handleID.String
		row.ChatIdentifier = chatIdentifier.String
		row.ChatGUID = chatGUID.String
		row.RoomName = roomName.String
		result = append(result, row)
	}
	return result, rows.Err()
}

// coreDataToTime converts chat.db message.date (Core Data epoch, seconds or
// nanoseconds since 2001-01-01 depending on macOS version) to time.Time.
func coreDataToTime(date int64) time.Time {
	const nsThreshold = 1e15 // real ns timestamps (~1e18) far exceed any seconds value (< 1e10)
	if date > nsThreshold {
		return time.Unix(coreDataEpoch, date).UTC()
	}
	return time.Unix(coreDataEpoch+date, 0).UTC()
}

func (a *Adapter) lastSeen() int64 {
	a.seenMu.Lock()
	defer a.seenMu.Unlock()
	return a.lastSeenRowID
}

func (a *Adapter) setLastSeen(rowID int64) {
	a.seenMu.Lock()
	a.lastSeenRowID = rowID
	a.seenMu.Unlock()
}

func (a *Adapter) loadSeen() {
	if a.seenPath == "" {
		return
	}
	data, err := os.ReadFile(a.seenPath)
	if err != nil {
		return
	}
	var stored struct {
		LastSeenRowID int64 `json:"last_seen_rowid"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		a.logger.Warn("imessage: parse seen file", "path", a.seenPath, "error", err)
		return
	}
	a.seenMu.Lock()
	a.lastSeenRowID = stored.LastSeenRowID
	a.seenMu.Unlock()
}

func (a *Adapter) saveSeen() {
	if a.seenPath == "" {
		return
	}
	rowID := a.lastSeen()
	data, err := json.Marshal(map[string]int64{"last_seen_rowid": rowID})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(a.seenPath), 0o700); err != nil {
		a.logger.Warn("imessage: create config dir", "error", err)
		return
	}
	if err := os.WriteFile(a.seenPath, data, 0o600); err != nil {
		a.logger.Warn("imessage: write seen file", "error", err)
	}
}
