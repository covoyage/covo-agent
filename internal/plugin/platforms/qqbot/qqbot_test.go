package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestAdapter wires an adapter to httptest-backed auth/REST endpoints.
func newTestAdapter(t *testing.T, handler http.Handler) (*Adapter, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a := &Adapter{
		appID:      "app-id",
		appSecret:  "app-secret",
		httpClient: &http.Client{Timeout: 5 * time.Second},
		logger:     testLogger(),
		authURL:    srv.URL + "/auth",
		apiBase:    srv.URL,
		gatewayURL: "wss://unused",
	}
	return a, srv
}

func TestValidate(t *testing.T) {
	a := &Adapter{}
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "QQBOT_APP_ID") {
		t.Fatalf("expected QQBOT_APP_ID error, got %v", err)
	}
	a.appID = "id"
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "QQBOT_APP_SECRET") {
		t.Fatalf("expected QQBOT_APP_SECRET error, got %v", err)
	}
	a.appSecret = "secret"
	if err := a.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestAccessTokenCached(t *testing.T) {
	var calls int
	var mu sync.Mutex
	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			AppID        string `json:"appId"`
			ClientSecret string `json:"clientSecret"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil || req.AppID != "app-id" || req.ClientSecret != "app-secret" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		calls++
		mu.Unlock()
		w.Write([]byte(`{"access_token":"tok-1","expires_in":"7200"}`))
	}))

	tok1, err := a.getAccessToken(context.Background())
	if err != nil {
		t.Fatalf("getAccessToken: %v", err)
	}
	tok2, err := a.getAccessToken(context.Background())
	if err != nil {
		t.Fatalf("getAccessToken cached: %v", err)
	}
	if tok1 != "tok-1" || tok2 != "tok-1" {
		t.Fatalf("unexpected tokens: %q %q", tok1, tok2)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected 1 auth call, got %d", calls)
	}
}

func TestAccessTokenForceRefreshOnExpiry(t *testing.T) {
	var calls int
	var mu sync.Mutex
	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		fmt.Fprintf(w, `{"access_token":"tok-%d","expires_in":"7200"}`, n)
	}))

	if _, err := a.getAccessToken(context.Background()); err != nil {
		t.Fatalf("getAccessToken: %v", err)
	}
	// Simulate expiry.
	a.tokenMu.Lock()
	a.tokenExpiry = time.Now().Add(-time.Minute)
	a.tokenMu.Unlock()

	tok, err := a.getAccessToken(context.Background())
	if err != nil {
		t.Fatalf("getAccessToken after expiry: %v", err)
	}
	if tok != "tok-2" {
		t.Fatalf("expected refreshed token tok-2, got %q", tok)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("expected 2 auth calls, got %d", calls)
	}
}

func TestSendMessageSingleChunk(t *testing.T) {
	var gotAuth, gotPath string
	var payload map[string]any
	var mu sync.Mutex

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			w.Write([]byte(`{"access_token":"tok","expires_in":"7200"}`))
			return
		case "/v2/users/user-openid/messages":
			mu.Lock()
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			json.NewDecoder(r.Body).Decode(&payload)
			mu.Unlock()
			w.Write([]byte(`{"code":0,"message":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))

	if err := a.Send(context.Background(), "user-openid", "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/v2/users/user-openid/messages" {
		t.Fatalf("unexpected path %q", gotPath)
	}
	if gotAuth != "QQBot tok" {
		t.Fatalf("unexpected auth header %q", gotAuth)
	}
	if payload["content"] != "hello" || payload["msg_type"] != float64(0) {
		t.Fatalf("unexpected payload: %v", payload)
	}
}

