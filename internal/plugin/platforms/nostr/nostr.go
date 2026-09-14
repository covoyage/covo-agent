// Package nostr implements a Nostr platform adapter providing NIP-04
// encrypted direct messages (kind:4) over NIP-01 relay connections.
//
// Environment:
//
//	NOSTR_PRIVATE_KEY  hex-encoded 32-byte secp256k1 secret key (required)
//	NOSTR_RELAYS       comma-separated relay URLs (default
//	                   wss://relay.damus.io,wss://nos.lol)
//	NOSTR_LISTEN_ADDR  reserved for future inbound listeners; currently unused
//
// Inbound: each relay gets a persistent websocket with a REQ subscription for
// kind:4 events addressed to our pubkey (#p filter). Reconnects use
// exponential backoff. Outbound: kind:4 events are signed (BIP-340) and sent
// on a per-send websocket connection, awaiting the relay's ["OK", ...]
// response.
package nostr

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
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
	PlatformName = "nostr"

	kindDM          = 4
	subID           = "covo-agent-sub1"
	okWaitTimeout   = 10 * time.Second
	reconnectMin    = 1 * time.Second
	reconnectMax    = 30 * time.Second
	stableConnAfter = time.Minute
	maxChunkRunes   = 4000 // outbound text chunk limit, per openclaw gateway.ts textChunkLimit
)

type Adapter struct {
	privateKey string // hex secret key
	relays     []string
	logger     *slog.Logger
	httpClient *http.Client

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	pubkeyHex string
	// lastEventAt is the created_at of the most recent inbound event; used
	// as the subscription `since` cursor on reconnect so no events are
	// missed (openclaw nostr-bus keeps a durable cursor; we keep it
	// in-process — restart resumes from "now").
	lastEventAt int64
	// sinceBaseline is the "now" of the first subscription; events older
	// than it never move the cursor backwards.
	sinceBaseline int64

	connMu   sync.Mutex
	recvConn map[string]net.Conn // receive connections keyed by relay URL
}

// trackConn registers a receive connection for clean shutdown.
func (a *Adapter) trackConn(relayURL string, conn net.Conn) {
	a.connMu.Lock()
	if a.recvConn == nil {
		a.recvConn = make(map[string]net.Conn)
	}
	a.recvConn[relayURL] = conn
	a.connMu.Unlock()
}

func (a *Adapter) untrackConn(relayURL string) {
	a.connMu.Lock()
	delete(a.recvConn, relayURL)
	a.connMu.Unlock()
}

func (a *Adapter) closeConns() {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	for url, conn := range a.recvConn {
		_ = conn.Close()
		delete(a.recvConn, url)
	}
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	relaysStr := strings.TrimSpace(os.Getenv("NOSTR_RELAYS"))
	var relays []string
	if relaysStr != "" {
		for _, r := range strings.Split(relaysStr, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				relays = append(relays, r)
			}
		}
	}
	if len(relays) == 0 {
		relays = []string{"wss://relay.damus.io", "wss://nos.lol"}
	}

	return &Adapter{
		privateKey: strings.TrimSpace(os.Getenv("NOSTR_PRIVATE_KEY")),
		relays:     relays,
		logger:     logger,
		httpClient: &http.Client{Timeout: okWaitTimeout},
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
	if a.privateKey == "" {
		return fmt.Errorf("NOSTR_PRIVATE_KEY not set in environment")
	}
	if _, err := ParsePrivateKey(a.privateKey); err != nil {
		return fmt.Errorf("nostr: %w", err)
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
		return fmt.Errorf("nostr: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("nostr: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	seckey, err := hex.DecodeString(a.privateKey)
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("nostr: decode private key: %w", err)
	}
	pub, err := xOnlyPubkey(seckey)
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("nostr: derive public key: %w", err)
	}
	a.mu.Lock()
	a.pubkeyHex = hex.EncodeToString(pub)
	a.mu.Unlock()

	a.logger.Info("nostr: adapter started", "pubkey", a.pubkeyHex, "relays", a.relays)

	for _, relay := range a.relays {
		go a.relayLoop(ctx, relay)
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
	a.closeConns()
	a.logger.Info("nostr: adapter stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

// SendMessage sends a NIP-04 encrypted kind:4 DM. channelID is the
// recipient's public key (64-char x-only or 66-char compressed hex). Text
// longer than maxChunkRunes (4000, per openclaw gateway.ts textChunkLimit)
// is split and sent as consecutive events.
func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" {
		return nil
	}
	if err := a.Validate(); err != nil {
		return fmt.Errorf("nostr: %w", err)
	}

	seckey, err := hex.DecodeString(a.privateKey)
	if err != nil {
		return fmt.Errorf("nostr: decode private key: %w", err)
	}
	targetX, err := parsePubkeyX(channelID)
	if err != nil {
		return fmt.Errorf("nostr: %w", err)
	}

	for _, chunk := range splitChunks(msg.Text, maxChunkRunes) {
		if err := a.sendChunk(ctx, seckey, targetX, chunk); err != nil {
			return err
		}
	}
	return nil
}

// splitChunks splits text into chunks of at most maxLen runes.
func splitChunks(text string, maxLen int) []string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(runes) > 0 {
		n := maxLen
		if len(runes) < n {
			n = len(runes)
		}
		chunks = append(chunks, string(runes[:n]))
		runes = runes[n:]
	}
	return chunks
}

