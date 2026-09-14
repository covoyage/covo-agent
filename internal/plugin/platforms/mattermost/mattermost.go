package mattermost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
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
	PlatformName = "mattermost"

	maxMessageLen    = 4000
	pingInterval     = 30 * time.Second
	reconnectMin     = 1 * time.Second
	reconnectMax     = 30 * time.Second
	stableConnection = time.Minute
)

// authTimeout bounds how long the websocket may stay unauthenticated after
// the authentication_challenge frame before the connection is torn down for
// a reconnect. Variable so tests can shorten it.
var authTimeout = 10 * time.Second

type Adapter struct {
	serverURL   string
	accessToken string
	botName     string
	selfUserID  string
	httpClient  *http.Client
	logger      *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)

	connMu sync.Mutex
	conn   net.Conn
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	return &Adapter{
		serverURL:   strings.TrimRight(strings.TrimSpace(os.Getenv("MATTERMOST_SERVER_URL")), "/"),
		accessToken: strings.TrimSpace(os.Getenv("MATTERMOST_ACCESS_TOKEN")),
		botName:     strings.TrimSpace(os.Getenv("MATTERMOST_BOT_NAME")),
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		logger:      logger,
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
	if a.serverURL == "" || a.accessToken == "" {
		return fmt.Errorf("MATTERMOST_SERVER_URL and MATTERMOST_ACCESS_TOKEN must be set in environment")
	}
	return nil
}

func (a *Adapter) OnMessage(callback func(plugin.IncomingMessage)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onMessage = callback
}

func (a *Adapter) callback(in plugin.IncomingMessage) {
	a.mu.Lock()
	cb := a.onMessage
	a.mu.Unlock()
	if cb != nil {
		cb(in)
	}
}

func (a *Adapter) Start(ctx context.Context) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("mattermost: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("mattermost: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	selfID, err := a.login(ctx)
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("mattermost: login failed: %w", err)
	}
	a.selfUserID = selfID
	a.logger.Info("mattermost: started", "server", a.serverURL, "user_id", selfID)

	go a.wsLoop(ctx)
	return nil
}

