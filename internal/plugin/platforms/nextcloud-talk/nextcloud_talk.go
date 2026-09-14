package nextcloudtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
)

const (
	PlatformName = "nextcloud-talk"

	ocsBasePath       = "/ocs/v2.php"
	capabilitiesPath  = ocsBasePath + "/cloud/capabilities"
	chatBasePath      = ocsBasePath + "/apps/spreed/api/v1/chat"
	maxMessageLen     = 32000
	pollServerTimeout = 30 // seconds, Nextcloud long-poll timeout
	pollRequestLimit  = 35 * time.Second
	pollRetryInterval = 2 * time.Second
)

type Adapter struct {
	baseURL    string
	username   string
	password   string
	roomsEnv   string
	httpClient *http.Client // sends + capabilities
	pollClient *http.Client // long-poll: no global timeout, ctx governs
	logger     *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	return &Adapter{
		baseURL:    strings.TrimRight(strings.TrimSpace(os.Getenv("NEXTCLOUD_SERVER_URL")), "/"),
		username:   strings.TrimSpace(os.Getenv("NEXTCLOUD_USER")),
		password:   strings.TrimSpace(os.Getenv("NEXTCLOUD_PASS")),
		roomsEnv:   strings.TrimSpace(os.Getenv("NEXTCLOUD_ROOM")),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		pollClient: &http.Client{},
		logger:     logger,
	}
}

func (a *Adapter) rooms() []string {
	var rooms []string
	for _, r := range strings.Split(a.roomsEnv, ",") {
		if r = strings.TrimSpace(r); r != "" {
			rooms = append(rooms, r)
		}
	}
	return rooms
}

func (a *Adapter) Name() string                      { return PlatformName }
func (a *Adapter) GetName() string                   { return PlatformName }
func (a *Adapter) GetID() string                     { return PlatformName }
func (a *Adapter) Category() plugin.Category         { return plugin.CategoryPlatform }
func (a *Adapter) GetCategory() plugin.Category      { return plugin.CategoryPlatform }
func (a *Adapter) ID() string                        { return PlatformName }
func (a *Adapter) Platform() plugin.PlatformProvider { return a }

func (a *Adapter) Validate() error {
	if a.baseURL == "" {
		return fmt.Errorf("NEXTCLOUD_SERVER_URL not set in environment")
	}
	if a.username == "" {
		return fmt.Errorf("NEXTCLOUD_USER not set in environment")
	}
	if a.password == "" {
		return fmt.Errorf("NEXTCLOUD_PASS not set in environment")
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
		return fmt.Errorf("nextcloud-talk: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("nextcloud-talk: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	if err := a.checkCapabilities(ctx); err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("nextcloud-talk: capabilities check failed: %w", err)
	}

	rooms := a.rooms()
	a.logger.Info("nextcloud-talk: adapter started", "url", a.baseURL, "rooms", len(rooms))
	for _, room := range rooms {
		go a.roomLoop(ctx, room)
	}
	return nil
}

func (a *Adapter) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.running {
		return nil
	}
	a.running = false
	if a.cancel != nil {
		a.cancel()
	}
	a.logger.Info("nextcloud-talk: adapter stopped")
	return nil
}

