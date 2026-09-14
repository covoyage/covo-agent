package msgraph

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
)

const (
	PlatformName = "msgraph"
	graphScope   = "https://graph.microsoft.com/.default"

	defaultGraphBase = "https://graph.microsoft.com"

	defaultPollSeconds = 60
	mailPageSize       = 25
	tokenExpirySkew    = 60 * time.Second
	seenMaxEntries     = 4096
)

type Adapter struct {
	tenantID     string
	clientID     string
	clientSecret string
	mailbox      string // sending mailbox (user principal name)
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

	tenantID := strings.TrimSpace(os.Getenv("MSGRAPH_TENANT_ID"))
	pollSeconds := defaultPollSeconds
	if v := strings.TrimSpace(os.Getenv("MSGRAPH_POLL_SECONDS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			pollSeconds = n
		}
	}

	mailbox := strings.TrimSpace(os.Getenv("MSGRAPH_MAILBOX"))
	if mailbox == "" {
		// Legacy env name kept as fallback.
		mailbox = strings.TrimSpace(os.Getenv("MSGRAPH_USER_ID"))
	}

	return &Adapter{
		tenantID:     tenantID,
		clientID:     strings.TrimSpace(os.Getenv("MSGRAPH_CLIENT_ID")),
		clientSecret: strings.TrimSpace(os.Getenv("MSGRAPH_CLIENT_SECRET")),
		mailbox:      mailbox,
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
		return fmt.Errorf("MSGRAPH_TENANT_ID, MSGRAPH_CLIENT_ID and MSGRAPH_CLIENT_SECRET must be set in environment")
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
		return fmt.Errorf("msgraph: validation failed: %w", err)
	}
	if a.mailbox == "" {
		return fmt.Errorf("msgraph: MSGRAPH_MAILBOX not set in environment")
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("msgraph: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	// Verify credentials by obtaining a token up front.
	if _, err := a.getToken(ctx); err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("msgraph: token acquisition failed: %w", err)
	}

	go a.pollLoop(ctx)
	a.logger.Info("msgraph: adapter started", "mailbox", a.mailbox, "poll_seconds", a.pollSeconds)
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
	a.logger.Info("msgraph: adapter stopped")
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
		a.logger.Warn("msgraph: media not supported, sending text only")
	}
	if msg.Text == "" {
		return nil
	}
	if a.mailbox == "" {
		return fmt.Errorf("msgraph: MSGRAPH_MAILBOX not set in environment")
	}
	if channelID == "" {
		return fmt.Errorf("msgraph: empty recipient address")
	}

	if err := a.sendMail(ctx, channelID, msg); err != nil {
		return fmt.Errorf("msgraph: send mail: %w", err)
	}
	return nil
}

func (a *Adapter) sendMail(ctx context.Context, recipient string, msg plugin.OutgoingMessage) error {
	token, err := a.getToken(ctx)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	message := map[string]any{
		"body": map[string]string{
			"contentType": "Text",
			"content":     msg.Text,
		},
		"toRecipients": []map[string]any{
			{"emailAddress": map[string]string{"address": recipient}},
		},
	}
	// ParseMode is repurposed as an optional mail subject.
	if subject := strings.TrimSpace(msg.ParseMode); subject != "" {
		message["subject"] = subject
	}
	payload, err := json.Marshal(map[string]any{
		"message":         message,
		"saveToSentItems": true,
	})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/v1.0/users/%s/sendMail", strings.TrimRight(a.graphBase, "/"), a.mailbox)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post sendMail: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	// Graph returns 202 Accepted for sendMail.
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sendMail failed (status %d): %s", resp.StatusCode, string(body))
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
			a.logger.Warn("msgraph: poll failed", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// pollOnce lists the newest inbox messages and emits callbacks for unseen
// messages that were not sent by the monitored mailbox itself.
func (a *Adapter) pollOnce(ctx context.Context) error {
	token, err := a.getToken(ctx)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	messages, err := a.listInboxMessages(ctx, token)
	if err != nil {
		return fmt.Errorf("list inbox messages: %w", err)
	}
	a.handleMessages(messages)
	return nil
}

func (a *Adapter) listInboxMessages(ctx context.Context, token string) ([]mailMessage, error) {
	q := url.Values{}
	q.Set("$top", strconv.Itoa(mailPageSize))
	q.Set("$orderby", "receivedDateTime desc")
	q.Set("$select", "id,from,subject,bodyPreview,receivedDateTime")
	endpoint := fmt.Sprintf("%s/v1.0/users/%s/mailFolders/inbox/messages?%s",
		strings.TrimRight(a.graphBase, "/"), a.mailbox, strings.ReplaceAll(q.Encode(), "+", "%20"))

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
		Value []mailMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse messages response: %w", err)
	}
	return result.Value, nil
}

func (a *Adapter) handleMessages(messages []mailMessage) {
	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()

	a.seenMu.Lock()
	if len(a.seen) > seenMaxEntries {
		a.seen = make(map[string]struct{})
	}
	defer a.seenMu.Unlock()

	for _, m := range messages {
		if m.ID == "" || m.From == nil || m.From.EmailAddress == nil {
			continue
		}
		if _, dup := a.seen[m.ID]; dup {
			continue
		}
		a.seen[m.ID] = struct{}{}

		from := m.From.EmailAddress.Address
		if from == "" || strings.EqualFold(from, a.mailbox) {
			continue
		}

		text := m.Subject + "\n\n" + m.BodyPreview
		incoming := plugin.IncomingMessage{
			Platform:  PlatformName,
			ChannelID: from,
			UserID:    from,
			UserName:  m.From.EmailAddress.Name,
			Text:      text,
			Timestamp: time.Now(),
			Raw:       m,
		}
		if ts, err := time.Parse(time.RFC3339, m.ReceivedDateTime); err == nil {
			incoming.Timestamp = ts
		}

		if callback != nil {
			callback(incoming)
		}
	}
}

type mailMessage struct {
	ID               string    `json:"id"`
	Subject          string    `json:"subject"`
	BodyPreview      string    `json:"bodyPreview"`
	ReceivedDateTime string    `json:"receivedDateTime"`
	From             *mailFrom `json:"from,omitempty"`
}

type mailFrom struct {
	EmailAddress *mailAddress `json:"emailAddress"`
}

type mailAddress struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}
