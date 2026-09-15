package slack

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
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

const (
	PlatformName = "slack"
	apiBaseURL   = "https://slack.com/api"
	pollInterval = 3 * time.Second
	maxRetries   = 3

	reconnectInitial = 1 * time.Second
	reconnectMax     = 60 * time.Second
)

type Adapter struct {
	botToken   string
	appToken   string
	botUserID  string
	httpClient *http.Client
	logger     *slog.Logger
	apiBase    string

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	return &Adapter{
		botToken:   strings.TrimSpace(os.Getenv("SLACK_BOT_TOKEN")),
		appToken:   strings.TrimSpace(os.Getenv("SLACK_APP_TOKEN")),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     logger,
	}
}

func (a *Adapter) Name() string                      { return PlatformName }
func (a *Adapter) GetName() string                   { return PlatformName }
func (a *Adapter) GetID() string                     { return PlatformName }
func (a *Adapter) Category() plugin.Category         { return plugin.CategoryPlatform }
func (a *Adapter) GetCategory() plugin.Category      { return plugin.CategoryPlatform }
func (a *Adapter) ID() string                        { return PlatformName }
func (a *Adapter) Platform() plugin.PlatformProvider { return a }

func (a *Adapter) Validate() error {
	if a.botToken == "" {
		return fmt.Errorf("SLACK_BOT_TOKEN not set in environment")
	}
	if !strings.HasPrefix(a.botToken, "xoxb-") {
		return fmt.Errorf("SLACK_BOT_TOKEN must be a Bot User OAuth Token (starts with xoxb-)")
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
		return fmt.Errorf("slack: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("slack: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	auth, err := a.authTest(ctx)
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("slack: auth test failed: %w", err)
	}
	a.botUserID = auth.UserID
	a.logger.Info("slack: bot connected", "user_id", a.botUserID, "team", auth.Team)

	if a.appToken != "" && strings.HasPrefix(a.appToken, "xapp-") {
		go a.socketModeLoop(ctx)
	} else {
		a.logger.Warn("slack: SLACK_APP_TOKEN not set, socket mode disabled. Set SLACK_APP_TOKEN for real-time messaging.")
	}

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
	a.logger.Info("slack: stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" && msg.Media == nil {
		return nil
	}

	chunks := splitLongMessage(msg.Text)
	for i, chunk := range chunks {
		if err := a.postMessage(ctx, channelID, chunk, msg.ReplyTo); err != nil {
			return fmt.Errorf("slack: send chunk %d/%d: %w", i+1, len(chunks), err)
		}
	}

	if msg.Media != nil {
		if err := a.uploadFile(ctx, channelID, msg.Media); err != nil {
			return fmt.Errorf("slack: upload media: %w", err)
		}
	}

	return nil
}

func (a *Adapter) postMessage(ctx context.Context, channelID, text string, threadTS string) error {
	body := map[string]any{
		"channel": channelID,
		"text":    text,
		"mrkdwn":  true,
	}
	if threadTS != "" {
		body["thread_ts"] = threadTS
	}
	return a.apiCall(ctx, "chat.postMessage", body, nil)
}

func (a *Adapter) uploadFile(ctx context.Context, channelID string, media *plugin.MediaInfo) error {
	// files.upload is deprecated (server returns method_deprecated); use the
	// three-step external upload flow: getUploadURLExternal -> upload bytes ->
	// completeUploadExternal. mirrors the reference implementation client-delivery.ts.
	if media.Data == nil {
		// URL-only media cannot use the external upload flow; post the link.
		return a.postMessage(ctx, channelID, media.URL, "")
	}

	filename := media.FileName
	if filename == "" {
		filename = "file"
	}

	var uploadResp struct {
		slackAPIResponse
		UploadURL string `json:"upload_url"`
		FileID    string `json:"file_id"`
	}
	params := map[string]any{
		"filename": filename,
		"length":   len(media.Data),
	}
	if err := a.apiCall(ctx, "files.getUploadURLExternal", params, &uploadResp); err != nil {
		return fmt.Errorf("slack: getUploadURLExternal: %w", err)
	}
	if uploadResp.UploadURL == "" || uploadResp.FileID == "" {
		return fmt.Errorf("slack: getUploadURLExternal returned empty upload_url/file_id")
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", uploadResp.UploadURL, bytes.NewReader(media.Data))
	if err != nil {
		return fmt.Errorf("slack: create upload request: %w", err)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack: upload file bytes: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack: upload file bytes failed (status %d): %s", resp.StatusCode, string(body))
	}

	complete := map[string]any{
		"files":      []map[string]string{{"id": uploadResp.FileID, "title": filename}},
		"channel_id": channelID,
	}
	if media.Caption != "" {
		complete["initial_comment"] = media.Caption
	}
	if err := a.apiCall(ctx, "files.completeUploadExternal", complete, nil); err != nil {
		return fmt.Errorf("slack: completeUploadExternal: %w", err)
	}
	return nil
}

func (a *Adapter) authTest(ctx context.Context) (*slackAuthResponse, error) {
	var result slackAuthResponse
	if err := a.apiCall(ctx, "auth.test", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (a *Adapter) apiCall(ctx context.Context, method string, params map[string]any, result any) error {
	base := a.apiBase
	if base == "" {
		base = apiBaseURL
	}
	apiURL := fmt.Sprintf("%s/%s", base, method)

	var reqBody io.Reader
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("slack: marshal params: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", apiURL, reqBody)
		if err != nil {
			return fmt.Errorf("slack: create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("Authorization", "Bearer "+a.botToken)

		resp, err := a.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("slack: %s request: %w", method, err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("slack: read response: %w", err)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := resp.Header.Get("Retry-After")
			dur := 5 * time.Second
			if retryAfter != "" {
				if d, err := time.ParseDuration(retryAfter + "s"); err == nil {
					dur = d
				}
			}
			a.logger.Warn("slack: rate limited", "retry_after", dur)
			time.Sleep(dur)
			continue
		}

		var apiResp slackAPIResponse
		if err := json.Unmarshal(body, &apiResp); err != nil {
			return fmt.Errorf("slack: unmarshal response: %w", err)
		}

		if !apiResp.OK {
			if apiResp.Error == "ratelimited" {
				time.Sleep(5 * time.Second)
				continue
			}
			return fmt.Errorf("slack: %s failed: %s", method, apiResp.Error)
		}

		if result != nil {
			if err := json.Unmarshal(body, result); err != nil {
				return fmt.Errorf("slack: unmarshal result: %w", err)
			}
		}

		return nil
	}

	return lastErr
}

func (a *Adapter) socketModeLoop(ctx context.Context) {
	// Reconnect loop: any dial failure or read error tears down the old
	// connection and rebuilds with exponential backoff (1s -> 60s cap).
	// Only ctx cancellation exits the loop. mirrors the reference implementation's
	// monitor/reconnect-policy.ts backoff approach.
	backoff := reconnectInitial
	for {
		if ctx.Err() != nil {
			return
		}

		helloSeen := a.socketModeSession(ctx)
		if ctx.Err() != nil {
			return
		}
		if helloSeen {
			// Connection was healthy at some point; reset the backoff.
			backoff = reconnectInitial
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > reconnectMax {
			backoff = reconnectMax
		}
	}
}

// socketModeSession opens one socket-mode connection and serves it until a
// read error or ctx cancellation. It reports whether a hello envelope was
// received on the connection.
func (a *Adapter) socketModeSession(ctx context.Context) (helloSeen bool) {
	connResp, err := a.appsConnectionsOpen(ctx)
	if err != nil {
		a.logger.Warn("slack: socket mode connections.open failed", "error", err)
		return false
	}

	conn, _, _, err := ws.Dial(ctx, connResp.URL)
	if err != nil {
		a.logger.Warn("slack: socket mode dial failed", "error", err)
		return false
	}
	defer conn.Close()

	a.logger.Info("slack: socket mode connected")

	for {
		select {
		case <-ctx.Done():
			return helloSeen
		default:
		}

		data, _, err := wsutil.ReadServerData(conn)
		if err != nil {
			if ctx.Err() == nil {
				a.logger.Warn("slack: socket mode read failed", "error", err)
			}
			return helloSeen
		}

		var envelope struct {
			EnvelopeID string          `json:"envelope_id"`
			Type       string          `json:"type"`
			Payload    json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			continue
		}

		if envelope.Type == "hello" {
			helloSeen = true
		}

		if envelope.Type == "events_api" {
			a.handleEvent(envelope.Payload)
		}

		if envelope.EnvelopeID != "" {
			ack := map[string]string{"envelope_id": envelope.EnvelopeID}
			ackData, _ := json.Marshal(ack)
			_ = wsutil.WriteClientText(conn, ackData)
		}
	}
}

func (a *Adapter) handleEvent(payload json.RawMessage) {
	var wrapper struct {
		Event struct {
			Type    string `json:"type"`
			User    string `json:"user"`
			Channel string `json:"channel"`
			Text    string `json:"text"`
			TS      string `json:"ts"`
		} `json:"event"`
	}
	if err := json.Unmarshal(payload, &wrapper); err != nil {
		return
	}

	ev := wrapper.Event
	if ev.Type != "message" && ev.Type != "app_mention" {
		return
	}
	if ev.User == a.botUserID {
		return
	}
	if ev.Text == "" {
		return
	}

	text := strings.TrimSpace(ev.Text)
	if ev.Type == "app_mention" {
		text = strings.TrimPrefix(text, "<@"+a.botUserID+">")
		text = strings.TrimSpace(text)
	}

	incoming := plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: ev.Channel,
		UserID:    ev.User,
		UserName:  ev.User,
		Text:      text,
		Timestamp: time.Now(),
		Raw:       payload,
	}

	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()

	if callback != nil {
		callback(incoming)
	}
}

func (a *Adapter) appsConnectionsOpen(ctx context.Context) (*slackConnResponse, error) {
	base := a.apiBase
	if base == "" {
		base = apiBaseURL
	}
	apiURL := fmt.Sprintf("%s/apps.connections.open", base)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.appToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("slack: connections.open: %w", err)
	}
	defer resp.Body.Close()

	var result slackConnResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("slack: decode connections.open: %w", err)
	}
	if !result.OK {
		return nil, fmt.Errorf("slack: connections.open failed: %s", result.Error)
	}
	return &result, nil
}

func splitLongMessage(text string) []string {
	const maxLen = 3000
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

type slackAPIResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type slackAuthResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	UserID string `json:"user_id"`
	Team   string `json:"team"`
}

type slackConnResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	URL   string `json:"url"`
}