// sendChunk encrypts, signs and publishes one text chunk as a kind:4 event.
func (a *Adapter) sendChunk(ctx context.Context, seckey, targetX []byte, text string) error {
	content, err := NIP04Encrypt(seckey, targetX, text)
	if err != nil {
		return fmt.Errorf("nostr: encrypt: %w", err)
	}

	ev := &Event{
		Content:   content,
		CreatedAt: time.Now().Unix(),
		Kind:      kindDM,
		Pubkey:    a.pubkey(ctx),
		Tags:      [][]string{{"p", hex.EncodeToString(targetX)}},
	}
	if ev.Pubkey == "" {
		pub, err := xOnlyPubkey(seckey)
		if err != nil {
			return fmt.Errorf("nostr: derive pubkey: %w", err)
		}
		ev.Pubkey = hex.EncodeToString(pub)
		a.mu.Lock()
		a.pubkeyHex = ev.Pubkey
		a.mu.Unlock()
	}
	if err := ev.SignEvent(kindDM, seckey); err != nil {
		return err
	}

	wire, err := ev.MarshalWire()
	if err != nil {
		return err
	}

	// Try each relay until one accepts the event.
	var lastErr error
	for _, relay := range a.relays {
		if err := a.sendToRelay(ctx, relay, ev.ID, wire); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("nostr: send cancelled: %w", err)
			}
			lastErr = fmt.Errorf("nostr: relay %s: %w", relay, err)
			continue
		}
		return nil
	}
	return lastErr
}

func (a *Adapter) pubkey(_ context.Context) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pubkeyHex
}

// sendToRelay dials a relay, writes ["EVENT", ev] and waits for the
// matching ["OK", id, ...] response.
func (a *Adapter) sendToRelay(ctx context.Context, relayURL, eventID string, wire []byte) error {
	conn, _, _, err := a.dial(ctx, relayURL)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	if err := wsutil.WriteClientText(conn, wire); err != nil {
		return fmt.Errorf("write EVENT: %w", err)
	}

	deadline := time.Now().Add(okWaitTimeout)
	_ = conn.SetReadDeadline(deadline)
	for time.Now().Before(deadline) {
		data, _, err := wsutil.ReadServerData(conn)
		if err != nil {
			return fmt.Errorf("read OK response: %w", err)
		}
		msgType, id, ok, reason, payload, err := parseRelayMessage(data)
		if err != nil {
			continue // ignore malformed/notice frames
		}
		switch msgType {
		case "OK":
			if id == eventID {
				if !ok {
					return fmt.Errorf("relay rejected event: %s", reason)
				}
				return nil
			}
		case "EVENT":
			// Unsolicited event on this connection; ignore for sends.
			_ = payload
		}
	}
	return fmt.Errorf("timed out waiting for OK response")
}

// relayLoop maintains a long-lived subscription connection to one relay,
// reconnecting with exponential backoff until ctx is cancelled.
func (a *Adapter) relayLoop(ctx context.Context, relayURL string) {
	backoff := reconnectMin
	for {
		start := time.Now()
		err := a.relaySession(ctx, relayURL)
		if ctx.Err() != nil {
			return
		}
		a.logger.Warn("nostr: relay disconnected, reconnecting", "relay", relayURL, "error", err)

		if time.Since(start) > stableConnAfter {
			backoff = reconnectMin
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > reconnectMax {
			backoff = reconnectMax
		}
	}
}

// relaySession subscribes to our kind:4 DMs and pumps events to the
// message callback until an error occurs or ctx is cancelled.
func (a *Adapter) relaySession(ctx context.Context, relayURL string) error {
	conn, _, _, err := a.dial(ctx, relayURL)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	a.trackConn(relayURL, conn)
	defer func() {
		a.untrackConn(relayURL)
		_ = conn.Close()
	}()

	pubkey := a.pubkey(ctx)
	req, err := jsonMarshal([]any{
		"REQ", subID, map[string]any{
			"kinds": []int{kindDM},
			"#p":    []string{pubkey},
			"since": a.sinceFor(time.Now().Unix()),
		},
	})
	if err != nil {
		return fmt.Errorf("marshal REQ: %w", err)
	}
	if err := wsutil.WriteClientText(conn, req); err != nil {
		return fmt.Errorf("write REQ: %w", err)
	}

	// Keepalive so dead relays are detected and the loop reconnects.
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				a.mu.Lock()
				running := a.running
				a.mu.Unlock()
				if !running {
					return
				}
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				_ = wsutil.WriteClientMessage(conn, ws.OpText, []byte(`["PING"]`))
			}
		}
	}()

	for {
		data, _, err := wsutil.ReadServerData(conn)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read: %w", err)
		}
		a.handleRelayFrame(data)
	}
}

