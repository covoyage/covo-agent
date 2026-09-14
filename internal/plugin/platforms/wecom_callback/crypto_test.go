package wecomcallback

import (
	"encoding/base64"
	"strings"
	"testing"
)

// testAESKey returns a deterministic 32-byte key and its 43-char
// EncodingAESKey representation.
func testAESKey(t *testing.T) ([]byte, string) {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 7)
	}
	encKey := base64.StdEncoding.EncodeToString(key)[:43]
	return key, encKey
}

func TestAESKeyFromEncodingKey(t *testing.T) {
	key, encKey := testAESKey(t)

	got, err := aesKeyFromEncodingKey(encKey)
	if err != nil {
		t.Fatalf("aesKeyFromEncodingKey: %v", err)
	}
	if string(got) != string(key) {
		t.Fatalf("decoded key mismatch: got %x want %x", got, key)
	}

	if _, err := aesKeyFromEncodingKey("tooshort"); err == nil {
		t.Fatal("expected error for short key")
	}
	badLen := strings.Repeat("A", 42)
	if _, err := aesKeyFromEncodingKey(badLen); err == nil {
		t.Fatal("expected error for 42-char key")
	}
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key, _ := testAESKey(t)
	msgs := []string{
		"hello",
		"你好，企业微信",
		"<xml><Content>multi\nline & special chars</Content></xml>",
		strings.Repeat("x", 300),
	}
	for _, msg := range msgs {
		ciphertext, err := encryptWeCom(key, []byte(msg), "corp123")
		if err != nil {
			t.Fatalf("encryptWeCom(%q): %v", msg, err)
		}
		plain, receiveID, err := decryptWeCom(key, ciphertext)
		if err != nil {
			t.Fatalf("decryptWeCom(%q): %v", msg, err)
		}
		if string(plain) != msg {
			t.Fatalf("roundtrip mismatch: got %q want %q", plain, msg)
		}
		if receiveID != "corp123" {
			t.Fatalf("receiveID = %q, want corp123", receiveID)
		}
	}
}

func TestDecryptReceiveID(t *testing.T) {
	key, _ := testAESKey(t)
	ciphertext, err := encryptWeCom(key, []byte("<xml/>"), "another-corp")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	_, receiveID, err := decryptWeCom(key, ciphertext)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if receiveID != "another-corp" {
		t.Fatalf("receiveID = %q, want another-corp", receiveID)
	}
}

func TestDecryptRejectsGarbage(t *testing.T) {
	key, _ := testAESKey(t)
	if _, _, err := decryptWeCom(key, "not-base64!!"); err == nil {
		t.Fatal("expected error for non-base64 ciphertext")
	}
	if _, _, err := decryptWeCom(key, base64.StdEncoding.EncodeToString([]byte("too short"))); err == nil {
		t.Fatal("expected error for short ciphertext")
	}
}

func TestSignatureVerify(t *testing.T) {
	sig := computeSignature("token", "1234", "nonce", "encrypt-blob")
	if sig == "" {
		t.Fatal("empty signature")
	}
	if !verifySignature("token", "1234", "nonce", "encrypt-blob", sig) {
		t.Fatal("expected valid signature to verify")
	}
	// Tampered fields must fail.
	if verifySignature("token", "9999", "nonce", "encrypt-blob", sig) {
		t.Fatal("expected tampered timestamp to fail")
	}
	if verifySignature("token", "1234", "nonce", "other-blob", sig) {
		t.Fatal("expected tampered encrypt to fail")
	}
	if verifySignature("wrongtoken", "1234", "nonce", "encrypt-blob", sig) {
		t.Fatal("expected wrong token to fail")
	}
	if verifySignature("token", "1234", "nonce", "encrypt-blob", "") {
		t.Fatal("expected empty signature to fail")
	}
}

func TestPKCS7(t *testing.T) {
	data := []byte("hello")
	padded := pkcs7Pad(data, 32)
	if len(padded) != 32 {
		t.Fatalf("padded length %d, want 32", len(padded))
	}
	unpadded, err := pkcs7Unpad(padded, 32)
	if err != nil {
		t.Fatalf("pkcs7Unpad: %v", err)
	}
	if string(unpadded) != "hello" {
		t.Fatalf("unpad mismatch: %q", unpadded)
	}

	if _, err := pkcs7Unpad([]byte(strings.Repeat("A", 33)), 32); err == nil {
		t.Fatal("expected error for bad padded length")
	}
	bad := make([]byte, 32)
	bad[31] = 5
	bad[30] = 6 // inconsistent
	if _, err := pkcs7Unpad(bad, 32); err == nil {
		t.Fatal("expected error for inconsistent padding")
	}
}