func TestSendMessageGroupEndpoint(t *testing.T) {
	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			w.Write([]byte(`{"access_token":"tok","expires_in":"7200"}`))
		case "/v2/groups/group-openid/messages":
			w.Write([]byte(`{"code":0,"message":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))

	a.endpointKnd.Store("group-openid", "group")
	if err := a.Send(context.Background(), "group-openid", "hi"); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestSendMessageAPIErrorPropagated(t *testing.T) {
	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth" {
			w.Write([]byte(`{"access_token":"tok","expires_in":"7200"}`))
			return
		}
		w.Write([]byte(`{"code":11253,"message":"msg_id invalid"}`))
	}))

	err := a.Send(context.Background(), "openid", "hi")
	if err == nil || !strings.Contains(err.Error(), "11253") {
		t.Fatalf("expected errcode 11253 error, got %v", err)
	}
}

func TestSendMessageMultiChunk(t *testing.T) {
	var posts int
	var mu sync.Mutex
	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth" {
			w.Write([]byte(`{"access_token":"tok","expires_in":"7200"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/messages") {
			mu.Lock()
			posts++
			mu.Unlock()
			w.Write([]byte(`{"code":0,"message":"ok"}`))
		}
	}))

	// 5000 CJK chars exceed the 4000-rune chunk limit.
	long := strings.Repeat("字", 5000)
	if err := a.Send(context.Background(), "openid", long); err != nil {
		t.Fatalf("Send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if posts != 2 {
		t.Fatalf("expected 2 chunk posts, got %d", posts)
	}
}

func TestIntentsValue(t *testing.T) {
	// C2C_GROUP_AT_MESSAGES | PUBLIC_GUILD_MESSAGES | DIRECT_MESSAGE.
	const want = 1<<25 | 1<<30 | 1<<12
	if intents != want {
		t.Fatalf("unexpected intents: got %d, want %d", intents, want)
	}
}

func TestPassiveReplyUsesCachedMsgID(t *testing.T) {
	var gotPath string
	var payload map[string]any
	var mu sync.Mutex

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			w.Write([]byte(`{"access_token":"tok","expires_in":"7200"}`))
			return
		case "/v2/groups/g-1/messages":
			mu.Lock()
			gotPath = r.URL.Path
			json.NewDecoder(r.Body).Decode(&payload)
			mu.Unlock()
			w.Write([]byte(`{"code":0,"message":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))

	// Inbound group dispatch carrying the message id the reply must echo.
	a.dispatch(context.Background(), eventGroupAtMsg, json.RawMessage(`{
		"id": "MSG-42",
		"content": "ping",
		"group_openid": "g-1",
		"author": {"user_openid": "u-1"}
	}`))

	if err := a.Send(context.Background(), "g-1", "pong"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/v2/groups/g-1/messages" {
		t.Fatalf("unexpected path %q", gotPath)
	}
	if payload["msg_id"] != "MSG-42" {
		t.Fatalf("expected passive reply msg_id MSG-42, got %v", payload["msg_id"])
	}
	if seq, ok := payload["msg_seq"].(float64); !ok || seq < 1 {
		t.Fatalf("expected msg_seq >= 1, got %v", payload["msg_seq"])
	}
}

func TestExplicitReplyToOverridesCache(t *testing.T) {
	var payload map[string]any
	var mu sync.Mutex

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			w.Write([]byte(`{"access_token":"tok","expires_in":"7200"}`))
			return
		case "/v2/users/u-1/messages":
			mu.Lock()
			json.NewDecoder(r.Body).Decode(&payload)
			mu.Unlock()
			w.Write([]byte(`{"code":0,"message":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))

	a.rememberMsgID("u-1", "CACHED")
	if err := a.SendMessage(context.Background(), "u-1", plugin.OutgoingMessage{Text: "hi", ReplyTo: "EXPLICIT"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if payload["msg_id"] != "EXPLICIT" {
		t.Fatalf("expected explicit reply_to msg_id, got %v", payload["msg_id"])
	}
}

func TestGuildReplyEndpoint(t *testing.T) {
	var gotPath string
	var payload map[string]any
	var mu sync.Mutex

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/auth":
			w.Write([]byte(`{"access_token":"tok","expires_in":"7200"}`))
			return
		case r.URL.Path == "/channels/ch-1/messages":
			mu.Lock()
			gotPath = r.URL.Path
			json.NewDecoder(r.Body).Decode(&payload)
			mu.Unlock()
			w.Write([]byte(`{"id":"out-1"}`))
		default:
			mu.Lock()
			gotPath = r.URL.Path
			mu.Unlock()
			http.NotFound(w, r)
		}
	}))

	// Guild event: channel_id present, author has native id (no user_openid).
	a.dispatch(context.Background(), eventGuildAtMsg, json.RawMessage(`{
		"id": "GMSG-7",
		"content": "hello guild",
		"channel_id": "ch-1",
		"guild_id": "guild-1",
		"author": {"id": "native-user"}
	}`))

	if err := a.Send(context.Background(), "ch-1", "reply"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/channels/ch-1/messages" {
		t.Fatalf("expected guild channel endpoint, got %q", gotPath)
	}
	if payload["msg_id"] != "GMSG-7" {
		t.Fatalf("expected guild reply msg_id GMSG-7, got %v", payload["msg_id"])
	}
}

