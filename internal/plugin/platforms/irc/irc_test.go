package irc

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
	"unicode/utf8"

	"github.com/covoyage/covo-agent/internal/plugin"
)

// discardLogger returns a silent logger for tests.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(
		&nullWriter{},
		&slog.HandlerOptions{Level: slog.LevelError + 100},
	))
}

type nullWriter struct{}

func (nullWriter) Write(p []byte) (int, error) { return len(p), nil }

// fakeIRCServer is a minimal IRC server used to exercise the adapter
// handshake, read loop and send path without touching the network beyond
// localhost.
type fakeIRCServer struct {
	listener net.Listener

	mu        sync.Mutex
	welcomed  bool
	lines     []string
	msgCh     chan plugin.IncomingMessage
	lineCh    chan string
	closeOnce sync.Once
}

func newFakeIRCServer(t *testing.T) *fakeIRCServer {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeIRCServer{
		listener: l,
		msgCh:    make(chan plugin.IncomingMessage, 8),
		lineCh:   make(chan string, 32),
	}
	go s.serve()
	t.Cleanup(func() { s.closeOnce.Do(func() { _ = l.Close() }) })
	return s
}

func (s *fakeIRCServer) addr() string { return s.listener.Addr().String() }

func (s *fakeIRCServer) serve() {
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
		select {
		case s.lineCh <- line:
		default:
		}

		switch {
		case strings.HasPrefix(line, "USER"):
			// Registration complete: send 001 welcome, then a PRIVMSG
			// from another user and a PING to exercise the read loop.
			fmt.Fprint(conn, ":srv.example 001 testbot :Welcome to the test IRC server\r\n")
			fmt.Fprint(conn, ":alice!alice@example.com PRIVMSG #test :hello from alice\r\n")
			fmt.Fprint(conn, ":bob!bob@example.com PRIVMSG testbot :private hello\r\n")
			fmt.Fprint(conn, "PING :srv-12345\r\n")
		case strings.HasPrefix(line, "PING"):
			fmt.Fprint(conn, "PONG :srv.example\r\n")
		}
	}
}

func (s *fakeIRCServer) hasLine(pred func(string) bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.lines {
		if pred(l) {
			return true
		}
	}
	return false
}

func (s *fakeIRCServer) privmsgLines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, l := range s.lines {
		if strings.HasPrefix(l, "PRIVMSG ") {
			out = append(out, l)
		}
	}
	return out
}

func newTestAdapter(server string, channels []string) *Adapter {
	a := &Adapter{
		server:   server,
		nick:     "testbot",
		channels: channels,
		useTLS:   false,
		logger:   discardLogger(),
	}
	return a
}

func TestStartHandshakeAndReceive(t *testing.T) {
	srv := newFakeIRCServer(t)
	a := newTestAdapter(srv.addr(), []string{"#test"})

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

	// Wait for both the channel message and the private message.
	deadline := time.Now().Add(5 * time.Second)
	for func() int { mu.Lock(); defer mu.Unlock(); return len(got) }() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	msgs := append([]plugin.IncomingMessage(nil), got...)
	mu.Unlock()
	if len(msgs) < 2 {
		t.Fatalf("expected 2 incoming messages, got %d", len(msgs))
	}

	chanMsg := msgs[0]
	if chanMsg.ChannelID != "#test" {
		t.Errorf("channel message ChannelID = %q, want #test", chanMsg.ChannelID)
	}
	if chanMsg.UserID != "alice" || chanMsg.UserName != "alice" {
		t.Errorf("channel message user = %q/%q, want alice/alice", chanMsg.UserID, chanMsg.UserName)
	}
	if chanMsg.Text != "hello from alice" {
		t.Errorf("channel message Text = %q", chanMsg.Text)
	}
	if chanMsg.Platform != PlatformName {
		t.Errorf("Platform = %q", chanMsg.Platform)
	}

	privMsg := msgs[1]
	if privMsg.ChannelID != "bob" {
		t.Errorf("private message ChannelID = %q, want bob", privMsg.ChannelID)
	}
	if privMsg.Text != "private hello" {
		t.Errorf("private message Text = %q", privMsg.Text)
	}

	// Handshake lines must have been sent, and PING must be answered by PONG.
	if !srv.hasLine(func(l string) bool { return strings.HasPrefix(l, "NICK testbot") }) {
		t.Error("NICK command not sent")
	}
	if !srv.hasLine(func(l string) bool { return strings.HasPrefix(l, "USER testbot") }) {
		t.Error("USER command not sent")
	}
	if !srv.hasLine(func(l string) bool { return l == "JOIN #test" }) {
		t.Error("JOIN #test not sent")
	}
	if !srv.hasLine(func(l string) bool { return l == "PONG :srv-12345" }) {
		t.Error("PONG for server PING not sent")
	}
}