// login verifies connectivity by exchanging the access token for a session
// token and returns the authenticated user's ID.
func (a *Adapter) login(ctx context.Context) (string, error) {
	body, err := json.Marshal(map[string]string{"token": a.accessToken})
	if err != nil {
		return "", fmt.Errorf("mattermost: marshal login body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.serverURL+"/api/v4/users/login", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("mattermost: create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("mattermost: login request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("mattermost: read login response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mattermost: login failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var user struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(respBody, &user); err != nil || user.ID == "" {
		return "", fmt.Errorf("mattermost: parse login response: %w", err)
	}
	return user.ID, nil
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
	a.closeConn()
	a.running = false
	a.logger.Info("mattermost: stopped")
	return nil
}

func (a *Adapter) closeConn() {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	if a.conn != nil {
		_ = a.conn.Close()
		a.conn = nil
	}
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" {
		return nil
	}
	chunks := splitLongMessage(msg.Text, maxMessageLen)
	for i, chunk := range chunks {
		if err := a.createPost(ctx, channelID, chunk); err != nil {
			return fmt.Errorf("mattermost: send chunk %d/%d: %w", i+1, len(chunks), err)
		}
	}
	return nil
}

func (a *Adapter) createPost(ctx context.Context, channelID, text string) error {
	body, err := json.Marshal(map[string]string{
		"channel_id": channelID,
		"message":    text,
	})
	if err != nil {
		return fmt.Errorf("mattermost: marshal post body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.serverURL+"/api/v4/posts", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mattermost: create post request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.accessToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("mattermost: post request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("mattermost: create post failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// wsLoop maintains the websocket connection, reconnecting with exponential
// backoff until ctx is cancelled.
func (a *Adapter) wsLoop(ctx context.Context) {
	backoff := reconnectMin
	for {
		start := time.Now()
		err := a.wsSession(ctx)
		if ctx.Err() != nil {
			return
		}
		a.logger.Warn("mattermost: websocket disconnected, reconnecting", "error", err)

		if time.Since(start) > stableConnection {
			backoff = reconnectMin
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > reconnectMax {
			backoff = reconnectMax
		}
	}
}

// wsSession dials the websocket, authenticates and pumps events until an
// error occurs or ctx is cancelled.
func (a *Adapter) wsSession(ctx context.Context) error {
	wsURL, err := websocketURL(a.serverURL)
	if err != nil {
		return fmt.Errorf("mattermost: build websocket URL: %w", err)
	}

	conn, _, _, err := ws.Dial(ctx, wsURL)
	if err != nil {
		return fmt.Errorf("mattermost: websocket dial: %w", err)
	}

	a.connMu.Lock()
	a.conn = conn
	a.connMu.Unlock()

	authPayload, _ := json.Marshal(map[string]any{
		"seq":    1,
		"action": "authentication_challenge",
		"data":   map[string]string{"token": a.accessToken},
	})
	if err := wsutil.WriteClientText(conn, authPayload); err != nil {
		_ = conn.Close()
		return fmt.Errorf("mattermost: websocket auth: %w", err)
	}

	// Keepalive: application-level ping, written under lock so it never
	// interleaves with other frames.
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				ping, _ := json.Marshal(map[string]any{"seq": 0, "action": "ping"})
				a.connMu.Lock()
				conn := a.conn
				if conn != nil {
					_ = wsutil.WriteClientText(conn, ping)
				}
				a.connMu.Unlock()
			}
		}
	}()

	// Bound the unauthenticated window: if no OK reply arrives within
	// authTimeout the read fails and the session is torn down for reconnect.
	const authSeq = 1
	authenticated := false
	_ = conn.SetReadDeadline(time.Now().Add(authTimeout))

	for {
		data, _, err := wsutil.ReadServerData(conn)
		if err != nil {
			_ = conn.Close()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !authenticated {
				return fmt.Errorf("mattermost: websocket authentication timed out after %s: %w", authTimeout, err)
			}
			return fmt.Errorf("mattermost: websocket read: %w", err)
		}

		var envelope struct {
			Event    string `json:"event"`
			Status   string `json:"status"`
			SeqReply int64  `json:"seq_reply"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			continue
		}

		if !authenticated {
			// Authentication reply: {"status":"OK","seq_reply":N}. The
			// "hello" event also signals a fully authenticated session.
			if envelope.SeqReply == authSeq && envelope.Status != "" {
				if envelope.Status != "OK" {
					_ = conn.Close()
					return fmt.Errorf("mattermost: websocket authentication rejected (status %s)", envelope.Status)
				}
				authenticated = true
				_ = conn.SetReadDeadline(time.Time{}) // clear the auth deadline
				continue
			}
			if envelope.Event == "hello" {
				authenticated = true
				_ = conn.SetReadDeadline(time.Time{})
				continue
			}
			// Ignore other frames while waiting for the auth reply.
			continue
		}

		a.handleWSFrame(data)
	}
}

func (a *Adapter) handleWSFrame(data []byte) {
	var frame struct {
		Event string `json:"event"`
		Data  struct {
			Post string `json:"post"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &frame); err != nil {
		return
	}
	if frame.Event != "posted" || frame.Data.Post == "" {
		return
	}

	var post struct {
		ID        string `json:"id"`
		ChannelID string `json:"channel_id"`
		Message   string `json:"message"`
		UserID    string `json:"user_id"`
	}
	if err := json.Unmarshal([]byte(frame.Data.Post), &post); err != nil {
		a.logger.Warn("mattermost: parse posted event failed", "error", err)
		return
	}
	if post.UserID == "" || post.UserID == a.selfUserID {
		return
	}

	a.callback(plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: post.ChannelID,
		UserID:    post.UserID,
		UserName:  post.UserID,
		Text:      post.Message,
		Timestamp: time.Now(),
		Raw:       frame.Data.Post,
	})
}

// websocketURL converts an HTTP(S) server URL into a ws(s) websocket URL.
func websocketURL(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v4/websocket"
	return u.String(), nil
}

func splitLongMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	var current strings.Builder
	for _, line := range strings.Split(text, "\n") {
		for len(line) > maxLen {
			if current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
			}
			chunks = append(chunks, line[:maxLen])
			line = line[maxLen:]
		}
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
	return chunks
}
