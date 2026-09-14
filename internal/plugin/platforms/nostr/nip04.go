package nostr

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
)

// NIP-04 encrypted direct messages:
//
//   - ECDH: the shared secret is the x coordinate of seckey * recipientPubkey
//     (raw 32 bytes, used directly as the AES-256 key — the standard NIP-04
//     convention used across major client implementations).
//   - Payload: AES-256-CBC over the PKCS#7-padded plaintext with a random
//     16-byte IV, base64 encoded as "<ciphertext>?iv=<iv>".

const (
	nip04IVLen    = 16
	nip04KeyLen   = 32
	nip04Sep      = "?iv="
)

// ecdhSharedX computes the x coordinate of seckey * pubkey(x-only).
func ecdhSharedX(seckey []byte, pubkeyX []byte) ([]byte, error) {
	P, err := liftX(pubkeyX)
	if err != nil {
		return nil, fmt.Errorf("nostr: ecdh: %w", err)
	}
	sk := new(big.Int).SetBytes(seckey)
	if sk.Sign() == 0 || sk.Cmp(secpN) >= 0 {
		return nil, errors.New("nostr: ecdh: invalid secret key")
	}
	shared := scalarMult(P, sk)
	if shared == nil || shared.x == nil {
		return nil, errors.New("nostr: ecdh: shared point at infinity")
	}
	return pad32(shared.x), nil
}

// nip04Pad applies PKCS#7 padding to block size 16.
func nip04Pad(data []byte) []byte {
	pad := aes.BlockSize - len(data)%aes.BlockSize
	return append(data, bytes.Repeat([]byte{byte(pad)}, pad)...)
}

// nip04Unpad removes PKCS#7 padding.
func nip04Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, errors.New("nostr: invalid padded length")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(data) {
		return nil, errors.New("nostr: invalid padding")
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, errors.New("nostr: invalid padding bytes")
		}
	}
	return data[:len(data)-pad], nil
}

// NIP04Encrypt encrypts plaintext for a recipient x-only pubkey, returning
// the NIP-04 payload "<base64 ciphertext>?iv=<base64 iv>".
func NIP04Encrypt(seckey []byte, recipientX []byte, plaintext string) (string, error) {
	shared, err := ecdhSharedX(seckey, recipientX)
	if err != nil {
		return "", err
	}
	if len(shared) != nip04KeyLen {
		return "", fmt.Errorf("nostr: ecdh shared secret %d bytes, want %d", len(shared), nip04KeyLen)
	}

	block, err := aes.NewCipher(shared)
	if err != nil {
		return "", fmt.Errorf("nostr: aes cipher: %w", err)
	}

	iv := make([]byte, nip04IVLen)
	if _, err := rand.Read(iv); err != nil {
		return "", fmt.Errorf("nostr: read iv: %w", err)
	}

	padded := nip04Pad([]byte(plaintext))
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)

	return base64.StdEncoding.EncodeToString(ciphertext) +
		nip04Sep + base64.StdEncoding.EncodeToString(iv), nil
}

// NIP04Decrypt decrypts a NIP-04 payload received from senderX.
func NIP04Decrypt(seckey []byte, senderX []byte, payload string) (string, error) {
	shared, err := ecdhSharedX(seckey, senderX)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(shared)
	if err != nil {
		return "", fmt.Errorf("nostr: aes cipher: %w", err)
	}

	idx := bytes.Index([]byte(payload), []byte(nip04Sep))
	if idx < 0 {
		return "", errors.New("nostr: nip04 payload missing ?iv= separator")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(payload[:idx])
	if err != nil {
		return "", fmt.Errorf("nostr: decode ciphertext: %w", err)
	}
	iv, err := base64.StdEncoding.DecodeString(payload[idx+len(nip04Sep):])
	if err != nil {
		return "", fmt.Errorf("nostr: decode iv: %w", err)
	}
	if len(iv) != nip04IVLen {
		return "", fmt.Errorf("nostr: iv must be %d bytes, got %d", nip04IVLen, len(iv))
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return "", errors.New("nostr: ciphertext length not a multiple of block size")
	}

	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)
	unpadded, err := nip04Unpad(plain)
	if err != nil {
		return "", err
	}
	return string(unpadded), nil
}

// sharedSecretHex exposes the ECDH shared x coordinate as hex (used by tests
// to assert symmetric derivation from both sides).
func sharedSecretHex(seckey []byte, otherX []byte) string {
	shared, err := ecdhSharedX(seckey, otherX)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(shared)
}
