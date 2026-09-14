package mattermost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func testAdapter(serverURL string) *Adapter {
	a := New()
	a.serverURL = serverURL
	a.accessToken = "test-token"
	a.selfUserID = "u1"
	return a
}

func TestValidate(t *testing.T) {
	a := New()
	if err := a.Validate(); err == nil {
		t.Fatal("expected validation error with empty config")
	}
	a.serverURL = "http://example.com"
	a.accessToken = "tok"
	if err := a.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestSendMessageSuccess(t *testing.T) {
	var mu sync.Mutex
	var gotChannel, gotMessage, gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/posts" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			ChannelID string `json:"channel_id"`
			Message   string `json:"message"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		gotChannel = body.ChannelID
		gotMessage = body.Message
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	a := testAdapter(srv.URL)
	if err := a.SendMessage(context.Background(), "chan1", plugin.OutgoingMessage{Text: "hello"}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer test-token" {
		t.Errorf("auth header = %q, want Bearer test-token", gotAuth)
	}
	if gotChannel != "chan1" || gotMessage != "hello" {
		t.Errorf("got channel=%q message=%q", gotChannel, gotMessage)
	}
}

func TestSendMessageEmptyText(t *testing.T) {
	a := testAdapter("http://127.0.0.1:1")
	if err := a.SendMessage(context.Background(), "chan1", plugin.OutgoingMessage{Text: ""}); err != nil {
		t.Fatalf("empty text should be a no-op, got: %v", err)
	}
}

func TestSendMessageHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	defer srv.Close()

	a := testAdapter(srv.URL)
	if err := a.SendMessage(context.Background(), "chan1", plugin.OutgoingMessage{Text: "hello"}); err == nil {
		t.Fatal("expected error on HTTP 403")
	}
}

func TestSendMessageChunking(t *testing.T) {
	var count int
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	a := testAdapter(srv.URL)
	long := strings.Repeat("a", maxMessageLen*2+100)
	if err := a.SendMessage(context.Background(), "chan1", plugin.OutgoingMessage{Text: long}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if count != 3 {
		t.Errorf("expected 3 chunk posts, got %d", count)
	}
}

func TestSplitLongMessage(t *testing.T) {
	if got := splitLongMessage("short", maxMessageLen); len(got) != 1 || got[0] != "short" {
		t.Errorf("short message should stay intact, got %v", got)
	}

	long := strings.Repeat("a", maxMessageLen*2+100)
	chunks := splitLongMessage(long, maxMessageLen)
	total := 0
	for _, c := range chunks {
		if len(c) > maxMessageLen {
			t.Errorf("chunk length %d exceeds max %d", len(c), maxMessageLen)
		}
		total += len(c)
	}
	if total != len(long) {
		t.Errorf("chunked total %d != original %d", total, len(long))
	}
}

func TestHandleWSFrame(t *testing.T) {
	a := testAdapter("http://example.com")

	var mu sync.Mutex
	var received []plugin.IncomingMessage
	a.OnMessage(func(in plugin.IncomingMessage) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, in)
	})

	// Message from another user should be forwarded.
	other := map[string]any{
		"event": "posted",
		"data": map[string]any{
			"post": `{"id":"p1","channel_id":"chan1","message":"hi","user_id":"u2"}`,
		},
	}
	data, _ := json.Marshal(other)
	a.handleWSFrame(data)

	// Message from self should be skipped.
	self := map[string]any{
		"event": "posted",
		"data": map[string]any{
			"post": `{"id":"p2","channel_id":"chan1","message":"self echo","user_id":"u1"}`,
		},
	}
	data, _ = json.Marshal(self)
	a.handleWSFrame(data)

	// Non-posted events should be ignored.
	otherEvent, _ := json.Marshal(map[string]any{"event": "status_change", "data": map[string]any{}})
	a.handleWSFrame(otherEvent)

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 callback, got %d", len(received))
	}
	in := received[0]
	if in.Platform != PlatformName || in.ChannelID != "chan1" || in.UserID != "u2" || in.Text != "hi" {
		t.Errorf("unexpected incoming message: %+v", in)
	}
}

func TestHandleWSFrameNilCallback(t *testing.T) {
	a := testAdapter("http://example.com")
	data, _ := json.Marshal(map[string]any{
		"event": "posted",
		"data":  map[string]any{"post": `{"channel_id":"c","message":"m","user_id":"u2"}`},
	})
	a.handleWSFrame(data) // must not panic
}

func TestWebsocketURL(t *testing.T) {
	cases := map[string]string{
		"https://mm.example.com": "wss://mm.example.com/api/v4/websocket",
		"http://127.0.0.1:8065":  "ws://127.0.0.1:8065/api/v4/websocket",
	}
	for in, want := range cases {
		got, err := websocketURL(in)
		if err != nil {
			t.Fatalf("websocketURL(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("websocketURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStartLoginAndStop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/users/login":
			var body map[string]string
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &body)
			if body["token"] != "test-token" {
				http.Error(w, "bad token", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Token", "session-token")
			w.Write([]byte(`{"id":"u1","username":"bot"}`))
		case "/api/v4/posts":
			w.WriteHeader(http.StatusCreated)
		default:
			// The websocket handshake will land here; plain 200 makes
			// gobwas fail the upgrade and exercise the reconnect path.
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	a := testAdapter(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if a.selfUserID != "u1" {
		t.Errorf("selfUserID = %q, want u1", a.selfUserID)
	}
	if err := a.Start(ctx); err == nil {
		t.Error("second Start should fail with already running")
	}

	// Outbound send should work right after login.
	if err := a.Send(context.Background(), "chan1", "hello"); err != nil {
		t.Errorf("Send after login failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if err := a.Stop(); err != nil {
		t.Errorf("Stop failed: %v", err)
	}
}

// fakeWSServer is a minimal Mattermost websocket server: it accepts the
// connection, captures the auth frame, replies with the configured frames
// and then holds the connection open until the client goes away.
type fakeWSServer struct {
	srv *httptest.Server

	mu         sync.Mutex
	conns      int
	authFrames []string
	replies    [][]byte
}

func newFakeWSServer(t *testing.T, replies ...string) *fakeWSServer {
	t.Helper()
	s := &fakeWSServer{}
	for _, r := range replies {
		s.replies = append(s.replies, []byte(r))
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, _, err := ws.UpgradeHTTP(r, w)
		if err != nil {
			return
		}
		defer conn.Close()
		s.mu.Lock()
		s.conns++
		replies := append([][]byte(nil), s.replies...)
		s.mu.Unlock()

		// Read from the hijacked bufio.ReadWriter so bytes already buffered
		// during the handshake are not lost.
		data, _, err := wsutil.ReadClientData(rw)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.authFrames = append(s.authFrames, string(data))
		s.mu.Unlock()

		for _, reply := range replies {
			if err := wsutil.WriteServerText(rw, reply); err != nil {
				return
			}
			if err := rw.Flush(); err != nil {
				return
			}
		}

		// Hold the connection open until the client closes it.
		buf := make([]byte, 64)
		for {
			if _, err := rw.Read(buf); err != nil {
				return
			}
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *fakeWSServer) addr() string { return s.srv.URL }

func (s *fakeWSServer) connectionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

func (s *fakeWSServer) authFramesSent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.authFrames...)
}

func TestWSSessionAuthOK(t *testing.T) {
	srv := newFakeWSServer(t, `{"status":"OK","seq_reply":1}`)
	a := testAdapter(srv.addr())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- a.wsSession(ctx) }()

	// The session must stay up (no error) now that auth succeeded.
	select {
	case err := <-errCh:
		t.Fatalf("wsSession returned early: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	frames := srv.authFramesSent()
	if len(frames) != 1 {
		t.Fatalf("auth frames = %v, want exactly one", frames)
	}
	var auth map[string]any
	if err := json.Unmarshal([]byte(frames[0]), &auth); err != nil {
		t.Fatalf("parse auth frame: %v", err)
	}
	if auth["action"] != "authentication_challenge" {
		t.Errorf("auth action = %v", auth["action"])
	}
	if auth["seq"] != float64(1) {
		t.Errorf("auth seq = %v, want 1", auth["seq"])
	}
	data := auth["data"].(map[string]any)
	if data["token"] != "test-token" {
		t.Errorf("auth token = %v", data["token"])
	}

	// Tear down the session.
	a.closeConn()
	select {
	case err := <-errCh:
		if err != nil && ctx.Err() == nil && !strings.Contains(err.Error(), "websocket read") {
			t.Logf("session ended with: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wsSession did not exit after closeConn")
	}
}

func TestWSSessionAuthErrorTriggersReconnect(t *testing.T) {
	srv := newFakeWSServer(t, `{"status":"ERROR","seq_reply":1}`)
	a := testAdapter(srv.addr())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A rejected auth must fail the session; wsLoop reconnects (after the
	// initial backoff) which shows up as a second connection on the server.
	go a.wsLoop(ctx)

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if srv.connectionCount() >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := srv.connectionCount(); got < 2 {
		t.Fatalf("connections = %d, want >=2 (reconnect after auth ERROR)", got)
	}
	if frames := srv.authFramesSent(); len(frames) < 2 {
		t.Fatalf("auth frames = %d, want >=2 (auth re-sent on reconnect)", len(frames))
	}
	cancel()
}

func TestWSSessionAuthTimeout(t *testing.T) {
	// Server accepts and replies with nothing: the auth deadline must close
	// the session instead of leaving it unauthenticated forever.
	srv := newFakeWSServer(t)
	a := testAdapter(srv.addr())

	old := authTimeout
	authTimeout = 200 * time.Millisecond
	defer func() { authTimeout = old }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := a.wsSession(ctx)
	if err == nil {
		t.Fatal("expected auth timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "authentication timed out") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWSSessionAuthNonOKMismatchedSeq(t *testing.T) {
	// An OK reply whose seq_reply does not match the auth seq must not
	// authenticate the session; it eventually times out.
	srv := newFakeWSServer(t, `{"status":"OK","seq_reply":99}`)
	a := testAdapter(srv.addr())

	old := authTimeout
	authTimeout = 200 * time.Millisecond
	defer func() { authTimeout = old }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := a.wsSession(ctx)
	if err == nil || !strings.Contains(err.Error(), "authentication timed out") {
		t.Fatalf("expected auth timeout for mismatched seq_reply, got %v", err)
	}
}
