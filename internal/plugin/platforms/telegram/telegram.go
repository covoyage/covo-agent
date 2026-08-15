package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
)

const (
	PlatformName = "telegram"
	apiBaseURL   = "https://api.telegram.org"
	pollInterval = 2 * time.Second
	pollTimeout  = 30 * time.Second
	maxRetries   = 3
)

type Adapter struct {
	token      string
	botName    string
	httpClient *http.Client
	logger     *slog.Logger

	mu           sync.Mutex
	running      bool
	cancel       context.CancelFunc
	onMessage    func(plugin.IncomingMessage)
	lastOffset   int64
	allowedChats map[int64]bool
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("TELEGRAM_TOKEN"))
	}

	return &Adapter{
		token:        token,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		logger:       logger,
		allowedChats: make(map[int64]bool),
	}
}

func (a *Adapter) Name() string {
	return PlatformName
}

func (a *Adapter) GetName() string {
	return PlatformName
}

func (a *Adapter) GetID() string {
	return PlatformName
}

func (a *Adapter) Category() plugin.Category {
	return plugin.CategoryPlatform
}

func (a *Adapter) GetCategory() plugin.Category {
	return plugin.CategoryPlatform
}

func (a *Adapter) ID() string {
	return PlatformName
}

func (a *Adapter) Platform() plugin.PlatformProvider {
	return a
}

func (a *Adapter) Validate() error {
	if a.token == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN not set in environment")
	}
	return nil
}

func (a *Adapter) OnMessage(callback func(plugin.IncomingMessage)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onMessage = callback
}

func (a *Adapter) Start(ctx context.Context) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("telegram: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("telegram: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	botInfo, err := a.getMe(ctx)
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("telegram: getMe failed: %w", err)
	}
	a.botName = botInfo.Username
	a.logger.Info("telegram: bot started", "username", a.botName)

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
	a.logger.Info("telegram: stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" && msg.Media == nil {
		return nil
	}

	chatID, err := a.parseChatID(channelID)
	if err != nil {
		return fmt.Errorf("telegram: invalid chat ID %q: %w", channelID, err)
	}

	chunks := a.splitLongMessage(msg.Text)
	for i, chunk := range chunks {
		if err := a.sendTextMessage(ctx, chatID, chunk, msg.ParseMode, msg.ReplyTo); err != nil {
			return fmt.Errorf("telegram: send chunk %d/%d: %w", i+1, len(chunks), err)
		}
	}

	if msg.Media != nil {
		switch msg.Media.Type {
		case "photo":
			return a.sendPhoto(ctx, chatID, msg.Media)
		case "document":
			return a.sendDocument(ctx, chatID, msg.Media)
		default:
			a.logger.Warn("telegram: unsupported media type", "type", msg.Media.Type)
		}
	}

	return nil
}

func (a *Adapter) sendTextMessage(ctx context.Context, chatID int64, text string, parseMode string, replyTo string) error {
	if text == "" {
		return nil
	}

	body := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}

	if parseMode != "" {
		body["parse_mode"] = parseMode
	} else {
		body["parse_mode"] = "Markdown"
	}

	if replyTo != "" {
		body["reply_to_message_id"] = replyTo
	}

	body["link_preview_options"] = map[string]bool{"is_disabled": true}

	return a.apiCall(ctx, "sendMessage", body, nil)
}

func (a *Adapter) sendPhoto(ctx context.Context, chatID int64, media *plugin.MediaInfo) error {
	body := map[string]any{
		"chat_id": chatID,
		"photo":   media.URL,
	}
	if media.Caption != "" {
		body["caption"] = media.Caption
	}
	return a.apiCall(ctx, "sendPhoto", body, nil)
}

func (a *Adapter) sendDocument(ctx context.Context, chatID int64, media *plugin.MediaInfo) error {
	if media.Data != nil {
		return a.uploadDocument(ctx, chatID, media)
	}
	body := map[string]any{
		"chat_id":  chatID,
		"document": media.URL,
	}
	if media.Caption != "" {
		body["caption"] = media.Caption
	}
	return a.apiCall(ctx, "sendDocument", body, nil)
}