func TestMsgIDCacheTTL(t *testing.T) {
	a := &Adapter{}
	a.rememberMsgID("ch", "m-1")
	if got := a.lastMsgIDFor("ch"); got != "m-1" {
		t.Fatalf("expected cached msg id, got %q", got)
	}
	// Expired entries are dropped.
	a.lastMsgID.Store("ch", msgIDEntry{id: "m-0", expires: time.Now().Add(-time.Second)})
	if got := a.lastMsgIDFor("ch"); got != "" {
		t.Fatalf("expected expired entry to be evicted, got %q", got)
	}
	if got := a.lastMsgIDFor("missing"); got != "" {
		t.Fatalf("expected empty id for unknown channel, got %q", got)
	}
}

func TestSplitLongMessageRuneBoundary(t *testing.T) {
	// A single line of CJK runes whose byte length is not a multiple of 3:
	// byte-based splitting would cut mid-character and produce invalid UTF-8.
	long := strings.Repeat("汉", 4500)
	chunks := splitLongMessage(long)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if !utf8.ValidString(c) {
			t.Fatalf("chunk is not valid UTF-8: %q...", c[:30])
		}
		if utf8.RuneCountInString(c) > maxChunkRunes {
			t.Fatalf("chunk exceeds rune limit: %d", utf8.RuneCountInString(c))
		}
	}
	// 3999 ASCII + one CJK char stays intact within one chunk.
	mixed := strings.Repeat("a", maxChunkRunes-1) + "字"
	chunks = splitLongMessage(mixed)
	if len(chunks) != 1 || chunks[0] != mixed {
		t.Fatalf("expected single intact chunk, got %d chunks", len(chunks))
	}
}

// --- websocket gateway tests ---

// fakeGateway is a raw TCP server that performs a gobwas server-side
// websocket upgrade and relays client frames to a channel.
type fakeGateway struct {
	listener net.Listener
	conns    chan net.Conn
	frames   chan map[string]any
}

func startFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fg := &fakeGateway{
		listener: l,
		conns:    make(chan net.Conn, 1),
		frames:   make(chan map[string]any, 32),
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			fg.conns <- c
		}
	}()
	t.Cleanup(func() { l.Close() })
	return fg
}

func (fg *fakeGateway) URL() string {
	return "ws://" + fg.listener.Addr().String() + "/websocket/"
}

// upgrade blocks until the adapter connects, performs the handshake and
// starts relaying client frames.
func (fg *fakeGateway) upgrade(t *testing.T) net.Conn {
	t.Helper()
	var c net.Conn
	select {
	case c = <-fg.conns:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for gateway connection")
		return nil
	}
	if _, err := ws.Upgrade(c); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	go func() {
		for {
			data, err := wsutil.ReadClientText(c) // reads (and unmasks) client frames
			if err != nil {
				return // connection closed or protocol error
			}
			var m map[string]any
			if json.Unmarshal(data, &m) == nil {
				fg.frames <- m
			}
		}
	}()
	return c
}

func (fg *fakeGateway) send(t *testing.T, c net.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal server frame: %v", err)
	}
	if err := wsutil.WriteServerText(c, data); err != nil {
		t.Fatalf("server write: %v", err)
	}
}

// nextFrame waits for the next client frame matching pred.
func nextFrame(t *testing.T, fg *fakeGateway, timeout time.Duration, pred func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case f := <-fg.frames:
			if pred(f) {
				return f
			}
		case <-deadline:
			t.Fatalf("timed out waiting for client frame")
			return nil
		}
	}
}

