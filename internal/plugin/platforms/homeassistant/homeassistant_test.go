package homeassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestAdapter returns an adapter whose serverURL points at the given
// test server (injected via the unexported serverURL field).
func newTestAdapter(t *testing.T, handler http.Handler) (*Adapter, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a := &Adapter{
		serverURL:   srv.URL,
		accessToken: "ha-token",
		notifier:    defaultNotifier,
		httpClient:  &http.Client{Timeout: 5 * time.Second},
		logger:      testLogger(),
	}
	return a, srv
}

func TestValidate(t *testing.T) {
	a := &Adapter{}
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "HA_TOKEN") {
		t.Fatalf("expected HA_TOKEN error, got %v", err)
	}
	a.accessToken = "tok"
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "HA_URL") {
		t.Fatalf("expected HA_URL error, got %v", err)
	}
	a.serverURL = "http://ha:8123"
	if err := a.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestSendMessageSuccess(t *testing.T) {
	var mu sync.Mutex
	var gotAuth string
	var payload map[string]any

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/services/notify/persistent_notification" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.WriteHeader(http.StatusNoContent)
	}))

	if err := a.SendMessage(context.Background(), "groupliving", plugin.OutgoingMessage{Text: "lights on", ParseMode: "Motion Detected"}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer ha-token" {
		t.Errorf("authorization = %q, want Bearer ha-token", gotAuth)
	}
	if payload["message"] != "lights on" {
		t.Errorf("message = %v, want lights on", payload["message"])
	}
	if payload["title"] != "Motion Detected" {
		t.Errorf("title = %v, want Motion Detected", payload["title"])
	}
	target, ok := payload["target"].([]any)
	if !ok || len(target) != 1 || target[0] != "groupliving" {
		t.Errorf("target = %v, want [groupliving]", payload["target"])
	}
}

func TestSendMessageNoTargetNoTitle(t *testing.T) {
	var mu sync.Mutex
	var payload map[string]any

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.WriteHeader(http.StatusNoContent)
	}))

	if err := a.SendMessage(context.Background(), "", plugin.OutgoingMessage{Text: "plain"}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if payload["message"] != "plain" {
		t.Errorf("message = %v, want plain", payload["message"])
	}
	if _, ok := payload["title"]; ok {
		t.Errorf("unexpected title: %v", payload["title"])
	}
	if _, ok := payload["target"]; ok {
		t.Errorf("unexpected target: %v", payload["target"])
	}
}

func TestSendMessageError(t *testing.T) {
	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("Unauthorized"))
	}))

	err := a.SendMessage(context.Background(), "", plugin.OutgoingMessage{Text: "hi"})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected error containing status 401, got %v", err)
	}
}

func TestStartVerifyConnectionFailure(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusNotFound} {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		})
		a, _ := newTestAdapter(t, mux)

		err := a.Start(context.Background())
		if err == nil {
			t.Fatalf("code %d: expected Start to fail", code)
		}
		if code == http.StatusUnauthorized && !strings.Contains(err.Error(), "authentication failed") {
			t.Errorf("code 401: unexpected error: %v", err)
		}
		if code == http.StatusNotFound && !strings.Contains(err.Error(), "not found") {
			t.Errorf("code 404: unexpected error: %v", err)
		}
		a.Stop()
	}
}

