package matrix

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
	PlatformName = "matrix"
	pollInterval = 5 * time.Second
	maxRetries   = 3
)

type Adapter struct {
	homeserver  string
	accessToken string
	userID      string
	httpClient  *http.Client
	logger      *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	nextBatch string
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	return &Adapter{
		homeserver:  strings.TrimSpace(os.Getenv("MATRIX_HOMESERVER")),
		accessToken: strings.TrimSpace(os.Getenv("MATRIX_ACCESS_TOKEN")),
		userID:      strings.TrimSpace(os.Getenv("MATRIX_USER_ID")),
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
	if a.homeserver == "" || a.accessToken == "" || a.userID == "" {
		return fmt.Errorf("MATRIX_HOMESERVER, MATRIX_ACCESS_TOKEN and MATRIX_USER_ID must be set in environment")
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
		return fmt.Errorf("matrix: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("matrix: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	go a.syncLoop(ctx)

	a.logger.Info("matrix: started", "homeserver", a.homeserver, "user", a.userID)
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
	a.logger.Info("matrix: stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	url := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/send/m.room.message", a.homeserver, channelID)
	body := map[string]any{
		"msgtype": "m.text",
		"body":    msg.Text,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.accessToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("matrix: send failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

func (a *Adapter) syncLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		events, batch, err := a.sync(ctx)
		if err != nil {
			a.logger.Warn("matrix: sync failed", "error", err)
			time.Sleep(pollInterval)
			continue
		}
		a.nextBatch = batch

		for _, ev := range events {
			if ev.Sender == a.userID {
				continue
			}
			if ev.Type != "m.room.message" {
				continue
			}
			text := ""
			if body, ok := ev.Content["body"].(string); ok {
				text = body
			}
			if text == "" {
				continue
			}

			a.mu.Lock()
			callback := a.onMessage
			a.mu.Unlock()
			if callback != nil {
				callback(plugin.IncomingMessage{
					Platform:  PlatformName,
					ChannelID: ev.RoomID,
					UserID:    ev.Sender,
					UserName:  ev.Sender,
					Text:      text,
					Timestamp: time.Now(),
				})
			}
		}
		time.Sleep(pollInterval)
	}
}

type matrixEvent struct {
	Type    string         `json:"type"`
	Sender  string         `json:"sender"`
	RoomID  string         `json:"room_id"`
	Content map[string]any `json:"content"`
}

func (a *Adapter) sync(ctx context.Context) ([]matrixEvent, string, error) {
	url := fmt.Sprintf("%s/_matrix/client/v3/sync?timeout=30000", a.homeserver)
	if a.nextBatch != "" {
		url += "&since=" + a.nextBatch
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.accessToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	var syncResp struct {
		NextBatch string `json:"next_batch"`
		Rooms     struct {
			Join map[string]struct {
				Timeline struct {
					Events []matrixEvent `json:"events"`
				} `json:"timeline"`
			} `json:"join"`
		} `json:"rooms"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&syncResp); err != nil {
		return nil, "", err
	}

	var events []matrixEvent
	for roomID, room := range syncResp.Rooms.Join {
		for _, ev := range room.Timeline.Events {
			ev.RoomID = roomID
			events = append(events, ev)
		}
	}

	return events, syncResp.NextBatch, nil
}
