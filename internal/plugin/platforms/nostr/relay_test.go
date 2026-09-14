package nostr

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// fakeRelay is an in-process websocket relay speaking the NIP-01 wire protocol
// over plain TCP (ws://). It records REQ subscriptions and EVENT messages,
// replying ["OK", ...] to the latter, and can push events to the client.
type fakeRelay struct {
	listener net.Listener
	url      string

	mu          sync.Mutex
	subFilters  []map[string]any
	events      []map[string]any
	clientConns []net.Conn
}

func newFakeRelay(t *testing.T) *fakeRelay {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fr := &fakeRelay{listener: l, url: "ws://" + l.Addr().String()}
	go fr.serve()
	t.Cleanup(func() { _ = l.Close() })
	return fr
}

func (fr *fakeRelay) serve() {
	for {
		conn, err := fr.listener.Accept()
		if err != nil {
			return
		}
		if _, err := ws.Upgrade(conn); err != nil {
			_ = conn.Close()
			return
		}
		fr.mu.Lock()
		fr.clientConns = append(fr.clientConns, conn)
		fr.mu.Unlock()
		go fr.readLoop(conn)
	}
}

func (fr *fakeRelay) readLoop(conn net.Conn) {
	defer conn.Close()
	for {
		data, _, err := wsutil.ReadClientData(conn)
		if err != nil {
			return
		}
		var frame []json.RawMessage
		if json.Unmarshal(data, &frame) != nil || len(frame) == 0 {
			continue
		}
		var msgType string
		if json.Unmarshal(frame[0], &msgType) != nil {
			continue
		}
		switch msgType {
		case "REQ":
			var filter map[string]any
			if len(frame) >= 3 && json.Unmarshal(frame[2], &filter) == nil {
				fr.mu.Lock()
				fr.subFilters = append(fr.subFilters, filter)
				fr.mu.Unlock()
			}
		case "EVENT":
			var ev map[string]any
			if len(frame) >= 2 && json.Unmarshal(frame[1], &ev) == nil {
				fr.mu.Lock()
				fr.events = append(fr.events, ev)
				fr.mu.Unlock()
				ok, _ := json.Marshal([]any{"OK", ev["id"], true, ""})
				_ = wsutil.WriteServerText(conn, ok)
			}
		}
	}
}

func (fr *fakeRelay) subscriptionFilters() []map[string]any {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	out := make([]map[string]any, len(fr.subFilters))
	copy(out, fr.subFilters)
	return out
}

func (fr *fakeRelay) receivedEvents() []map[string]any {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	out := make([]map[string]any, len(fr.events))
	copy(out, fr.events)
	return out
}

func (fr *fakeRelay) lastConn() net.Conn {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	if len(fr.clientConns) == 0 {
		return nil
	}
	return fr.clientConns[len(fr.clientConns)-1]
}