// handleRelayFrame processes one relay message: ["EVENT", subid, event].
func (a *Adapter) handleRelayFrame(data []byte) {
	msgType, sub, _, _, payload, err := parseRelayMessage(data)
	if err != nil {
		a.logger.Debug("nostr: ignoring malformed relay frame", "error", err)
		return
	}
	switch msgType {
	case "NOTICE":
		a.logger.Info("nostr: relay notice", "notice", string(payload))
	case "CLOSED":
		a.logger.Warn("nostr: relay closed subscription", "sub", sub)
	case "EVENT":
		if sub != subID || len(payload) == 0 {
			return
		}
		a.handleEvent(payload)
	}
}

// sinceFor returns the subscription `since` cursor: the created_at of the
// last received event when one has already been seen (so reconnects do not
// reset to "now" and miss messages), otherwise the current start time. The
// first call also fixes the baseline that older events cannot undercut.
func (a *Adapter) sinceFor(now int64) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sinceBaseline == 0 {
		a.sinceBaseline = now
	}
	if a.lastEventAt > 0 {
		return a.lastEventAt
	}
	return now
}

// recordEventAt advances the since cursor when a newer event arrives; events
// older than the initial subscription baseline are ignored.
func (a *Adapter) recordEventAt(createdAt int64) {
	a.mu.Lock()
	if a.sinceBaseline > 0 && createdAt < a.sinceBaseline {
		a.mu.Unlock()
		return
	}
	if createdAt > a.lastEventAt {
		a.lastEventAt = createdAt
	}
	a.mu.Unlock()
}

// handleEvent processes a kind:4 event. Before decryption the event is
// fully validated: the id must equal SHA256 of the deterministic NIP-01
// serialization and the BIP-340 signature over the id must verify against
// the sender pubkey. Invalid events are dropped with a log entry.
func (a *Adapter) handleEvent(payload []byte) {
	ev, err := ParseEvent(payload)
	if err != nil {
		a.logger.Warn("nostr: parse event failed", "error", err)
		return
	}
	if ev.Kind != kindDM || ev.Pubkey == "" || ev.Content == "" {
		return
	}

	if err := a.validateEvent(ev); err != nil {
		a.logger.Warn("nostr: dropping invalid event", "id", ev.ID, "error", err)
		return
	}

	senderX, err := parsePubkeyX(ev.Pubkey)
	if err != nil {
		a.logger.Warn("nostr: bad sender pubkey", "error", err)
		return
	}
	seckey, err := hex.DecodeString(a.privateKey)
	if err != nil {
		a.logger.Warn("nostr: decode private key failed", "error", err)
		return
	}

	text, err := NIP04Decrypt(seckey, senderX, ev.Content)
	if err != nil {
		a.logger.Warn("nostr: decrypt DM failed", "error", err)
		return
	}

	a.recordEventAt(ev.CreatedAt)

	a.callback(plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: ev.Pubkey,
		UserID:    ev.Pubkey,
		UserName:  ev.Pubkey,
		Text:      text,
		Timestamp: time.Unix(ev.CreatedAt, 0),
		Raw:       ev,
	})
}

// validateEvent checks the event id against the NIP-01 deterministic
// serialization and verifies the BIP-340 Schnorr signature over the id
// using the sender's x-only public key.
func (a *Adapter) validateEvent(ev *Event) error {
	wantID, err := ev.SerializeID()
	if err != nil {
		return fmt.Errorf("serialize id: %w", err)
	}
	if ev.ID != wantID {
		return fmt.Errorf("event id mismatch: got %s, want %s", ev.ID, wantID)
	}
	sig, err := hex.DecodeString(ev.Sig)
	if err != nil || len(sig) != 64 {
		return fmt.Errorf("invalid signature encoding")
	}
	idBytes, err := hex.DecodeString(ev.ID)
	if err != nil {
		return fmt.Errorf("invalid id encoding: %w", err)
	}
	pub, err := parsePubkeyX(ev.Pubkey)
	if err != nil {
		return fmt.Errorf("invalid pubkey: %w", err)
	}
	if !bip340Verify(idBytes, pub, sig) {
		return fmt.Errorf("BIP-340 signature verification failed")
	}
	return nil
}

// dial opens a websocket to a ws:// or wss:// relay URL.
func (a *Adapter) dial(ctx context.Context, relayURL string) (net.Conn, *bufio.Reader, ws.Handshake, error) {
	conn, br, hs, err := ws.Dial(ctx, relayURL)
	if err != nil {
		return nil, nil, hs, fmt.Errorf("nostr: websocket dial %s: %w", relayURL, err)
	}
	return conn, br, hs, nil
}
