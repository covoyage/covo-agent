package signal

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
)

type fakeServer struct {
	t           *testing.T
	ln          net.Listener
	mu          sync.Mutex
	conns       []net.Conn
	respondErr  bool // respond to "send" with a JSON-RPC error
	gotRequests chan map[string]any
}

func startFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeServer{
		t:           t,
		ln:          ln,
		gotRequests: make(chan map[string]any, 32),
	}
	go s.acceptLoop()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *fakeServer) addr() string { return s.ln.Addr().String() }

func (s *fakeServer) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, conn)
		s.mu.Unlock()
		go s.handleConn(conn)
	}
}

func (s *fakeServer) handleConn(conn net.Conn) {
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		var req map[string]any
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		s.gotRequests <- req

		method, _ := req["method"].(string)
		id, _ := req["id"].(float64)
		switch method {
		case "subscribe":
			s.writeLine(conn, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, int64(id)))
		case "send":
			if s.respondErr {
				s.writeLine(conn, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"error":{"code":-1,"message":"not registered"}}`, int64(id)))
			} else {
				s.writeLine(conn, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"timestamp":1690000000}}`, int64(id)))
			}
		}
	}
}

func (s *fakeServer) writeLine(conn net.Conn, line string) {
	s.t.Helper()
	if _, err := conn.Write([]byte(line + "\n")); err != nil {
		s.t.Logf("fake server write: %v", err)
	}
}

// pushEvent writes a server-initiated receive event to the first connection.
func (s *fakeServer) pushEvent(line string) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.conns) == 0 {
		s.t.Fatal("no connections to push to")
	}
	if _, err := s.conns[0].Write([]byte(line + "\n")); err != nil {
		s.t.Fatalf("push event: %v", err)
	}
}

func newTestAdapter(addr string) *Adapter {
	return &Adapter{
		socketAddr:  addr,
		account:     "+15550000000",
		pollSeconds: 1,
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		pending:     make(map[int64]chan rpcFrame),
	}
}

