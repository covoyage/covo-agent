package nostr

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestEventIDDeterministic(t *testing.T) {
	ev := &Event{
		Content:   "determinism check <hello & friends> 你好",
		CreatedAt: 1700000000,
		Kind:      4,
		Pubkey:    "dff1d77f2a671c5f36183726db2341be58feae1da2deced843240f7b502ba659",
		Tags:      [][]string{{"p", "aabb"}, {"e", "ccdd"}},
	}
	id1, err := ev.SerializeID()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := ev.SerializeID()
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("serialization not deterministic: %s vs %s", id1, id2)
	}
	if len(id1) != 64 {
		t.Fatalf("id length %d, want 64 hex chars", len(id1))
	}
	// Verify against the hash of the exact NIP-01 serialization.
	b, _ := json.Marshal([]any{0, ev.Pubkey, ev.CreatedAt, ev.Kind, ev.Tags, ev.Content})
	want := sha256Hex(b)
	if id1 != want {
		t.Fatalf("id mismatch: got %s want %s", id1, want)
	}
}

func TestEventIDChangesWithContent(t *testing.T) {
	ev := &Event{Content: "one", CreatedAt: 1, Kind: 4, Pubkey: "aa", Tags: [][]string{}}
	id1, _ := ev.SerializeID()
	ev.Content = "two"
	id2, _ := ev.SerializeID()
	if id1 == id2 {
		t.Fatal("different content produced the same id")
	}
}

func TestEventWireFormat(t *testing.T) {
	ev := &Event{
		Content:   "wire",
		CreatedAt: 1700000000,
		ID:        "deadbeef",
		Kind:      4,
		Pubkey:    "aabbaabb",
		Sig:       "ccddccdd",
		Tags:      [][]string{{"p", "x"}},
	}
	wire, err := ev.MarshalWire()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(wire, []byte(`["EVENT",`)) {
		t.Fatalf("wire message must start with [\"EVENT\",: %s", wire)
	}

	var arr []json.RawMessage
	if err := json.Unmarshal(wire, &arr); err != nil {
		t.Fatal(err)
	}
	if len(arr) != 2 {
		t.Fatalf("wire array length %d", len(arr))
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(arr[1], &m); err != nil {
		t.Fatal(err)
	}
	// All NIP-01 fields present, keys in alphabetical order on the wire.
	order := []string{"content", "created_at", "id", "kind", "pubkey", "sig", "tags"}
	last := -1
	for _, k := range order {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing key %s", k)
		}
		idx := bytes.Index(wire, []byte(`"`+k+`":`))
		if idx < last {
			t.Errorf("key %s out of alphabetical order in %s", k, wire)
		}
		last = idx
	}

	// Determinism: same event marshals to identical bytes.
	wire2, _ := ev.MarshalWire()
	if !bytes.Equal(wire, wire2) {
		t.Fatal("wire serialization not deterministic")
	}
}

func TestSignEventFillsIDAndSig(t *testing.T) {
	sec, _ := hex.DecodeString(testSeckey1Hex)
	pubX, err := xOnlyPubkey(sec)
	if err != nil {
		t.Fatal(err)
	}
	ev := &Event{
		Content:   "sign me",
		CreatedAt: 1700000001,
		Pubkey:    hex.EncodeToString(pubX),
		Tags:      [][]string{{"p", "1122"}},
	}
	if err := ev.SignEvent(4, sec); err != nil {
		t.Fatal(err)
	}
	if ev.Kind != 4 {
		t.Fatalf("kind = %d", ev.Kind)
	}
	if len(ev.ID) != 64 || len(ev.Sig) != 128 {
		t.Fatalf("id/sig lengths: %d/%d", len(ev.ID), len(ev.Sig))
	}
	idBytes, _ := hex.DecodeString(ev.ID)
	sigBytes, _ := hex.DecodeString(ev.Sig)
	if !bip340Verify(idBytes, pubX, sigBytes) {
		t.Fatal("signed event does not verify")
	}
}

func TestParseEventRoundtrip(t *testing.T) {
	ev := &Event{
		Content:   "encrypted-content?iv=xyz",
		CreatedAt: 1700000002,
		ID:        "0123",
		Kind:      4,
		Pubkey:    "4567",
		Sig:       "89ab",
		Tags:      [][]string{{"p", "target"}},
	}
	wire, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseEvent(wire)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Content != ev.Content || parsed.Pubkey != ev.Pubkey || parsed.Sig != ev.Sig ||
		parsed.ID != ev.ID || parsed.Kind != ev.Kind || parsed.CreatedAt != ev.CreatedAt {
		t.Fatalf("roundtrip mismatch: %+v", parsed)
	}
	if len(parsed.Tags) != 1 || parsed.Tags[0][0] != "p" {
		t.Fatalf("tags: %+v", parsed.Tags)
	}
}

func TestParsePubkeyX(t *testing.T) {
	xOnly := "dff1d77f2a671c5f36183726db2341be58feae1da2deced843240f7b502ba659"
	compressed := "02" + xOnly

	got, err := parsePubkeyX(xOnly)
	if err != nil || hex.EncodeToString(got) != xOnly {
		t.Errorf("x-only parse: %v %s", err, got)
	}
	got, err = parsePubkeyX(compressed)
	if err != nil || hex.EncodeToString(got) != xOnly {
		t.Errorf("compressed parse: %v %s", err, got)
	}
	if _, err := parsePubkeyX("zz"); err == nil {
		t.Error("invalid pubkey should fail")
	}
}
