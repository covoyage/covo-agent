package wecomcallback

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
)

// xmlUnmarshal decodes a WeCom XML document (the envelope and the decrypted
// message body are both small flat <xml>...</xml> documents).
func xmlUnmarshal(data []byte, v any) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	return dec.Decode(v)
}

// computeSignature computes the WeCom callback signature:
// SHA1(sort(token, timestamp, nonce, encrypt) joined without separator).
func computeSignature(token, timestamp, nonce, encrypt string) string {
	parts := []string{token, timestamp, nonce, encrypt}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	return hex.EncodeToString(sum[:])
}

func verifySignature(token, timestamp, nonce, encrypt, signature string) bool {
	if signature == "" {
		return false
	}
	expected := computeSignature(token, timestamp, nonce, encrypt)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}

// aesKeyFromEncodingKey decodes the 43-char EncodingAESKey into the 32-byte
// AES key (the base64 encoding of 32 bytes is 44 chars: 43 + "=").
func aesKeyFromEncodingKey(encodingAESKey string) ([]byte, error) {
	if len(encodingAESKey) != 43 {
		return nil, fmt.Errorf("encoding AES key must be exactly 43 base64 characters, got %d", len(encodingAESKey))
	}
	key, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil {
		return nil, fmt.Errorf("decode encoding AES key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("decoded AES key must be 32 bytes, got %d", len(key))
	}
	return key, nil
}

// decryptWeCom decrypts a base64-encoded WeCom encrypted message.
// Ciphertext structure: AES-256-CBC with key/IV derived from aesKey
// (IV = first 16 bytes of key), plaintext = 16B random + 4B msg_len (big
// endian) + msg + receiveid, PKCS7 padded to 32 bytes. Returns the message
// and the trailing receiveid (callers must verify it against the configured
// corp_id, mirroring the reference adapter wecom_crypto.py's receive_id check).
func decryptWeCom(aesKey []byte, ciphertextB64 string) (msg []byte, receiveID string, err error) {
	data, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, "", fmt.Errorf("decode ciphertext: %w", err)
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, "", fmt.Errorf("new cipher: %w", err)
	}
	if len(data) < aes.BlockSize || len(data)%aes.BlockSize != 0 {
		return nil, "", fmt.Errorf("ciphertext length %d is not a multiple of the block size", len(data))
	}

	iv := aesKey[:16]
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(data, data)

	data, err = pkcs7Unpad(data, 32)
	if err != nil {
		return nil, "", err
	}
	if len(data) < 20 {
		return nil, "", fmt.Errorf("plaintext too short: %d bytes", len(data))
	}
	msgLen := binary.BigEndian.Uint32(data[16:20])
	if uint64(len(data)) < 20+uint64(msgLen) {
		return nil, "", fmt.Errorf("plaintext msg_len %d exceeds actual payload %d", msgLen, len(data)-20)
	}
	return data[20 : 20+msgLen], string(data[20+msgLen:]), nil
}

// encryptWeCom encrypts msg for WeCom (inverse of decryptWeCom) and returns
// the base64-encoded ciphertext. receiveID is appended per the WeCom spec
// (corp_id for self-built apps).
func encryptWeCom(aesKey []byte, msg []byte, receiveID string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate random prefix: %w", err)
	}

	buf := make([]byte, 0, 16+4+len(msg)+len(receiveID))
	buf = append(buf, random...)
	var lenPrefix [4]byte
	binary.BigEndian.PutUint32(lenPrefix[:], uint32(len(msg)))
	buf = append(buf, lenPrefix[:]...)
	buf = append(buf, msg...)
	buf = append(buf, receiveID...)
	buf = pkcs7Pad(buf, 32)

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return "", fmt.Errorf("new cipher: %w", err)
	}
	iv := aesKey[:16]
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(buf, buf)

	return base64.StdEncoding.EncodeToString(buf), nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	padding := make([]byte, pad)
	for i := range padding {
		padding[i] = byte(pad)
	}
	return append(data, padding...)
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("invalid padded data length %d", len(data))
	}
	pad := int(data[len(data)-1])
	if pad < 1 || pad > blockSize || pad > len(data) {
		return nil, fmt.Errorf("invalid PKCS7 padding byte %d", pad)
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, fmt.Errorf("inconsistent PKCS7 padding")
		}
	}
	return data[:len(data)-pad], nil
}