func TestSendAndReceive(t *testing.T) {
	fake := startFakeServer(t)
	a := newTestAdapter(fake.addr())

	callbacks := make(chan plugin.IncomingMessage, 8)
	a.OnMessage(func(msg plugin.IncomingMessage) { callbacks <- msg })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	// Server should have received the subscribe request.
	select {
	case req := <-fake.gotRequests:
		if req["method"] != "subscribe" {
			t.Fatalf("expected subscribe, got %v", req["method"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no subscribe request received")
	}

	// Send via the persistent connection.
	if err := a.SendMessage(ctx, "+15551112222", plugin.OutgoingMessage{Text: "hello signal"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	select {
	case req := <-fake.gotRequests:
		if req["method"] != "send" {
			t.Fatalf("expected send, got %v", req["method"])
		}
		params, _ := req["params"].(map[string]any)
		if params["account"] != a.account {
			t.Fatalf("unexpected account param: %v", params)
		}
		recipients, _ := params["recipient"].([]any)
		if len(recipients) != 1 || recipients[0] != "+15551112222" {
			t.Fatalf("unexpected recipient param: %v", params["recipient"])
		}
		if params["message"] != "hello signal" {
			t.Fatalf("unexpected message param: %v", params["message"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no send request received")
	}

	// Server pushes an inbound 1:1 message.
	fake.pushEvent(`{"jsonrpc":"2.0","method":"receive","params":{"envelope":{"source":"+15551112222","sourceNumber":"+15551112222","timestampMs":1690000001000,"dataMessage":{"message":"hi from friend"}}}}`)
	select {
	case msg := <-callbacks:
		if msg.Platform != "signal" || msg.ChannelID != "+15551112222" ||
			msg.UserID != "+15551112222" || msg.Text != "hi from friend" {
			t.Fatalf("unexpected callback: %+v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no callback for pushed event")
	}

	// Server pushes a group message; the channel is marked with the group prefix.
	fake.pushEvent(`{"jsonrpc":"2.0","method":"subscription","params":{"envelope":{"source":"+15553334444","dataMessage":{"message":"group hello","groupInfo":{"groupId":"abc123"}}}}}`)
	select {
	case msg := <-callbacks:
		if msg.ChannelID != "group.abc123" || msg.UserID != "+15553334444" || msg.Text != "group hello" {
			t.Fatalf("unexpected group callback: %+v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no callback for group event")
	}

	// Replying to the group must use the groupId param without recipient.
	if err := a.SendMessage(ctx, "group.abc123", plugin.OutgoingMessage{Text: "group reply"}); err != nil {
		t.Fatalf("SendMessage(group): %v", err)
	}
	select {
	case req := <-fake.gotRequests:
		if req["method"] != "send" {
			t.Fatalf("expected send, got %v", req["method"])
		}
		params, _ := req["params"].(map[string]any)
		if params["groupId"] != "abc123" {
			t.Fatalf("unexpected groupId param: %v", params)
		}
		if _, hasRecipient := params["recipient"]; hasRecipient {
			t.Fatalf("group send must not carry recipient: %v", params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no group send request received")
	}

	// The "group:" prefix variant is also accepted on outbound sends.
	if err := a.SendMessage(ctx, "group:abc123", plugin.OutgoingMessage{Text: "group reply 2"}); err != nil {
		t.Fatalf("SendMessage(group:): %v", err)
	}
	select {
	case req := <-fake.gotRequests:
		params, _ := req["params"].(map[string]any)
		if params["groupId"] != "abc123" {
			t.Fatalf("unexpected groupId param: %v", params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no group send request received")
	}
}

func TestGroupIDFromChannel(t *testing.T) {
	for _, tc := range []struct {
		in    string
		group string
		ok    bool
	}{
		{"+15551112222", "+15551112222", false},
		{"group.abc123", "abc123", true},
		{"group:abc123", "abc123", true},
	} {
		g, ok := groupIDFromChannel(tc.in)
		if ok != tc.ok || g != tc.group {
			t.Errorf("groupIDFromChannel(%q) = (%q, %v), want (%q, %v)", tc.in, g, ok, tc.group, tc.ok)
		}
	}
}

func TestSendErrorPath(t *testing.T) {
	fake := startFakeServer(t)
	fake.respondErr = true
	a := newTestAdapter(fake.addr())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	err := a.SendMessage(ctx, "+15551112222", plugin.OutgoingMessage{Text: "hello"})
	if err == nil {
		t.Fatal("expected send error")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("error should contain json-rpc message: %v", err)
	}
}

func TestOneShotSendWithoutStart(t *testing.T) {
	fake := startFakeServer(t)
	a := newTestAdapter(fake.addr())

	// SendMessage before Start uses a fresh per-call connection.
	if err := a.Send(context.Background(), "+15551112222", "pre-start"); err != nil {
		t.Fatalf("Send without Start: %v", err)
	}
	select {
	case req := <-fake.gotRequests:
		if req["method"] != "send" {
			t.Fatalf("expected send, got %v", req["method"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no send request received")
	}
}

func TestStartFailsWithoutDaemon(t *testing.T) {
	// Nothing listening on this port.
	a := newTestAdapter("127.0.0.1:1")
	err := a.Start(context.Background())
	if err == nil {
		a.Stop()
		t.Fatal("expected Start error when signal-cli is unreachable")
	}
	if !strings.Contains(err.Error(), "signal-cli -u") {
		t.Fatalf("error should hint at signal-cli startup command: %v", err)
	}
}

func TestValidate(t *testing.T) {
	a := &Adapter{}
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "SIGNAL_ACCOUNT") {
		t.Fatalf("unexpected validate error: %v", err)
	}
	a = &Adapter{account: "+15550000000"}
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "SIGNAL_CLI_SOCKET") {
		t.Fatalf("unexpected validate error: %v", err)
	}
}