func TestSendMessage(t *testing.T) {
	srv := newFakeIRCServer(t)
	a := newTestAdapter(srv.addr(), []string{"#test"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	// Wait until the connection is registered.
	deadline := time.Now().Add(5 * time.Second)
	for a.currentConn() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.currentConn() == nil {
		t.Fatal("adapter never connected")
	}

	if err := a.Send(context.Background(), "#test", "hello world"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if srv.hasLine(func(l string) bool { return l == "PRIVMSG #test :hello world" }) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("PRIVMSG not received by server; lines=%v", srv.privmsgLines())
}

func TestSendMessageChunksLongText(t *testing.T) {
	srv := newFakeIRCServer(t)
	a := newTestAdapter(srv.addr(), []string{"#test"})

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

	long := strings.Repeat("あ", 300) // 900 bytes in UTF-8, must be split without breaking runes
	if err := a.Send(context.Background(), "#test", long); err != nil {
		t.Fatalf("Send: %v", err)
	}

	budget := privmsgPayloadBudget("#test")
	deadline = time.Now().Add(5 * time.Second)
	var msgs []string
	for time.Now().Before(deadline) {
		msgs = srv.privmsgLines()
		if len(msgs) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(msgs) < 2 {
		t.Fatalf("expected >=2 PRIVMSG chunks, got %d: %v", len(msgs), msgs)
	}

	var rebuilt strings.Builder
	for _, m := range msgs {
		payload := strings.TrimPrefix(m, "PRIVMSG #test :")
		if len(payload) > budget {
			t.Errorf("chunk too large: %d bytes (budget %d)", len(payload), budget)
		}
		rebuilt.WriteString(payload)
	}
	if rebuilt.String() != long {
		t.Error("chunked payload does not reassemble to the original text")
	}
}

func TestSendMessageNotConnected(t *testing.T) {
	a := newTestAdapter("127.0.0.1:1", nil) // nothing listening
	if err := a.Send(context.Background(), "#test", "hi"); err == nil {
		t.Fatal("expected error when not connected, got nil")
	}
}

func TestPrivmsgPayloadBudget(t *testing.T) {
	// 512 - len("PRIVMSG ")-len(target)-len(" :")-len("\r\n")
	cases := map[string]int{
		"#test":                   512 - 8 - 5 - 2 - 2, // 495
		"botnick":                 512 - 8 - 7 - 2 - 2, // 493
		"#verylongchannelname123": 512 - 8 - 23 - 2 - 2,
	}
	for target, want := range cases {
		if got := privmsgPayloadBudget(target); got != want {
			t.Errorf("privmsgPayloadBudget(%q) = %d, want %d", target, got, want)
		}
	}
	// Budget must shrink as the target gets longer.
	short := privmsgPayloadBudget("#a")
	long := privmsgPayloadBudget("#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if long >= short {
		t.Errorf("budget for longer target (%d) should be smaller than for shorter (%d)", long, short)
	}
}

func TestSendMessageLongTargetUsesTighterBudget(t *testing.T) {
	srv := newFakeIRCServer(t)
	a := newTestAdapter(srv.addr(), nil)

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

	target := "#verylongchannelname123"
	long := strings.Repeat("a", 1000)
	if err := a.Send(context.Background(), target, long); err != nil {
		t.Fatalf("Send: %v", err)
	}

	budget := privmsgPayloadBudget(target)
	deadline = time.Now().Add(5 * time.Second)
	var msgs []string
	for time.Now().Before(deadline) {
		msgs = srv.privmsgLines()
		if len(msgs) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, m := range msgs {
		payload := strings.TrimPrefix(m, "PRIVMSG "+target+" :")
		if len(payload) > budget {
			t.Errorf("chunk of %d bytes exceeds target-derived budget %d", len(payload), budget)
		}
	}
}

func TestSplitMessage(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		maxBytes int
		maxChars int
	}{
		{"short", "hello", 400, 350},
		{"ascii long", strings.Repeat("a", 1000), 400, 350},
		{"utf8 long", strings.Repeat("あ", 300), 400, 350},
		{"exact bytes", strings.Repeat("b", 400), 400, 350},
		{"char capped", strings.Repeat("a", 400), 495, 350},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chunks := splitMessage(tc.text, tc.maxBytes, tc.maxChars)
			var rebuilt strings.Builder
			total := 0
			for _, c := range chunks {
				if len(c) > tc.maxBytes {
					t.Errorf("chunk of %d bytes exceeds byte limit %d", len(c), tc.maxBytes)
				}
				if utf8.RuneCountInString(c) > tc.maxChars {
					t.Errorf("chunk of %d chars exceeds char limit %d", utf8.RuneCountInString(c), tc.maxChars)
				}
				rebuilt.WriteString(c)
				total += len(c)
			}
			if rebuilt.String() != tc.text {
				t.Error("chunks do not reassemble to the original text")
			}
			if total != len(tc.text) {
				t.Errorf("byte count mismatch: %d vs %d", total, len(tc.text))
			}
		})
	}
}

func TestSanitizeIrcOutboundText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello", "hello"},
		{"line1\r\nline2", "line1 line2"},
		{"line1\nline2", "line1 line2"},
		{"a\x02b", "ab"},                      // C0 control stripped
		{"a\tb", "ab"},                        // tab stripped (matches the reference implementation)
		{"a\x7fb", "ab"},                      // DEL stripped
		{"a\u0085b", "ab"},                    // C1 NEL stripped
		{"a\u009fb", "ab"},                    // C1 upper bound stripped
		{"  padded  ", "padded"},              // trimmed
		{"caf\u00e9 \u4f60\u597d", "café 你好"}, // printable non-ASCII preserved
	}
	for _, tc := range cases {
		if got := sanitizeIrcOutboundText(tc.in); got != tc.want {
			t.Errorf("sanitizeIrcOutboundText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// nickCollideServer rejects the first NICK with 433, accepts the retried
// nick and then welcomes the client.
type nickCollideServer struct {
	listener net.Listener
	mu       sync.Mutex
	nicks    []string
	closed   sync.Once
}

func newNickCollideServer(t *testing.T) *nickCollideServer {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &nickCollideServer{listener: l}
	go s.serve()
	t.Cleanup(func() { s.closed.Do(func() { _ = l.Close() }) })
	return s
}

func (s *nickCollideServer) addr() string { return s.listener.Addr().String() }

func (s *nickCollideServer) serve() {
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
		switch {
		case strings.HasPrefix(line, "NICK "):
			nick := strings.TrimPrefix(line, "NICK ")
			s.mu.Lock()
			s.nicks = append(s.nicks, nick)
			count := len(s.nicks)
			s.mu.Unlock()
			if count == 1 {
				fmt.Fprintf(conn, ":srv 433 * %s :Nickname is already in use\r\n", nick)
			} else {
				fmt.Fprintf(conn, ":srv 001 %s :Welcome\r\n", nick)
				fmt.Fprint(conn, ":alice!alice@example.com PRIVMSG #test :registered after collision\r\n")
			}
		case strings.HasPrefix(line, "USER"):
			// Registration finishes via 001 sent after the accepted NICK.
		}
	}
}

func TestNickCollisionFallback(t *testing.T) {
	srv := newNickCollideServer(t)
	a := newTestAdapter(srv.addr(), nil)

	var mu sync.Mutex
	got := ""
	a.OnMessage(func(m plugin.IncomingMessage) {
		mu.Lock()
		got = m.Text
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	// The adapter must register after appending "_" and receive messages.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		text := got
		mu.Unlock()
		if text == "registered after collision" {
			srv.mu.Lock()
			nicks := append([]string(nil), srv.nicks...)
			srv.mu.Unlock()
			if len(nicks) != 2 || nicks[0] != "testbot" || nicks[1] != "testbot_" {
				t.Fatalf("nicks = %v, want [testbot testbot_]", nicks)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("adapter never completed registration after 433")
}

func TestValidate(t *testing.T) {
	a := &Adapter{server: ""}
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "IRC_SERVER") {
		t.Errorf("Validate() = %v, want IRC_SERVER error", err)
	}
	a.server = "irc.example.com:6667"
	if err := a.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}
