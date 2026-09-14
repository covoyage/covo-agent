package nostr

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
)

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// signedDMEvent builds a fully valid, signed kind:4 DM event from an
// independent sender key to the adapter's key.
func signedDMEvent(t *testing.T, a *Adapter, text string, createdAt int64) *Event {
	t.Helper()
	senderSK, err := GenerateSeckey()
	if err != nil {
		t.Fatalf("generate sender key: %v", err)
	}
	recSK, err := hex.DecodeString(a.privateKey)
	if err != nil {
		t.Fatalf("decode adapter key: %v", err)
	}
	recX, err := xOnlyPubkey(recSK)
	if err != nil {
		t.Fatalf("derive receiver pubkey: %v", err)
	}
	senderX, err := xOnlyPubkey(senderSK)
	if err != nil {
		t.Fatalf("derive sender pubkey: %v", err)
	}

	content, err := NIP04Encrypt(senderSK, recX, text)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ev := &Event{
		Content:   content,
		CreatedAt: createdAt,
		Kind:      kindDM,
		Pubkey:    hex.EncodeToString(senderX),
		Tags:      [][]string{{"p", hex.EncodeToString(recX)}},
	}
	if err := ev.SignEvent(kindDM, senderSK); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return ev
}

// TestHandleEventAcceptsValidSignature verifies that a properly signed and
// correctly-serialized event reaches the callback.
func TestHandleEventAcceptsValidSignature(t *testing.T) {
	sk, err := GenerateSeckey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	a := &Adapter{privateKey: hex.EncodeToString(sk), logger: testLogger(t)}

	ev := signedDMEvent(t, a, "hello nostr", time.Now().Unix())
	payload, err := json.Marshal(*ev)
	if err != nil {
		t.Fatal(err)
	}

	got := make(chan plugin.IncomingMessage, 1)
	a.OnMessage(func(m plugin.IncomingMessage) { got <- m })
	a.handleEvent(payload)

	select {
	case m := <-got:
		if m.Text != "hello nostr" {
			t.Errorf("text = %q, want hello nostr", m.Text)
		}
		if m.ChannelID != ev.Pubkey {
			t.Errorf("channelID = %q, want sender pubkey", m.ChannelID)
		}
	case <-time.After(time.Second):
		t.Fatal("callback not invoked for valid event")
	}
}

// TestHandleEventRejectsForgedEvent verifies that events with tampered
// content (id mismatch) or invalid signatures are dropped.
func TestHandleEventRejectsForgedEvent(t *testing.T) {
	sk, err := GenerateSeckey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	a := &Adapter{privateKey: hex.EncodeToString(sk), logger: testLogger(t)}

	got := make(chan plugin.IncomingMessage, 1)
	a.OnMessage(func(m plugin.IncomingMessage) { got <- m })

	t.Run("tampered content breaks the id", func(t *testing.T) {
		ev := signedDMEvent(t, a, "legit", time.Now().Unix())
		ev.Content = ev.Content[:len(ev.Content)-4] + "WF9d" // corrupt ciphertext
		payload, _ := json.Marshal(*ev)
		a.handleEvent(payload)
	})

	t.Run("bad signature encoding", func(t *testing.T) {
		ev := signedDMEvent(t, a, "legit", time.Now().Unix())
		ev.Sig = "zz" + ev.Sig[2:]
		payload, _ := json.Marshal(*ev)
		a.handleEvent(payload)
	})

	t.Run("wrong key signature", func(t *testing.T) {
		ev := signedDMEvent(t, a, "legit", time.Now().Unix())
		otherSK, err := GenerateSeckey()
		if err != nil {
			t.Fatal(err)
		}
		if err := ev.SignEvent(kindDM, otherSK); err != nil {
			t.Fatal(err)
		}
		// Re-signing changed id/sig consistently, so corrupt the sig only.
		sig, _ := hex.DecodeString(ev.Sig)
		sig[0] ^= 0xff
		ev.Sig = hex.EncodeToString(sig)
		payload, _ := json.Marshal(*ev)
		a.handleEvent(payload)
	})

	select {
	case m := <-got:
		t.Errorf("forged event reached callback: %+v", m)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestSinceCursor verifies the subscription since cursor: it starts at "now",
// advances with received events, and does not reset to now on reconnect.
func TestSinceCursor(t *testing.T) {
	start := time.Now().Unix()
	a := &Adapter{logger: testLogger(t)}

	if got := a.sinceFor(start); got != start {
		t.Errorf("initial since = %d, want %d (now)", got, start)
	}

	a.recordEventAt(start - 100) // older event must not move the cursor back
	if got := a.sinceFor(start); got != start {
		t.Errorf("since after older event = %d, want %d", got, start)
	}

	a.recordEventAt(start + 50)
	if got := a.sinceFor(start + 1000); got != start+50 {
		t.Errorf("since after event = %d, want %d (reconnect must not reset to now)", got, start+50)
	}
}

// TestSplitChunks verifies the 4000-rune outbound chunking.
func TestSplitChunks(t *testing.T) {
	if got := splitChunks("short", maxChunkRunes); len(got) != 1 || got[0] != "short" {
		t.Errorf("single chunk expected, got %v", got)
	}
	text := ""
	var wantLens []int
	for i := 0; i < 9500; i++ {
		text += "日" // multi-byte runes
	}
	for n := len([]rune(text)); n > 0; n -= maxChunkRunes {
		l := maxChunkRunes
		if n < l {
			l = n
		}
		wantLens = append(wantLens, l)
	}
	got := splitChunks(text, maxChunkRunes)
	if len(got) != len(wantLens) {
		t.Fatalf("chunks = %d, want %d", len(got), len(wantLens))
	}
	joined := ""
	for i, c := range got {
		if len([]rune(c)) != wantLens[i] {
			t.Errorf("chunk %d length = %d, want %d", i, len([]rune(c)), wantLens[i])
		}
		joined += c
	}
	if joined != text {
		t.Error("chunked text does not reassemble to the original")
	}
}
