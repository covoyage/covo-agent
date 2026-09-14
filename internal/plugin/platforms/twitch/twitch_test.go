package twitch

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
)

type nullWriter struct{}

func (nullWriter) Write(p []byte) (int, error) { return len(p), nil }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nullWriter{}, &slog.HandlerOptions{Level: slog.LevelError + 100}))
}

// fakeTwitchServer is a minimal Twitch IRC gateway used by the tests.
type fakeTwitchServer struct {
	listener net.Listener

	mu    sync.Mutex
	lines []string

	msgCh chan plugin.IncomingMessage

	closeOnce sync.Once
}

func newFakeTwitchServer(t *testing.T) *fakeTwitchServer {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeTwitchServer{
		listener: l,
		msgCh:    make(chan plugin.IncomingMessage, 8),
	}
	go s.serve()
	t.Cleanup(func() { s.closeOnce.Do(func() { _ = l.Close() }) })
	return s
}

func (s *fakeTwitchServer) addr() string { return s.listener.Addr().String() }

func (s *fakeTwitchServer) serve() {
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		s.mu.Lock()
		s.lines = append(s.lines, line)
		s.mu.Unlock()

		switch {
		case strings.HasPrefix(line, "NICK "):
			fmt.Fprint(conn, ":tmi.twitch.tv 001 testbot :Welcome, GLHF!\r\n")
			// An echo of the bot's own PRIVMSG must be filtered out.
			fmt.Fprint(conn, "@badge-info=;badges=;color=#1E90FF;display-name=TestBot;user-id=999;login=testbot :testbot!testbot@test.tmi.twitch.tv PRIVMSG #somechannel :self echo should be dropped\r\n")
			fmt.Fprint(conn, "@badge-info=;badges=;color=#1E90FF;display-name=Alice!;user-id=12345 :alice!alice@alice.tmi.twitch.tv PRIVMSG #somechannel :hi there from twitch\r\n")
			fmt.Fprint(conn, "PING :tmi.twitch.tv\r\n")
		}
	}
}

func (s *fakeTwitchServer) hasLine(pred func(string) bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.lines {
		if pred(l) {
			return true
		}
	}
	return false
}

func newTestAdapter(server string) *Adapter {
	return &Adapter{
		username:   "testbot",
		oauthToken: "rawtoken123",
		channels:   []string{"somechannel"},
		serverAddr: server,
		skipTLS:    true,
		logger:     discardLogger(),
	}
}