func (a *Adapter) uploadDocument(ctx context.Context, chatID int64, media *plugin.MediaInfo) error {
	url := fmt.Sprintf("%s/bot%s/sendDocument", apiBaseURL, a.token)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(media.Data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: upload document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram: upload document failed (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

func (a *Adapter) splitLongMessage(text string) []string {
	const maxLen = 4000
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	lines := strings.Split(text, "\n")
	var current strings.Builder

	for _, line := range lines {
		if current.Len()+len(line)+1 > maxLen && current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(line)
	}

	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}

	if len(chunks) == 0 {
		chunks = append(chunks, text[:maxLen])
	}

	return chunks
}

func (a *Adapter) getMe(ctx context.Context) (*telegramUser, error) {
	var result struct {
		OK     bool         `json:"ok"`
		Result telegramUser `json:"result"`
	}
	if err := a.apiCall(ctx, "getMe", nil, &result); err != nil {
		return nil, err
	}
	return &result.Result, nil
}

func (a *Adapter) getUpdates(ctx context.Context, offset int64) ([]telegramUpdate, error) {
	body := map[string]any{
		"offset":  offset,
		"timeout": int(pollTimeout.Seconds()),
	}
	var result struct {
		OK     bool             `json:"ok"`
		Result []telegramUpdate `json:"result"`
	}
	if err := a.apiCall(ctx, "getUpdates", body, &result); err != nil {
		return nil, err
	}
	return result.Result, nil
}

func (a *Adapter) apiCall(ctx context.Context, method string, params map[string]any, result any) error {
	url := fmt.Sprintf("%s/bot%s/%s", apiBaseURL, a.token, method)

	var reqBody io.Reader
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("telegram: marshal params: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", url, reqBody)
		if err != nil {
			return fmt.Errorf("telegram: create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := a.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("telegram: %s request: %w", method, err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("telegram: read response: %w", err)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			lastErr = fmt.Errorf("telegram: rate limited")
			time.Sleep(5 * time.Second)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("telegram: %s failed (status %d): %s", method, resp.StatusCode, string(body))
			continue
		}

		if result != nil {
			if err := json.Unmarshal(body, result); err != nil {
				return fmt.Errorf("telegram: unmarshal response: %w", err)
			}
		}

		return nil
	}

	return lastErr
}

func (a *Adapter) pollLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := a.getUpdates(ctx, a.lastOffset+1)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			a.logger.Warn("telegram: getUpdates failed", "error", err)
			time.Sleep(pollInterval)
			continue
		}

		for _, update := range updates {
			if update.UpdateID >= a.lastOffset {
				a.lastOffset = update.UpdateID
			}

			if update.Message == nil {
				continue
			}

			a.handleMessage(update.Message)
		}

		time.Sleep(pollInterval)
	}
}

func (a *Adapter) handleMessage(msg *telegramMessage) {
	voiceNote := msg.Voice
	if voiceNote == nil {
		voiceNote = msg.Audio
	}
	if msg.Text == "" && voiceNote == nil {
		return
	}

	channelID := fmt.Sprintf("%d", msg.Chat.ID)
	userName := msg.From.FirstName
	if msg.From.Username != "" {
		userName = msg.From.Username
	}

	text := msg.Text
	if text == "" {
		text = msg.Caption
	}

	incoming := plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: channelID,
		UserID:    fmt.Sprintf("%d", msg.From.ID),
		UserName:  userName,
		Text:      text,
		Timestamp: time.Unix(int64(msg.Date), 0),
		Raw:       msg,
	}

	if voiceNote != nil {
		if fileURL, err := a.resolveFileURL(context.Background(), voiceNote.FileID); err != nil {
			a.logger.Warn("telegram: resolve voice file failed", "error", err)
		} else {
			incoming.Attachments = append(incoming.Attachments, plugin.Attachment{
				Type:     plugin.AttachmentTypeAudio,
				URL:      fileURL,
				MimeType: voiceNote.MimeType,
				FileName: fmt.Sprintf("%s.ogg", voiceNote.FileID),
			})
		}
	}

	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()

	if callback != nil {
		callback(incoming)
	}
}

// resolveFileURL resolves a Telegram file_id to a downloadable HTTPS URL via
// the getFile Bot API method.
func (a *Adapter) resolveFileURL(ctx context.Context, fileID string) (string, error) {
	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := a.apiCall(ctx, "getFile", map[string]any{"file_id": fileID}, &result); err != nil {
		return "", fmt.Errorf("telegram: getFile: %w", err)
	}
	if result.Result.FilePath == "" {
		return "", fmt.Errorf("telegram: getFile returned empty file_path")
	}
	return fmt.Sprintf("%s/file/bot%s/%s", apiBaseURL, a.token, result.Result.FilePath), nil
}

func (a *Adapter) parseChatID(channelID string) (int64, error) {
	var chatID int64
	_, err := fmt.Sscanf(channelID, "%d", &chatID)
	if err != nil {
		return 0, fmt.Errorf("invalid chat ID: %s", channelID)
	}
	return chatID, nil
}

type telegramUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type telegramChat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title,omitempty"`
	Username string `json:"username,omitempty"`
}

type telegramMessage struct {
	MessageID int64          `json:"message_id"`
	From      telegramUser   `json:"from,omitempty"`
	Chat      telegramChat   `json:"chat"`
	Date      int64          `json:"date"`
	Text      string         `json:"text,omitempty"`
	Caption   string         `json:"caption,omitempty"`
	Voice     *telegramVoice `json:"voice,omitempty"`
	Audio     *telegramVoice `json:"audio,omitempty"`
}

// telegramVoice describes both "voice" (OGG/OPUS voice notes) and "audio"
// (regular audio files) message payloads — Telegram's Bot API uses the same
// shape for both.
type telegramVoice struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type,omitempty"`
	FileSize int64  `json:"file_size,omitempty"`
}

type telegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *telegramMessage `json:"message,omitempty"`
}