func TestGatewayHandshakeHeartbeatDispatch(t *testing.T) {
	fg := startFakeGateway(t)

	var mu sync.Mutex
	var received []plugin.IncomingMessage

	a := &Adapter{
		appID:      "app-id",
		appSecret:  "app-secret",
		httpClient: &http.Client{Timeout: 5 * time.Second},
		logger:     testLogger(),
	}
	a.accessToken = "tok"
	a.tokenExpiry = time.Now().Add(time.Hour)
	a.gatewayURL = fg.URL()
	a.onMessage = func(msg plugin.IncomingMessage) {
		mu.Lock()
		received = append(received, msg)
		mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = a.runGatewaySession(ctx, fg.URL())
		close(done)
	}()

	c := fg.upgrade(t)

	// Server hello with a short heartbeat interval.
	fg.send(t, c, map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 50}})

	// Expect client Identify (op 2).
	identify := nextFrame(t, fg, 5*time.Second, func(f map[string]any) bool { return f["op"] == float64(2) })
	idData, _ := identify["d"].(map[string]any)
	if idData == nil {
		t.Fatalf("identify payload missing d: %v", identify)
	}
	if idData["token"] != "QQBot tok" {
		t.Fatalf("unexpected identify token %v", idData["token"])
	}
	if idData["intents"] != float64(intents) {
		t.Fatalf("unexpected intents %v", idData["intents"])
	}
	shard, _ := idData["shard"].([]any)
	if len(shard) != 2 || shard[0] != float64(0) || shard[1] != float64(1) {
		t.Fatalf("unexpected shard %v", idData["shard"])
	}

	// Dispatch carries sequence s=7; the next heartbeat (op 1) must echo it.
	fg.send(t, c, map[string]any{"op": 0, "s": 7, "t": "RESUMED", "d": map[string]any{}})
	hb := nextFrame(t, fg, 5*time.Second, func(f map[string]any) bool { return f["op"] == float64(1) })
	if hb["d"] != float64(7) {
		t.Fatalf("expected heartbeat seq 7, got %v", hb["d"])
	}

	// Dispatch a C2C message; the adapter must invoke the callback.
	fg.send(t, c, map[string]any{
		"op": 0,
		"s":  8,
		"t":  "C2C_MESSAGE_CREATE",
		"d": map[string]any{
			"content": "ping",
			"author":  map[string]any{"user_openid": "u-1"},
		},
	})

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		var msg plugin.IncomingMessage
		if n > 0 {
			msg = received[0]
		}
		mu.Unlock()
		if n >= 1 {
			if msg.Platform != PlatformName || msg.ChannelID != "u-1" || msg.UserID != "u-1" || msg.Text != "ping" {
				t.Fatalf("unexpected incoming message: %+v", msg)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for dispatched message")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("session did not exit after cancel")
	}
}

func TestGatewayGroupDispatch(t *testing.T) {
	fg := startFakeGateway(t)

	var mu sync.Mutex
	var received []plugin.IncomingMessage

	a := &Adapter{httpClient: &http.Client{Timeout: 5 * time.Second}, logger: testLogger()}
	a.accessToken = "tok"
	a.tokenExpiry = time.Now().Add(time.Hour)
	a.gatewayURL = fg.URL()
	a.onMessage = func(msg plugin.IncomingMessage) {
		mu.Lock()
		received = append(received, msg)
		mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = a.runGatewaySession(ctx, fg.URL()) }()

	c := fg.upgrade(t)
	fg.send(t, c, map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 60000}})
	nextFrame(t, fg, 5*time.Second, func(f map[string]any) bool { return f["op"] == float64(2) })

	fg.send(t, c, map[string]any{
		"op": 0,
		"s":  1,
		"t":  "GROUP_AT_MESSAGE_CREATE",
		"d": map[string]any{
			"content":      "hello group",
			"group_openid": "g-1",
			"author":       map[string]any{"user_openid": "u-9"},
		},
	})

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		var msg plugin.IncomingMessage
		if n > 0 {
			msg = received[0]
		}
		mu.Unlock()
		if n >= 1 {
			if msg.ChannelID != "g-1" || msg.UserID != "u-9" || msg.Text != "hello group" {
				t.Fatalf("unexpected incoming message: %+v", msg)
			}
			// The adapter must remember g-1 is a group endpoint for replies.
			if _, ok := a.endpointKnd.Load("g-1"); !ok {
				t.Fatalf("group endpoint not recorded")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for group dispatch")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
}

func TestSplitLongMessage(t *testing.T) {
	if got := splitLongMessage("short"); len(got) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(got))
	}
	long := strings.Repeat("ab", 2500) // 5000 runes
	chunks := splitLongMessage(long)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if utf8.RuneCountInString(c) > maxChunkRunes {
			t.Fatalf("chunk exceeds rune limit: %d", utf8.RuneCountInString(c))
		}
	}
}

