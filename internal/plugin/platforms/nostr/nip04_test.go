package nostr

import (
	"encoding/base64"
	"encoding/hex"
	"testing"
)

// testSeckey1 is a fixed valid secret key for deterministic tests.
const testSeckey1Hex = "B7E151628AED2A6ABF7158809CF4F3C762E7160F38B4DA56A784D9045190CFEF"

func TestNIP04RoundtripSelf(t *testing.T) {
	sec, _ := hex.DecodeString(testSeckey1Hex)
	pubX, err := xOnlyPubkey(sec)
	if err != nil {
		t.Fatal(err)
	}

	for _, plaintext := range []string{
		"hello nostr",
		"",
		"unicode: 你好，世界！🚀",
		"multi\nline\nwith\ttabs",
		"padding edge exactly 16 bytes",
	} {
		payload, err := NIP04Encrypt(sec, pubX, plaintext)
		if err != nil {
			t.Fatalf("encrypt %q: %v", plaintext, err)
		}
		got, err := NIP04Decrypt(sec, pubX, payload)
		if err != nil {
			t.Fatalf("decrypt %q: %v", plaintext, err)
		}
		if got != plaintext {
			t.Errorf("roundtrip: got %q want %q", got, plaintext)
		}
	}
}

func TestNIP04TwoPartyRoundtrip(t *testing.T) {
	skA, err := GenerateSeckey()
	if err != nil {
		t.Fatal(err)
	}
	skB, err := GenerateSeckey()
	if err != nil {
		t.Fatal(err)
	}
	pkA, _ := xOnlyPubkey(skA)
	pkB, _ := xOnlyPubkey(skB)

	// Both sides derive the same ECDH shared secret.
	if sharedSecretHex(skA, pkB) != sharedSecretHex(skB, pkA) {
		t.Fatal("ECDH shared secrets differ between parties")
	}

	payload, err := NIP04Encrypt(skA, pkB, "secret message")
	if err != nil {
		t.Fatal(err)
	}
	got, err := NIP04Decrypt(skB, pkA, payload)
	if err != nil {
		t.Fatal(err)
	}
	if got != "secret message" {
		t.Fatalf("got %q", got)
	}
}

func TestNIP04PayloadFormat(t *testing.T) {
	sec, _ := hex.DecodeString(testSeckey1Hex)
	pubX, _ := xOnlyPubkey(sec)
	payload, err := NIP04Encrypt(sec, pubX, "format check")
	if err != nil {
		t.Fatal(err)
	}
	// Must be "<base64>?iv=<base64>" with a 16-byte IV (22 base64 chars).
	idx := 0
	for i := 0; i < len(payload); i++ {
		if payload[i] == '?' {
			idx = i
			break
		}
	}
	if idx == 0 || payload[idx:] != payload[idx:] {
		t.Fatalf("payload missing ?iv= separator: %q", payload)
	}
	ivPart := payload[idx+len(nip04Sep):]
	ivBytes, err := base64.StdEncoding.DecodeString(ivPart)
	if err != nil {
		t.Fatalf("decode iv: %v", err)
	}
	if len(ivBytes) != nip04IVLen {
		t.Errorf("iv length = %d bytes, want %d", len(ivBytes), nip04IVLen)
	}
}

func TestNIP04DecryptRejectsGarbage(t *testing.T) {
	sec, _ := hex.DecodeString(testSeckey1Hex)
	pubX, _ := xOnlyPubkey(sec)

	if _, err := NIP04Decrypt(sec, pubX, "notavalidpayload"); err == nil {
		t.Error("missing ?iv= should fail")
	}
	if _, err := NIP04Decrypt(sec, pubX, "AAAA?iv=AAAA"); err == nil {
		t.Error("bad base64/short iv should fail")
	}
}
