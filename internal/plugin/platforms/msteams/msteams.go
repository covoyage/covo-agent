package msteams

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/messageutil"
)

const (
	PlatformName = "msteams"
	graphScope   = "https://graph.microsoft.com/.default"

	defaultGraphBase = "https://graph.microsoft.com"

	maxMessageBytes    = 28000
	defaultPollSeconds = 15
	pollPageSize       = 50
	tokenExpirySkew    = 60 * time.Second
	seenMaxEntries     = 4096
)

type Adapter struct {
	tenantID     string
	clientID     string
	clientSecret string
	chatID       string // optional: restrict polling to a single chat
	pollSeconds  int
	httpClient   *http.Client
	logger       *slog.Logger

	// Overridable for tests (inject httptest server URLs).
	tokenURL  string
	graphBase string

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)

	tokMu       sync.Mutex
	accessToken string
	tokenExpiry time.Time

	seenMu sync.Mutex
	seen   map[string]struct{}
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	tenantID := strings.TrimSpace(os.Getenv("MSTEAMS_TENANT_ID"))
	pollSeconds := defaultPollSeconds
	if v := strings.TrimSpace(os.Getenv("MSTEAMS_POLL_SECONDS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			pollSeconds = n
		}
	}

	return &Adapter{
		tenantID:     tenantID,
		clientID:     strings.TrimSpace(os.Getenv("MSTEAMS_CLIENT_ID")),
		clientSecret: strings.TrimSpace(os.Getenv("MSTEAMS_CLIENT_SECRET")),
		chatID:       strings.TrimSpace(os.Getenv("MSTEAMS_CHAT_ID")),
		pollSeconds:  pollSeconds,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		logger:       logger,
		tokenURL:     fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenantID),
		graphBase:    defaultGraphBase,
		seen:         make(map[string]struct{}),
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
	if a.tenantID == "" || a.clientID == "" || a.clientSecret == "" {
		return fmt.Errorf("MSTEAMS_TENANT_ID, MSTEAMS_CLIENT_ID and MSTEAMS_CLIENT_SECRET must be set in environment")
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
		return fmt.Errorf("msteams: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("msteams: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	// Verify credentials by obtaining a token up front.
	if _, err := a.getToken(ctx); err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("msteams: token acquisition failed: %w", err)
	}

	go a.pollLoop(ctx)
	a.logger.Info("msteams: adapter started", "poll_seconds", a.pollSeconds)
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
	a.logger.Info("msteams: adapter stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" && msg.Media == nil {
		return nil
	}
	if msg.Media != nil {
		a.logger.Warn("msteams: media not supported, sending text only")
	}
	if msg.Text == "" {
		return nil
	}
	if channelID == "" {
		return fmt.Errorf("msteams: empty channel ID")
	}

	chunks := messageutil.SplitLongText(msg.Text, maxMessageBytes)
	for i, chunk := range chunks {
		if err := a.sendText(ctx, channelID, chunk); err != nil {
			return fmt.Errorf("msteams: send chunk %d/%d: %w", i+1, len(chunks), err)
		}
	}
	return nil
}

func (a *Adapter) sendText(ctx context.Context, chatID, text string) error {
	token, err := a.getToken(ctx)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	endpoint := fmt.Sprintf("%s/v1.0/chats/%s/messages", strings.TrimRight(a.graphBase, "/"), chatID)
	payload, err := json.Marshal(map[string]any{
		"body": map[string]string{
			"contentType": "html",
			"content":     text,
		},
	})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post message: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("send message failed (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

type cachedToken struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// getToken returns a cached access token, refreshing it ahead of expiry.
func (a *Adapter) getToken(ctx context.Context) (string, error) {
	a.tokMu.Lock()
	if a.accessToken != "" && time.Now().Add(tokenExpirySkew).Before(a.tokenExpiry) {
		token := a.accessToken
		a.tokMu.Unlock()
		return token, nil
	}
	a.tokMu.Unlock()

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", a.clientID)
	form.Set("client_secret", a.clientSecret)
	form.Set("scope", graphScope)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token request failed (status %d): %s", resp.StatusCode, string(body))
	}

	var tok cachedToken
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("token response missing access_token")
	}

	expiresIn := time.Duration(tok.ExpiresIn) * time.Second
	if expiresIn <= 0 {
		expiresIn = time.Hour
	}

	a.tokMu.Lock()
	a.accessToken = tok.AccessToken
	a.tokenExpiry = time.Now().Add(expiresIn)
	a.tokMu.Unlock()

	return tok.AccessToken, nil
}

func (a *Adapter) pollLoop(ctx context.Context) {
	interval := time.Duration(a.pollSeconds) * time.Second
	if interval <= 0 {
		interval = defaultPollSeconds * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := a.pollOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			a.logger.Warn("msteams: poll failed", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// pollOnce fetches recent messages for the configured chat (or every chat the
// app is a member of) and emits callbacks for unseen, non-self messages.
func (a *Adapter) pollOnce(ctx context.Context) error {
	token, err := a.getToken(ctx)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	chatIDs := []string{a.chatID}
	if a.chatID == "" {
		chatIDs, err = a.listChats(ctx, token)
		if err != nil {
			return fmt.Errorf("list chats: %w", err)
		}
	}

	for _, chatID := range chatIDs {
		messages, err := a.listChatMessages(ctx, token, chatID)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			a.logger.Warn("msteams: list chat messages failed", "chat", chatID, "error", err)
			continue
		}
		a.handleMessages(chatID, messages)
	}
	return nil
}

func (a *Adapter) listChats(ctx context.Context, token string) ([]string, error) {
	endpoint := strings.TrimRight(a.graphBase, "/") + "/v1.0/chats?$top=" + strconv.Itoa(pollPageSize)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list chats failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Value []struct {
			ID string `json:"id"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse chats response: %w", err)
	}

	ids := make([]string, 0, len(result.Value))
	for _, c := range result.Value {
		if c.ID != "" {
			ids = append(ids, c.ID)
		}
	}
	return ids, nil
}

func (a *Adapter) listChatMessages(ctx context.Context, token, chatID string) ([]chatMessage, error) {
	q := url.Values{}
	q.Set("$top", strconv.Itoa(pollPageSize))
	q.Set("$orderby", "lastModifiedDateTime desc")
	endpoint := fmt.Sprintf("%s/v1.0/chats/%s/messages?%s", strings.TrimRight(a.graphBase, "/"), chatID, q.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list messages failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Value []chatMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse messages response: %w", err)
	}
	return result.Value, nil
}

func (a *Adapter) handleMessages(chatID string, messages []chatMessage) {
	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()

	a.seenMu.Lock()
	if len(a.seen) > seenMaxEntries {
		a.seen = make(map[string]struct{})
	}
	defer a.seenMu.Unlock()

	for _, m := range messages {
		if m.ID == "" {
			continue
		}
		if _, dup := a.seen[m.ID]; dup {
			continue
		}
		a.seen[m.ID] = struct{}{}

		// Only messages sent by an application or a user are relevant.
		if m.From == nil || (m.From.Application == nil && m.From.User == nil) {
			continue
		}
		sender := m.From.User
		if m.From.Application != nil {
			sender = m.From.Application
			// Skip our own application's messages.
			if sender.ID == a.clientID {
				continue
			}
		}

		userID := sender.ID
		incoming := plugin.IncomingMessage{
			Platform:  PlatformName,
			ChannelID: chatID,
			UserID:    userID,
			UserName:  sender.DisplayName,
			Text:      m.Body.Content,
			Timestamp: time.Now(),
			Raw:       m,
		}
		if ts, err := time.Parse(time.RFC3339, m.LastModifiedDateTime); err == nil {
			incoming.Timestamp = ts
		}

		if callback != nil {
			callback(incoming)
		}
	}
}

type chatMessage struct {
	ID                   string    `json:"id"`
	LastModifiedDateTime string    `json:"lastModifiedDateTime"`
	From                 *chatFrom `json:"from,omitempty"`
	Body                 chatBody  `json:"body"`
}

type chatFrom struct {
	Application *chatIdentity `json:"application,omitempty"`
	User        *chatIdentity `json:"user,omitempty"`
}

type chatIdentity struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type chatBody struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}
