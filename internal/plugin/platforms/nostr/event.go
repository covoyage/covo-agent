package nostr

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
)

// Event is a NIP-01 event object. Field order matters: the struct fields are
// deliberately declared in alphabetical order so that json.Marshal produces
// deterministic, canonically-ordered JSON on the wire.
type Event struct {
	Content   string     `json:"content"`
	CreatedAt int64      `json:"created_at"`
	ID        string     `json:"id"`
	Kind      int        `json:"kind"`
	Pubkey    string     `json:"pubkey"`
	Sig       string     `json:"sig"`
	Tags      [][]string `json:"tags"`
}

// SerializeID computes the NIP-01 event id: SHA256 over the canonical
// serialization ["0", pubkey, created_at, kind, tags, content]. This is the
// layout defined by NIP-01 (an array led by the number 0), which relays
// re-serialize on their side to verify the id — so it must follow the spec
// exactly, not the wire format below.
func (e *Event) SerializeID() (string, error) {
	b, err := json.Marshal([]any{
		0, e.Pubkey, e.CreatedAt, e.Kind, e.Tags, e.Content,
	})
	if err != nil {
		return "", fmt.Errorf("nostr: serialize event id: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// MarshalWire renders the client->relay message ["EVENT", {event}] with the
// event object keys in alphabetical order (struct declaration order).
func (e *Event) MarshalWire() ([]byte, error) {
	b, err := json.Marshal([]any{"EVENT", *e})
	if err != nil {
		return nil, fmt.Errorf("nostr: marshal wire event: %w", err)
	}
	return b, nil
}

// SignEvent fills in Kind, ID and Sig, signing the 32-byte id with the
// secret key per BIP-340.
func (e *Event) SignEvent(kind int, seckey []byte) error {
	e.Kind = kind
	id, err := e.SerializeID()
	if err != nil {
		return err
	}
	idBytes, err := hex.DecodeString(id)
	if err != nil {
		return fmt.Errorf("nostr: decode id: %w", err)
	}
	aux := make([]byte, 32)
	if _, err := readRandom(aux); err != nil {
		return err
	}
	sig, err := bip340Sign(idBytes, seckey, aux)
	if err != nil {
		return fmt.Errorf("nostr: sign event: %w", err)
	}
	e.ID = id
	e.Sig = hex.EncodeToString(sig)
	return nil
}

// ParseEvent decodes a relay-published event object.
func ParseEvent(data []byte) (*Event, error) {
	var ev Event
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, fmt.Errorf("nostr: parse event: %w", err)
	}
	return &ev, nil
}

// ParsePrivateKey decodes a hex-encoded 32-byte secp256k1 secret key and
// validates it against the group order.
func ParsePrivateKey(hexKey string) ([]byte, error) {
	raw, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("nostr: decode private key: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("nostr: private key must be 32 bytes, got %d", len(raw))
	}
	v := new(big.Int).SetBytes(raw)
	if v.Sign() == 0 || v.Cmp(secpN) >= 0 {
		return nil, fmt.Errorf("nostr: private key out of range")
	}
	return raw, nil
}

// parsePubkeyX accepts either a 64-char x-only pubkey hex or a 66-char
// compressed (02/03-prefixed) form and returns the 32-byte x coordinate.
func parsePubkeyX(channelID string) ([]byte, error) {
	key := channelID
	if len(key) == 66 {
		prefix := key[:2]
		if prefix == "02" || prefix == "03" {
			key = key[2:]
		}
	}
	if len(key) != 64 {
		return nil, fmt.Errorf("nostr: invalid pubkey %q: expected 64 or 66 hex chars", channelID)
	}
	raw, err := hex.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("nostr: invalid pubkey hex %q: %w", channelID, err)
	}
	return raw, nil
}

// readRandom fills b with cryptographically secure random bytes.
func readRandom(b []byte) (int, error) {
	return randRead(b)
}