// checkCapabilities verifies server connectivity and credentials.
func (a *Adapter) checkCapabilities(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", a.baseURL+capabilitiesPath, nil)
	if err != nil {
		return fmt.Errorf("nextcloud-talk: create capabilities request: %w", err)
	}
	a.setOCSHeaders(req)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("nextcloud-talk: capabilities request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("nextcloud-talk: capabilities failed (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

func (a *Adapter) setOCSHeaders(req *http.Request) {
	req.SetBasicAuth(a.username, a.password)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Accept", "application/json")
}

// roomLoop long-polls a single room's chat endpoint until ctx is cancelled.
// The first round runs without lookIntoFuture to seed the cursor so old
// messages are not replayed.
func (a *Adapter) roomLoop(ctx context.Context, roomToken string) {
	lastID := int64(0)
	baseline := true
	for {
		if ctx.Err() != nil {
			return
		}
		msgs, nextLast, err := a.fetchNewMessages(ctx, roomToken, lastID, !baseline)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			a.logger.Warn("nextcloud-talk: poll failed", "room", roomToken, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollRetryInterval):
			}
			continue
		}
		if !baseline {
			for _, in := range msgs {
				a.callback(in)
			}
		}
		baseline = false
		lastID = nextLast
	}
}

// fetchNewMessages performs one poll round against the room and returns the
// messages to forward plus the new lastKnownMessageId cursor. When
// lookIntoFuture is false a history round is performed instead.
func (a *Adapter) fetchNewMessages(ctx context.Context, roomToken string, lastID int64, lookIntoFuture bool) ([]plugin.IncomingMessage, int64, error) {
	params := url.Values{}
	if lookIntoFuture {
		params.Set("lookIntoFuture", "1")
		params.Set("timeout", fmt.Sprintf("%d", pollServerTimeout))
	}
	if lastID > 0 {
		params.Set("lastKnownMessageId", fmt.Sprintf("%d", lastID))
	}

	reqCtx, cancel := context.WithTimeout(ctx, pollRequestLimit)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "GET", a.baseURL+chatBasePath+"/"+url.PathEscape(roomToken), nil)
	if err != nil {
		return nil, lastID, fmt.Errorf("nextcloud-talk: create chat request: %w", err)
	}
	req.URL.RawQuery = params.Encode()
	a.setOCSHeaders(req)

	resp, err := a.pollClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, lastID, ctx.Err()
		}
		return nil, lastID, fmt.Errorf("nextcloud-talk: chat request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, lastID, fmt.Errorf("nextcloud-talk: read chat response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, lastID, fmt.Errorf("nextcloud-talk: chat poll failed (status %d): %s", resp.StatusCode, string(body))
	}

	var ocsResp struct {
		OCS struct {
			Data []ocsChatMessage `json:"data"`
		} `json:"ocs"`
	}
	if err := json.Unmarshal(body, &ocsResp); err != nil {
		return nil, lastID, fmt.Errorf("nextcloud-talk: parse chat response: %w", err)
	}

	var msgs []plugin.IncomingMessage
	maxID := lastID
	for _, m := range ocsResp.OCS.Data {
		if m.ID <= maxID {
			continue
		}
		maxID = m.ID
		if m.MessageType != "comment" || m.Message == "" || m.ActorID == a.username {
			continue
		}
		msgs = append(msgs, plugin.IncomingMessage{
			Platform:  PlatformName,
			ChannelID: roomToken,
			UserID:    m.ActorID,
			UserName:  m.ActorDisplayName,
			Text:      m.Message,
			Timestamp: time.Now(),
			Raw:       m,
		})
	}
	return msgs, maxID, nil
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
		if err := a.postChatMessage(ctx, channelID, chunk); err != nil {
			return fmt.Errorf("nextcloud-talk: send chunk %d/%d: %w", i+1, len(chunks), err)
		}
	}
	return nil
}

func (a *Adapter) postChatMessage(ctx context.Context, roomToken, text string) error {
	form := url.Values{"message": {text}}

	req, err := http.NewRequestWithContext(ctx, "POST",
		a.baseURL+chatBasePath+"/"+url.PathEscape(roomToken), strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("nextcloud-talk: create send request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	a.setOCSHeaders(req)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("nextcloud-talk: send request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("nextcloud-talk: send message failed (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
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

type ocsChatMessage struct {
	ID                int64          `json:"id"`
	Token             string         `json:"token"`
	ActorID           string         `json:"actorId"`
	ActorType         string         `json:"actorType"`
	ActorDisplayName  string         `json:"actorDisplayName"`
	Message           string         `json:"message"`
	MessageType       string         `json:"messageType"`
	MessageParameters map[string]any `json:"messageParameters,omitempty"`
}