func TestHandleEventCallService(t *testing.T) {
	a := &Adapter{logger: testLogger()}
	callbacks := make(chan plugin.IncomingMessage, 4)
	a.OnMessage(func(m plugin.IncomingMessage) { callbacks <- m })

	// Matching domain/service forwards the message.
	a.handleEvent(&wsEvent{
		EventType: "call_service",
		Data: map[string]any{
			"domain":       "notify",
			"service":      "covo_agent",
			"service_data": map[string]any{"message": "run cleanup"},
			"sender":       "pixel7",
		},
	})
	select {
	case m := <-callbacks:
		if m.Text != "run cleanup" || m.ChannelID != "pixel7" {
			t.Errorf("unexpected message: %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for callback")
	}

	// Non-notify domain is ignored.
	a.handleEvent(&wsEvent{
		EventType: "call_service",
		Data:      map[string]any{"domain": "homeassistant", "service": "covo_agent", "service_data": map[string]any{"message": "nope"}},
	})
	// notify/covo_agent without message text is ignored.
	a.handleEvent(&wsEvent{
		EventType: "call_service",
		Data:      map[string]any{"domain": "notify", "service": "covo_agent", "service_data": map[string]any{}},
	})
	select {
	case m := <-callbacks:
		t.Errorf("unexpected callback: %+v", m)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestHandleEventNotificationAction(t *testing.T) {
	a := &Adapter{logger: testLogger()}
	callbacks := make(chan plugin.IncomingMessage, 4)
	a.OnMessage(func(m plugin.IncomingMessage) { callbacks <- m })

	a.handleEvent(&wsEvent{
		EventType: "mobile_app_notification_action",
		Data:      map[string]any{"action": "arm_alarm", "source": "dashboard"},
	})
	select {
	case m := <-callbacks:
		if m.Text != "arm_alarm" || m.ChannelID != "dashboard" {
			t.Errorf("unexpected message: %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for callback")
	}

	// Unrelated event types are ignored.
	a.handleEvent(&wsEvent{EventType: "state_changed", Data: map[string]any{"action": "nope"}})
	select {
	case m := <-callbacks:
		t.Errorf("unexpected callback: %+v", m)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestWebsocketFlow runs Start against a fake HA server that serves the REST
// verification endpoint and performs the websocket auth/subscribe/event
// handshake.
func TestWebsocketFlow(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ha-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"message":"API running."}`))
	})

	var wsErr error
	var wsMu sync.Mutex
	mux.HandleFunc("/api/websocket", func(w http.ResponseWriter, r *http.Request) {
		conn, rw, _, err := ws.UpgradeHTTP(r, w)
		if err != nil {
			return
		}
		defer conn.Close()

		writeServerText(t, conn, `{"type":"auth_required","ha_version":"2026.1.0"}`)

		authData, err := wsutil.ReadClientText(rw)
		if err != nil {
			wsMu.Lock()
			wsErr = err
			wsMu.Unlock()
			return
		}
		var auth wsMessage
		if err := json.Unmarshal(authData, &auth); err != nil || auth.Type != "auth" || auth.AccessToken != "ha-token" {
			wsMu.Lock()
			wsErr = err
			wsMu.Unlock()
			return
		}

		writeServerText(t, conn, `{"type":"auth_ok","ha_version":"2026.1.0"}`)

		// The adapter subscribes to both event types with ids 1 and 2 and
		// waits for each result ack before proceeding.
		for i := 1; i <= 2; i++ {
			subData, err := wsutil.ReadClientText(rw)
			if err != nil {
				wsMu.Lock()
				wsErr = err
				wsMu.Unlock()
				return
			}
			var sub wsMessage
			if err := json.Unmarshal(subData, &sub); err != nil || sub.Type != "subscribe_events" || sub.ID != i {
				wsMu.Lock()
				wsErr = fmt.Errorf("unexpected subscribe frame: %s", subData)
				wsMu.Unlock()
				return
			}
			writeServerText(t, conn, fmt.Sprintf(`{"id":%d,"type":"result","success":true}`, i))
		}

		writeServerText(t, conn, `{"id":1,"type":"event","event":{"event_type":"mobile_app_notification_action","data":{"action":"arm_alarm","sender":"pixel7"}}}`)

		// Keep the connection open until the client goes away.
		for {
			if _, err := wsutil.ReadClientText(rw); err != nil {
				return
			}
		}
	})

	a, _ := newTestAdapter(t, mux)
	a.logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	callbacks := make(chan plugin.IncomingMessage, 4)
	a.OnMessage(func(m plugin.IncomingMessage) { callbacks <- m })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer a.Stop()

	select {
	case m := <-callbacks:
		if m.Text != "arm_alarm" {
			t.Errorf("text = %q, want arm_alarm", m.Text)
		}
		if m.ChannelID != "pixel7" {
			t.Errorf("channelID = %q, want pixel7", m.ChannelID)
		}
		if m.Platform != PlatformName {
			t.Errorf("platform = %q, want %q", m.Platform, PlatformName)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for websocket event callback")
	}

	wsMu.Lock()
	defer wsMu.Unlock()
	if wsErr != nil {
		t.Errorf("websocket server error: %v", wsErr)
	}
}

func writeServerText(t *testing.T, conn io.Writer, payload string) {
	t.Helper()
	if err := wsutil.WriteServerText(conn, []byte(payload)); err != nil {
		t.Errorf("write server text failed: %v", err)
	}
}

// TestSubscribeAckFailureReconnects verifies that a subscribe_events result
// with success=false aborts the websocket session (with a logged reason)
// and triggers a reconnect that then succeeds.
func TestSubscribeAckFailureReconnects(t *testing.T) {
	var conns atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"message":"API running."}`))
	})
	mux.HandleFunc("/api/websocket", func(w http.ResponseWriter, r *http.Request) {
		conn, rw, _, err := ws.UpgradeHTTP(r, w)
		if err != nil {
			return
		}
		defer conn.Close()
		n := conns.Add(1)

		writeServerText(t, conn, `{"type":"auth_required","ha_version":"2026.1.0"}`)
		authData, err := wsutil.ReadClientText(rw)
		if err != nil {
			return
		}
		var auth wsMessage
		if err := json.Unmarshal(authData, &auth); err != nil || auth.Type != "auth" || auth.AccessToken != "ha-token" {
			return
		}
		writeServerText(t, conn, `{"type":"auth_ok","ha_version":"2026.1.0"}`)

		// First connection: reject the first subscription.
		if n == 1 {
			if _, err := wsutil.ReadClientText(rw); err != nil {
				return
			}
			writeServerText(t, conn, `{"id":1,"type":"result","success":false,"error":{"code":"unauthorized","message":"event type not allowed"}}`)
			// Stay open until the client hangs up.
			for {
				if _, err := wsutil.ReadClientText(rw); err != nil {
					return
				}
			}
		}

		// Second connection: ack both subscriptions, then deliver an event.
		for i := 1; i <= 2; i++ {
			if _, err := wsutil.ReadClientText(rw); err != nil {
				return
			}
			writeServerText(t, conn, fmt.Sprintf(`{"id":%d,"type":"result","success":true}`, i))
		}
		writeServerText(t, conn, `{"id":1,"type":"event","event":{"event_type":"mobile_app_notification_action","data":{"action":"arm_alarm","sender":"pixel7"}}}`)
		for {
			if _, err := wsutil.ReadClientText(rw); err != nil {
				return
			}
		}
	})

	a, _ := newTestAdapter(t, mux)
	a.logger = testLogger()
	callbacks := make(chan plugin.IncomingMessage, 4)
	a.OnMessage(func(m plugin.IncomingMessage) { callbacks <- m })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer a.Stop()

	select {
	case m := <-callbacks:
		if m.Text != "arm_alarm" {
			t.Errorf("text = %q, want arm_alarm", m.Text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for post-reconnect event")
	}

	if n := conns.Load(); n != 2 {
		t.Errorf("expected exactly 2 websocket connections (failure + reconnect), got %d", n)
	}
}

// TestSubscribeAckWaitsForMatchingID ensures an event frame interleaved
// before the ack doesn't break the wait.
func TestSubscribeAckWaitsForMatchingID(t *testing.T) {
	a := &Adapter{logger: testLogger()}
	callbacks := make(chan plugin.IncomingMessage, 4)
	a.OnMessage(func(m plugin.IncomingMessage) { callbacks <- m })

	pr, pw := io.Pipe()
	rw := readWriterAdapter{Reader: pr, Writer: io.Discard}
	go func() {
		defer pw.Close()
		frames := []string{
			`{"id":9,"type":"event","event":{"event_type":"mobile_app_notification_action","data":{"action":"early"}}}`,
			`{"id":1,"type":"result","success":true}`,
		}
		for _, f := range frames {
			if err := wsutil.WriteServerText(pw, []byte(f)); err != nil {
				return
			}
		}
	}()

	if err := a.awaitSubscribeAck(rw, 1, "mobile_app_notification_action"); err != nil {
		t.Fatalf("awaitSubscribeAck: %v", err)
	}
	select {
	case m := <-callbacks:
		if m.Text != "early" {
			t.Errorf("text = %q, want early", m.Text)
		}
	default:
		t.Error("interleaved event was not dispatched")
	}
}

// readWriterAdapter pairs a reader with a sink writer so wsutil's
// ReadWriter-typed helpers can read from an io.Pipe.
type readWriterAdapter struct {
	io.Reader
	io.Writer
}