// TestGatewayResumeAfterReconnect verifies op 6 Resume is sent on reconnect
// when a READY session id and dispatch seq are known.
func TestGatewayResumeAfterReconnect(t *testing.T) {
	fg := startFakeGateway(t)

	a := &Adapter{httpClient: &http.Client{Timeout: 5 * time.Second}, logger: testLogger()}
	a.accessToken = "tok"
	a.tokenExpiry = time.Now().Add(time.Hour)
	a.gatewayURL = fg.URL()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = a.runGatewaySession(ctx, fg.URL())
		close(done)
	}()

	c := fg.upgrade(t)
	fg.send(t, c, map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 60000}})
	nextFrame(t, fg, 5*time.Second, func(f map[string]any) bool { return f["op"] == float64(2) })

	// READY supplies the session id; a dispatch advances the seq.
	fg.send(t, c, map[string]any{"op": 0, "s": 3, "t": "READY", "d": map[string]any{"session_id": "sess-1"}})
	fg.send(t, c, map[string]any{"op": 0, "s": 5, "t": "RESUMED", "d": map[string]any{}})
	time.Sleep(100 * time.Millisecond)

	// Drop the connection: the adapter's session loop exits and reconnects.
	c.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("first session did not exit")
	}

	go func() { _ = a.runGatewaySession(ctx, fg.URL()) }()
	c2 := fg.upgrade(t)
	fg.send(t, c2, map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 60000}})

	resume := nextFrame(t, fg, 5*time.Second, func(f map[string]any) bool { return f["op"] == float64(6) })
	rd, _ := resume["d"].(map[string]any)
	if rd == nil {
		t.Fatalf("resume payload missing d: %v", resume)
	}
	if rd["session_id"] != "sess-1" {
		t.Fatalf("unexpected resume session_id %v", rd["session_id"])
	}
	if rd["seq"] != float64(5) {
		t.Fatalf("unexpected resume seq %v", rd["seq"])
	}
	if rd["token"] != "QQBot tok" {
		t.Fatalf("unexpected resume token %v", rd["token"])
	}
	cancel()
}

// TestGatewayInvalidSessionClearsState verifies op 9 (d=false) clears the
// session so the next Hello re-Identifies.
func TestGatewayInvalidSessionClearsState(t *testing.T) {
	fg := startFakeGateway(t)

	a := &Adapter{httpClient: &http.Client{Timeout: 5 * time.Second}, logger: testLogger()}
	a.accessToken = "tok"
	a.tokenExpiry = time.Now().Add(time.Hour)
	a.gatewayURL = fg.URL()
	a.setWebsocketSession("sess-9")
	a.trackSeq(11)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = a.runGatewaySession(ctx, fg.URL())
		close(done)
	}()

	c := fg.upgrade(t)
	fg.send(t, c, map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 60000}})

	// With session + seq known, the Hello is answered by Resume.
	nextFrame(t, fg, 5*time.Second, func(f map[string]any) bool { return f["op"] == float64(6) })

	// Server declares the session invalid and not resumable.
	fg.send(t, c, map[string]any{"op": 9, "d": false})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("session did not exit after op 9")
	}

	if _, _, canResume := a.websocketSession(); canResume {
		t.Fatalf("expected session state to be cleared after op 9")
	}
}
