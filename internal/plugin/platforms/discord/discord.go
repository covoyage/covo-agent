package discord

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
	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covo-agent/internal/useragent"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

const (
	PlatformName  = "discord"
	apiBaseURL    = "https://discord.com/api/v10"
	gatewayURL    = "wss://gateway.discord.gg/?v=10&encoding=json"
	pollInterval  = 5 * time.Second
	maxRetries    = 3
	heartbeatIntv = 41250 * time.Millisecond
)

type Adapter struct {
	botToken   string
	httpClient *http.Client
	logger     *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	sequence  *int64
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	return &Adapter{
		botToken:   strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN")),
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
		return fmt.Errorf("DISCORD_BOT_TOKEN not set in environment")
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
		return fmt.Errorf("discord: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("discord: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	a.logger.Info("discord: connecting to gateway")
	go a.gatewayLoop(ctx)

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
	a.logger.Info("discord: stopped")
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
		if err := a.createMessage(ctx, channelID, chunk, msg.ReplyTo); err != nil {
			return fmt.Errorf("discord: send chunk %d/%d: %w", i+1, len(chunks), err)
		}
	}

	if msg.Media != nil {
		if err := a.sendFile(ctx, channelID, msg.Media); err != nil {
			return fmt.Errorf("discord: send media: %w", err)
		}
	}

	return nil
}

func (a *Adapter) createMessage(ctx context.Context, channelID, text string, replyTo string) error {
	body := map[string]any{
		"content": text,
	}
	if replyTo != "" {
		body["message_reference"] = map[string]string{"message_id": replyTo}
	}
	return a.apiCall(ctx, "POST", "/channels/"+channelID+"/messages", body, nil)
}

func (a *Adapter) sendFile(ctx context.Context, channelID string, media *plugin.MediaInfo) error {
	body := map[string]any{}
	if media.URL != "" {
		body["content"] = media.URL
		if media.Caption != "" {
			body["content"] = media.Caption + "\n" + media.URL
		}
	} else if media.Data != nil {
		body["content"] = media.Caption
		body["file"] = media.Data
		body["filename"] = media.FileName
	}
	return a.apiCall(ctx, "POST", "/channels/"+channelID+"/messages", body, nil)
}

func (a *Adapter) apiCall(ctx context.Context, method, path string, body map[string]any, result any) error {
	apiURL := apiBaseURL + path

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("discord: marshal body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		req, err := http.NewRequestWithContext(ctx, method, apiURL, reqBody)
		if err != nil {
			return fmt.Errorf("discord: create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bot "+a.botToken)
		req.Header.Set("User-Agent", useragent.UserAgent("covo-agent"))

		resp, err := a.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("discord: %s %s: %w", method, path, err)
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("discord: read response: %w", err)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := 5 * time.Second
			if s := resp.Header.Get("Retry-After"); s != "" {
				if d, err := time.ParseDuration(s + "s"); err == nil {
					retryAfter = d
				}
			}
			a.logger.Warn("discord: rate limited", "retry_after", retryAfter)
			time.Sleep(retryAfter)
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("discord: %s %s failed (status %d): %s", method, path, resp.StatusCode, string(respBody))
			continue
		}

		if result != nil {
			if err := json.Unmarshal(respBody, result); err != nil {
				return fmt.Errorf("discord: unmarshal response: %w", err)
			}
		}

		return nil
	}

	return lastErr
}

func (a *Adapter) gatewayLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := a.connectGateway(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			a.logger.Warn("discord: gateway connection failed, reconnecting", "error", err)
			time.Sleep(pollInterval)
		}
	}
}

func (a *Adapter) connectGateway(ctx context.Context) error {
	conn, _, _, err := ws.Dial(ctx, gatewayURL)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	a.logger.Info("discord: gateway connected")

	helloCh := make(chan int, 1)
	errCh := make(chan error, 1)

	safego.SafeGo(func() {
		for {
			data, _, err := wsutil.ReadServerData(conn)
			if err != nil {
				errCh <- err
				return
			}

			var payload struct {
				Op int             `json:"op"`
				D  json.RawMessage `json:"d"`
				S  *int64          `json:"s"`
				T  string          `json:"t"`
			}
			if err := json.Unmarshal(data, &payload); err != nil {
				continue
			}

			if payload.S != nil {
				a.mu.Lock()
				a.sequence = payload.S
				a.mu.Unlock()
			}

			switch payload.Op {
			case 10:
				var hello struct {
					HeartbeatInterval int `json:"heartbeat_interval"`
				}
				json.Unmarshal(payload.D, &hello)
				helloCh <- hello.HeartbeatInterval
			case 0:
				a.handleDispatch(payload.T, payload.D)
			case 11:
			}
		}
	}, nil)

	var interval int
	select {
	case interval = <-helloCh:
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}

	identify := map[string]any{
		"op": 2,
		"d": map[string]any{
			"token":   a.botToken,
			"intents": 1<<9 | 1<<0,
			"properties": map[string]string{
				"os":      "linux",
				"browser": "covo-agent",
				"device":  "covo-agent",
			},
		},
	}
	identifyData, _ := json.Marshal(identify)
	_ = wsutil.WriteClientText(conn, identifyData)

	heartbeatMs := time.Duration(interval) * time.Millisecond
	ticker := time.NewTicker(heartbeatMs)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			return err
		case <-ticker.C:
			a.mu.Lock()
			seq := a.sequence
			a.mu.Unlock()

			heartbeat := map[string]any{"op": 1, "d": seq}
			hbData, _ := json.Marshal(heartbeat)
			_ = wsutil.WriteClientText(conn, hbData)
		}
	}
}

func (a *Adapter) handleDispatch(eventType string, data json.RawMessage) {
	if eventType != "MESSAGE_CREATE" {
		return
	}

	var msg struct {
		ID        string `json:"id"`
		ChannelID string `json:"channel_id"`
		Author    struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Bot      bool   `json:"bot"`
		} `json:"author"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	if msg.Author.Bot {
		return
	}
	if msg.Content == "" {
		return
	}

	incoming := plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: msg.ChannelID,
		UserID:    msg.Author.ID,
		UserName:  msg.Author.Username,
		Text:      msg.Content,
		Timestamp: time.Now(),
		Raw:       data,
	}

	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()

	if callback != nil {
		callback(incoming)
	}
}

func splitLongMessage(text string) []string {
	const maxLen = 2000
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