func TestStartHandshakeAndReceive(t *testing.T) {
	srv := newFakeTwitchServer(t)
	a := newTestAdapter(srv.addr())

	var mu sync.Mutex
	var got []plugin.IncomingMessage
	a.OnMessage(func(m plugin.IncomingMessage) {
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for func() int { mu.Lock(); defer mu.Unlock(); return len(got) }() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	msgs := append([]plugin.IncomingMessage(nil), got...)
	mu.Unlock()
	if len(msgs) < 1 {
		t.Fatal("no incoming message received")
	}

	m := msgs[0]
	if m.Platform != PlatformName {
		t.Errorf("Platform = %q", m.Platform)
	}
	if m.ChannelID != "#somechannel" {
		t.Errorf("ChannelID = %q, want #somechannel", m.ChannelID)
	}
	if m.UserName != "Alice!" {
		t.Errorf("UserName = %q, want Alice! (from display-name tag)", m.UserName)
	}
	if m.UserID != "12345" {
		t.Errorf("UserID = %q, want 12345 (from user-id tag)", m.UserID)
	}
	if m.Text != "hi there from twitch" {
		t.Errorf("Text = %q", m.Text)
	}

	// Handshake: CAP REQ, PASS with oauth: prefix, NICK, then JOIN after 001.
	if !srv.hasLine(func(l string) bool { return l == "CAP REQ twitch.tv/tags twitch.tv/commands" }) {
		t.Error("CAP REQ not sent")
	}
	if !srv.hasLine(func(l string) bool { return l == "PASS oauth:rawtoken123" }) {
		t.Error("PASS with oauth: prefix not sent")
	}
	if !srv.hasLine(func(l string) bool { return l == "NICK testbot" }) {
		t.Error("NICK not sent")
	}
	if !srv.hasLine(func(l string) bool { return l == "JOIN #somechannel" }) {
		t.Error("JOIN #somechannel not sent")
	}
	// PING :tmi.twitch.tv must be answered with PONG :tmi.twitch.tv.
	if !srv.hasLine(func(l string) bool { return l == "PONG :tmi.twitch.tv" }) {
		t.Error("PONG :tmi.twitch.tv not sent")
	}
}

func TestSendMessage(t *testing.T) {
	srv := newFakeTwitchServer(t)
	a := newTestAdapter(srv.addr())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	// Wait until connected.
	deadline := time.Now().Add(5 * time.Second)
	for a.currentConn() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.currentConn() == nil {
		t.Fatal("adapter never connected")
	}

	if err := a.Send(context.Background(), "#SomeChannel", "hello twitch"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if srv.hasLine(func(l string) bool { return l == "PRIVMSG #somechannel :hello twitch" }) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("PRIVMSG not received by server")
}

func TestSendMessageLongTextChunks(t *testing.T) {
	srv := newFakeTwitchServer(t)
	a := newTestAdapter(srv.addr())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for a.currentConn() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	long := strings.Repeat("x", 1000)
	if err := a.Send(context.Background(), "#somechannel", long); err != nil {
		t.Fatalf("Send: %v", err)
	}

	deadline = time.Now().Add(5 * time.Second)
	var count int
	for time.Now().Before(deadline) {
		count = 0
		srv.mu.Lock()
		for _, l := range srv.lines {
			if strings.HasPrefix(l, "PRIVMSG #somechannel :") {
				count++
			}
		}
		srv.mu.Unlock()
		if count >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if count < 3 {
		t.Fatalf("expected >=3 chunks for 1000 bytes, got %d", count)
	}
}

func TestSendMessageWithoutToken(t *testing.T) {
	a := &Adapter{logger: discardLogger(), serverAddr: "127.0.0.1:1", skipTLS: true}
	if err := a.Send(context.Background(), "#x", "hi"); err == nil {
		t.Fatal("expected error when token not configured")
	}
}

func TestValidate(t *testing.T) {
	a := &Adapter{}
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "TWITCH_NICK") {
		t.Errorf("Validate() = %v, want TWITCH_NICK error", err)
	}
	a.username = "bot"
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "TWITCH_OAUTH_TOKEN") {
		t.Errorf("Validate() = %v, want TWITCH_OAUTH_TOKEN error", err)
	}
	a.oauthToken = "tok"
	if err := a.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestSelfEchoFiltered(t *testing.T) {
	// End-to-end: the fake gateway echoes one of the bot's own PRIVMSGs before
	// Alice's message; only Alice's must reach the callback.
	srv := newFakeTwitchServer(t)
	a := newTestAdapter(srv.addr())

	var mu sync.Mutex
	var got []plugin.IncomingMessage
	a.OnMessage(func(m plugin.IncomingMessage) {
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for func() int { mu.Lock(); defer mu.Unlock(); return len(got) }() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// Give any (incorrectly) delivered self-echo time to arrive.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("callbacks = %d, want 1 (self echo must be filtered)", len(got))
	}
	if got[0].UserName != "Alice!" {
		t.Fatalf("delivered message = %+v, want Alice's", got[0])
	}
}

func TestHandlePrivmsgSelfEchoVariants(t *testing.T) {
	tests := []struct {
		name   string
		tags   map[string]string
		prefix string
	}{
		{"login tag", map[string]string{"login": "testbot"}, "other!other@host"},
		{"display-name tag", map[string]string{"display-name": "TestBot"}, "other!other@host"},
		{"prefix nick", map[string]string{}, "testbot!testbot@test.tmi.twitch.tv"},
		{"case-insensitive login", map[string]string{"login": "TestBot"}, "other!other@host"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAdapter("127.0.0.1:1")
			a.mu.Lock()
			a.selfNick = "testbot"
			a.mu.Unlock()

			called := false
			a.OnMessage(func(m plugin.IncomingMessage) { called = true })
			a.handlePrivmsg(tc.tags, tc.prefix, "#chan :echo")
			if called {
				t.Fatalf("self echo via %s must be filtered out", tc.name)
			}
		})
	}

	// A message from someone else is delivered normally.
	a := newTestAdapter("127.0.0.1:1")
	a.mu.Lock()
	a.selfNick = "testbot"
	a.mu.Unlock()

	var got plugin.IncomingMessage
	a.OnMessage(func(m plugin.IncomingMessage) { got = m })
	a.handlePrivmsg(
		map[string]string{"login": "alice", "display-name": "Alice", "user-id": "12345"},
		"alice!alice@alice.tmi.twitch.tv", "#somechannel :hi there from twitch")
	if got.Text != "hi there from twitch" || got.UserID != "12345" || got.UserName != "Alice" {
		t.Fatalf("other-user message not delivered: %+v", got)
	}
}

func TestParseTwitchLine(t *testing.T) {
	tags, prefix, cmd, params := parseTwitchLine(
		"@badge-info=;display-name=Alice\\sB;user-id=42 :alice!alice@host PRIVMSG #chan :hello world")
	if tags["display-name"] != "Alice B" {
		t.Errorf("display-name tag = %q", tags["display-name"])
	}
	if prefix != "alice!alice@host" {
		t.Errorf("prefix = %q", prefix)
	}
	if cmd != "PRIVMSG" || params != "#chan :hello world" {
		t.Errorf("cmd/params = %q/%q", cmd, params)
	}
}

func TestNewStripsOauthPrefix(t *testing.T) {
	t.Setenv("TWITCH_OAUTH_TOKEN", "oauth:abc123")
	t.Setenv("TWITCH_NICK", "mybot")
	t.Setenv("TWITCH_CHANNELS", "#A, b ,#c")
	a := New()
	if a.oauthToken != "abc123" {
		t.Errorf("oauthToken = %q, want abc123", a.oauthToken)
	}
	if a.username != "mybot" {
		t.Errorf("username = %q", a.username)
	}
	if len(a.channels) != 3 || a.channels[0] != "a" || a.channels[2] != "c" {
		t.Errorf("channels = %v", a.channels)
	}
}