// pushDM builds a signed kind:4 NIP-04 event from encKey to the given
// recipient and writes it over the given connection as ["EVENT", subID, ev].
func pushDM(t *testing.T, conn net.Conn, encKey []byte, recipientXOnlyHex, subID, plaintext string) {
	t.Helper()
	if conn == nil {
		t.Fatal("no client connection to push on")
	}
	recX, err := hex.DecodeString(recipientXOnlyHex)
	if err != nil {
		t.Fatal(err)
	}
	content, err := NIP04Encrypt(encKey, recX, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	pubX, _ := xOnlyPubkey(encKey)
	ev := &Event{
		Content:   content,
		CreatedAt: time.Now().Unix(),
		Kind:      4,
		Pubkey:    hex.EncodeToString(pubX),
		Tags:      [][]string{{"p", recipientXOnlyHex}},
	}
	if err := ev.SignEvent(4, encKey); err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal([]any{"EVENT", subID, ev})
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := wsutil.WriteServerText(conn, frame); err != nil {
		t.Fatal(err)
	}
}

func newTestAdapter(t *testing.T, relayURLs ...string) *Adapter {
	t.Helper()
	sec, err := GenerateSeckey()
	if err != nil {
		t.Fatal(err)
	}
	return &Adapter{
		privateKey: hex.EncodeToString(sec),
		relays:     relayURLs,
		logger:     slog.New(slog.NewTextHandler(discardWriter{}, nil)),
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func mustHexDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func TestFakeRelayReceive(t *testing.T) {
	fr := newFakeRelay(t)
	a := newTestAdapter(t, fr.url)

	senderKey, err := GenerateSeckey()
	if err != nil {
		t.Fatal(err)
	}
	senderPubX, _ := xOnlyPubkey(senderKey)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = a.Stop() }()

	// Wait for the REQ subscription and validate its filter.
	waitFor(t, 3*time.Second, "subscription", func() bool {
		return len(fr.subscriptionFilters()) > 0
	})
	ourPubX, _ := xOnlyPubkey(mustHexDecode(t, a.privateKey))
	f := fr.subscriptionFilters()[0]
	kinds, _ := f["kinds"].([]any)
	if len(kinds) != 1 || kinds[0].(float64) != 4 {
		t.Errorf("filter kinds = %v, want [4]", f["kinds"])
	}
	pList, _ := f["#p"].([]any)
	if len(pList) != 1 || pList[0] != hex.EncodeToString(ourPubX) {
		t.Errorf("filter #p = %v, want our pubkey", pList)
	}
	since, ok := f["since"].(float64)
	if !ok || since <= 0 {
		t.Errorf("filter since = %v", f["since"])
	}

	// Push an encrypted DM; expect the callback with decrypted text.
	got := make(chan plugin.IncomingMessage, 1)
	a.OnMessage(func(m plugin.IncomingMessage) {
		select {
		case got <- m:
		default:
		}
	})
	pushDM(t, fr.lastConn(), senderKey, hex.EncodeToString(ourPubX), subID, "hello from relay")

	select {
	case m := <-got:
		if m.Text != "hello from relay" {
			t.Errorf("text = %q", m.Text)
		}
		if m.ChannelID != hex.EncodeToString(senderPubX) {
			t.Errorf("channelID = %q, want sender pubkey", m.ChannelID)
		}
		if m.UserID != m.ChannelID {
			t.Errorf("userID = %q, want channelID", m.UserID)
		}
		if m.Platform != PlatformName {
			t.Errorf("platform = %q", m.Platform)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("callback not invoked for pushed DM")
	}
}

func TestFakeRelaySendMessage(t *testing.T) {
	fr := newFakeRelay(t)
	a := newTestAdapter(t, fr.url)

	recKey, err := GenerateSeckey()
	if err != nil {
		t.Fatal(err)
	}
	recPubX, _ := xOnlyPubkey(recKey)
	recPubHex := hex.EncodeToString(recPubX)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = a.Stop() }()

	if err := a.SendMessage(ctx, recPubHex, plugin.OutgoingMessage{Text: "dm outbound"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// The relay must have received exactly one kind:4 event.
	waitFor(t, 3*time.Second, "relay event", func() bool {
		return len(fr.receivedEvents()) == 1
	})
	ev := fr.receivedEvents()[0]
	if kind, _ := ev["kind"].(float64); kind != 4 {
		t.Errorf("kind = %v", ev["kind"])
	}
	ourPubX, _ := xOnlyPubkey(mustHexDecode(t, a.privateKey))
	if ev["pubkey"] != hex.EncodeToString(ourPubX) {
		t.Errorf("pubkey = %v", ev["pubkey"])
	}
	if ev["id"] == "" || ev["sig"] == "" {
		t.Errorf("id/sig missing: %v", ev)
	}

	// tags contain ["p", recipient]
	tags, _ := ev["tags"].([]any)
	found := false
	for _, tag := range tags {
		pair, _ := tag.([]any)
		if len(pair) == 2 && pair[0] == "p" && pair[1] == recPubHex {
			found = true
		}
	}
	if !found {
		t.Errorf("tags missing [\"p\", recipient]: %v", tags)
	}

	// Content must decrypt to the original plaintext with the recipient key.
	content, _ := ev["content"].(string)
	text, err := NIP04Decrypt(mustHexDecode(t, a.privateKey), recPubX, content)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if text != "dm outbound" {
		t.Errorf("decrypted text = %q", text)
	}

	// Verify the id/signature cryptographically.
	evObj := &Event{
		Content:   content,
		CreatedAt: int64(ev["created_at"].(float64)),
		Kind:      4,
		Pubkey:    ev["pubkey"].(string),
		Sig:       ev["sig"].(string),
		Tags:      [][]string{{"p", recPubHex}},
	}
	id, err := evObj.SerializeID()
	if err != nil {
		t.Fatal(err)
	}
	if id != ev["id"] {
		t.Errorf("event id mismatch: computed %s, sent %v", id, ev["id"])
	}
	sigBytes, _ := hex.DecodeString(evObj.Sig)
	if !bip340Verify(mustHexDecode(t, id), ourPubX, sigBytes) {
		t.Error("sent event signature does not verify")
	}
}

func TestFakeRelaySendMessageBadChannelID(t *testing.T) {
	fr := newFakeRelay(t)
	a := newTestAdapter(t, fr.url)
	ctx := context.Background()
	if err := a.SendMessage(ctx, "not-a-pubkey", plugin.OutgoingMessage{Text: "x"}); err == nil {
		t.Fatal("expected error for invalid channelID")
	}
	if err := a.SendMessage(ctx, "", plugin.OutgoingMessage{Text: ""}); err != nil {
		t.Fatalf("empty text should be a no-op, got %v", err)
	}
}
