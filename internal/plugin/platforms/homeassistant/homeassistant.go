// Package homeassistant implements the Home Assistant platform adapter.
//
// Outbound messages are delivered via the Home Assistant REST API by
// calling the notify service (POST {HA_URL}/api/services/{HA_NOTIFIER}).
// Start verifies the connection against GET {HA_URL}/api/ and then keeps a
// websocket connection to {HA_URL}/api/websocket open, subscribing to
// mobile_app_notification_action and call_service events as an inbound
// command entry point. The websocket reconnects with exponential backoff.
package homeassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	PlatformName     = "homeassistant"
	defaultServerURL = "http://homeassistant.local:8123"
	defaultNotifier  = "notify.persistent_notification"
	wsReadTimeout    = 15 * time.Second
	maxBackoff       = 30 * time.Second
)

type Adapter struct {
	serverURL   string
	accessToken string
	notifier    string
	httpClient  *http.Client
	logger      *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	serverURL := strings.TrimSpace(os.Getenv("HA_URL"))
	if serverURL == "" {
		serverURL = defaultServerURL
	}
	notifier := strings.TrimSpace(os.Getenv("HA_NOTIFIER"))
	if notifier == "" {
		notifier = defaultNotifier
	}

	return &Adapter{
		serverURL:   strings.TrimRight(serverURL, "/"),
		accessToken: strings.TrimSpace(os.Getenv("HA_TOKEN")),
		notifier:    notifier,
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
	if a.accessToken == "" {
		return fmt.Errorf("HA_TOKEN not set in environment")
	}
	if a.serverURL == "" {
		return fmt.Errorf("HA_URL not set in environment")
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
		return fmt.Errorf("homeassistant: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("homeassistant: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	if err := a.verifyConnection(ctx); err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("homeassistant: %w", err)
	}

	go a.wsLoop(ctx)

	a.logger.Info("homeassistant: adapter started", "url", a.serverURL, "notifier", a.notifier)
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
	a.logger.Info("homeassistant: adapter stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

// SendMessage calls the configured HA notify service. channelID maps to the
// optional "target" field and msg.ParseMode (when set) to "title".
func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" {
		return nil
	}

	payload := map[string]any{"message": msg.Text}
	if msg.ParseMode != "" {
		payload["title"] = msg.ParseMode
	}
	if channelID != "" {
		payload["target"] = []string{channelID}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("homeassistant: marshal payload: %w", err)
	}

	servicePath := strings.ReplaceAll(a.notifier, ".", "/")
	url := fmt.Sprintf("%s/api/services/%s", a.serverURL, servicePath)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("homeassistant: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.accessToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("homeassistant: call service %s: %w", a.notifier, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("homeassistant: call service %s failed (status %d): %s", a.notifier, resp.StatusCode, string(body))
	}
	return nil
}

// verifyConnection checks the REST API root endpoint with the access token.
func (a *Adapter) verifyConnection(ctx context.Context) error {
	url := a.serverURL + "/api/"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.accessToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("api check failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("authentication failed (401): check HA_TOKEN")
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("api endpoint not found (404): check HA_URL")
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("api check failed (status %d)", resp.StatusCode)
	}
	return nil
}

// wsLoop keeps the websocket connection alive, reconnecting with
// exponential backoff (capped at maxBackoff) until ctx is cancelled.
func (a *Adapter) wsLoop(ctx context.Context) {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}

		err := a.runWS(ctx)
		if ctx.Err() != nil {
			return
		}
		attempt++
		shift := attempt
		if shift > 5 {
			shift = 5
		}
		delay := (1 << shift) * time.Second
		if delay > maxBackoff {
			delay = maxBackoff
		}
		a.logger.Warn("homeassistant: websocket disconnected, reconnecting", "error", err, "attempt", attempt, "backoff", delay)

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// runWS dials the HA websocket, authenticates with the long-lived access
// token, subscribes to the command-entry events and processes messages
// until the connection breaks or ctx is cancelled.
func (a *Adapter) runWS(ctx context.Context) error {
	wsURL := strings.Replace(a.serverURL, "http", "ws", 1) + "/api/websocket"

	dialCtx, cancelDial := context.WithTimeout(ctx, 15*time.Second)
	conn, br, _, err := ws.Dial(dialCtx, wsURL)
	cancelDial()
	if err != nil {
		return fmt.Errorf("dial %s: %w", wsURL, err)
	}
	defer conn.Close()

	// Frames may already be buffered in the handshake reader; drain it
	// before reading from the raw connection. Control-frame replies are
	// written back to conn. ws.Dial can return a nil reader (e.g. when the
	// server consumed the handshake), so guard against that.
	var frameSrc io.Reader = conn
	if br != nil {
		frameSrc = io.MultiReader(br, conn)
	}
	frames := &wsReadWriter{r: frameSrc, w: conn}

	// Close the connection when ctx is cancelled so blocking reads unblock.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	// Handshake: HA sends auth_required, we answer auth, HA confirms auth_ok.
	conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	var hello wsMessage
	if err := readWSMessage(frames, &hello); err != nil {
		return fmt.Errorf("read auth_required: %w", err)
	}
	if hello.Type != "auth_required" {
		return fmt.Errorf("expected auth_required, got %q", hello.Type)
	}

	if err := writeWSMessage(conn, wsMessage{Type: "auth", AccessToken: a.accessToken}); err != nil {
		return fmt.Errorf("send auth: %w", err)
	}

	var authResp wsMessage
	if err := readWSMessage(frames, &authResp); err != nil {
		return fmt.Errorf("read auth response: %w", err)
	}
	if authResp.Type != "auth_ok" {
		return fmt.Errorf("authentication rejected: %s", authResp.Type)
	}
	// Authenticated: clear the handshake deadline.
	conn.SetReadDeadline(time.Time{})

	nextID := 1
	for _, eventType := range []string{"mobile_app_notification_action", "call_service"} {
		id := nextID
		nextID++
		if err := writeWSMessage(conn, wsMessage{
			ID:        id,
			Type:      "subscribe_events",
			EventType: eventType,
		}); err != nil {
			return fmt.Errorf("subscribe %s: %w", eventType, err)
		}

		// Wait for the matching result frame and fail loudly on
		// success=false (e.g. insufficient permissions) instead of
		// silently ignoring it; returning an error makes wsLoop
		// reconnect with backoff.
		conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		err := a.awaitSubscribeAck(frames, id, eventType)
		conn.SetReadDeadline(time.Time{})
		if err != nil {
			return err
		}
	}

	for {
		var msg wsMessage
		if err := readWSMessage(frames, &msg); err != nil {
			return fmt.Errorf("read event: %w", err)
		}
		if msg.Type == "event" {
			a.handleEvent(msg.Event)
		}
		// "pong" messages are ignored.
	}
}

// awaitSubscribeAck reads messages until the result frame for the given
// subscription id arrives. Events seen along the way are dispatched; any
// other frames are skipped. A result with success=false (or missing
// success) aborts the connection with a descriptive error.
func (a *Adapter) awaitSubscribeAck(frames io.ReadWriter, id int, eventType string) error {
	for {
		var msg wsMessage
		if err := readWSMessage(frames, &msg); err != nil {
			return fmt.Errorf("read subscribe ack for %s: %w", eventType, err)
		}
		switch {
		case msg.Type == "event":
			a.handleEvent(msg.Event)
		case msg.Type == "result" && msg.ID == id:
			if msg.Success == nil || !*msg.Success {
				detail := ""
				if msg.Error != nil {
					detail = fmt.Sprintf(" (code=%v message=%s)", msg.Error.Code, msg.Error.Message)
				}
				return fmt.Errorf("subscribe_events %s rejected by Home Assistant%s; check token permissions", eventType, detail)
			}
			return nil
		default:
			// Not our ack (other ids, pong, ...) — keep waiting.
		}
	}
}

// handleEvent converts a HA event into an incoming message. Only
// mobile_app_notification_action and call_service (domain notify, service
// covo_agent) events carry commands.
func (a *Adapter) handleEvent(ev *wsEvent) {
	if ev == nil {
		return
	}

	var text string
	switch ev.EventType {
	case "mobile_app_notification_action":
		if action, ok := ev.Data["action"].(string); ok {
			text = action
		} else if message, ok := ev.Data["message"].(string); ok {
			text = message
		}
	case "call_service":
		domain, _ := ev.Data["domain"].(string)
		service, _ := ev.Data["service"].(string)
		if domain != "notify" || service != "covo_agent" {
			return
		}
		if serviceData, ok := ev.Data["service_data"].(map[string]any); ok {
			text, _ = serviceData["message"].(string)
		}
	default:
		return
	}
	if text == "" {
		return
	}

	// Prefer a sender/source identity from the event data, if present.
	identity := "homeassistant"
	for _, key := range []string{"sender", "source"} {
		if v, ok := ev.Data[key].(string); ok && v != "" {
			identity = v
			break
		}
	}

	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()
	if callback != nil {
		callback(plugin.IncomingMessage{
			Platform:  PlatformName,
			ChannelID: identity,
			UserID:    identity,
			UserName:  PlatformName,
			Text:      text,
			Timestamp: time.Now(),
			Raw:       ev,
		})
	}
}

// wsReadWriter reads frames from r (handshake buffer + conn) and writes
// control-frame replies to w (conn), satisfying io.ReadWriter as required
// by wsutil.
type wsReadWriter struct {
	r io.Reader
	w io.Writer
}

func (rw *wsReadWriter) Read(p []byte) (int, error)  { return rw.r.Read(p) }
func (rw *wsReadWriter) Write(p []byte) (int, error) { return rw.w.Write(p) }

// readWSMessage reads one websocket text frame and decodes it as JSON.
func readWSMessage(r io.ReadWriter, msg *wsMessage) error {
	data, err := wsutil.ReadServerText(r)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, msg); err != nil {
		return fmt.Errorf("decode message: %w", err)
	}
	return nil
}

// writeWSMessage encodes msg as JSON and writes it as a websocket text
// frame to the HA server.
func writeWSMessage(conn net.Conn, msg wsMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}
	return wsutil.WriteClientText(conn, data)
}

// wsMessage is the subset of the HA websocket API message schema used here.
// See https://developers.home-assistant.io/docs/api/websocket.
type wsMessage struct {
	Type        string   `json:"type"`
	ID          int      `json:"id,omitempty"`
	EventType   string   `json:"event_type,omitempty"`
	AccessToken string   `json:"access_token,omitempty"`
	Success     *bool    `json:"success,omitempty"`
	Error       *wsError `json:"error,omitempty"`
	Event       *wsEvent `json:"event,omitempty"`
}

// wsError is the error payload of a failed command result frame.
type wsError struct {
	Code    any    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// wsEvent is the payload of a "type":"event" message.
type wsEvent struct {
	EventType string         `json:"event_type"`
	Data      map[string]any `json:"data,omitempty"`
}
