package feishu

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/covoyage/covo-agent/internal/plugin"
)

func TestSendMessageSplitsLongUTF8Text(t *testing.T) {
	var mu sync.Mutex
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		mu.Lock()
		received = append(received, body.Content.Text)
		mu.Unlock()
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	adapter := &Adapter{webhookURL: server.URL, httpClient: server.Client()}
	text := strings.Repeat("\u98de", 2000)
	if err := adapter.SendMessage(context.Background(), "", plugin.OutgoingMessage{Text: text}); err != nil {
		t.Fatal(err)
	}
	assertChunks(t, received, text)
}

func assertChunks(t *testing.T, chunks []string, original string) {
	t.Helper()
	if len(chunks) != 2 {
		t.Fatalf("requests = %d, want 2", len(chunks))
	}
	for _, chunk := range chunks {
		if len(chunk) > 4000 || !utf8.ValidString(chunk) {
			t.Fatalf("invalid chunk: bytes=%d utf8=%v", len(chunk), utf8.ValidString(chunk))
		}
	}
	if strings.Join(chunks, "") != original {
		t.Fatal("chunks do not preserve text")
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func startWebhook(t *testing.T, adapter *Adapter, path string) string {
	t.Helper()
	addr := freePort(t)
	adapter.whServer = plugin.NewWebhookServer(addr, adapter.logger)
	adapter.whServer.RegisterRouteWithRequest(path, adapter.handleWebhook)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := adapter.whServer.Start(ctx); err != nil {
		t.Fatalf("webhook server start: %v", err)
	}
	return "http://" + addr + path
}

func TestFeishuURLVerificationChallengeEcho(t *testing.T) {
	adapter := &Adapter{logger: testLogger()}
	url := startWebhook(t, adapter, "/feishu/webhook")

	body := `{"type":"url_verification","challenge":"challenge-token-123","token":"tok"}`
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["challenge"] != "challenge-token-123" {
		t.Fatalf("challenge = %q, want challenge-token-123 (body %v)", got["challenge"], got)
	}
}

func TestFeishuTokenMismatchRejected(t *testing.T) {
	adapter := &Adapter{logger: testLogger(), verificationToken: "secret-token"}
	url := startWebhook(t, adapter, "/feishu/webhook")

	body := `{"type":"url_verification","challenge":"abc","token":"wrong"}`
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("wrong token should not be accepted")
	}
}

func TestFeishuEncryptedChallenge(t *testing.T) {
	const encryptKey = "test-encrypt-key"
	adapter := &Adapter{logger: testLogger(), encryptKey: encryptKey}
	url := startWebhook(t, adapter, "/feishu/webhook")

	plaintext := `{"type":"url_verification","challenge":"enc-challenge","token":"tok"}`
	encrypted := encryptFeishuForTest(t, plaintext, encryptKey)
	body, _ := json.Marshal(map[string]string{"encrypt": encrypted})

	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["challenge"] != "enc-challenge" {
		t.Fatalf("challenge = %q, want enc-challenge", got["challenge"])
	}
}

func TestFeishuMessageEventDelivered(t *testing.T) {
	adapter := &Adapter{logger: testLogger()}
	url := startWebhook(t, adapter, "/feishu/webhook")

	var mu sync.Mutex
	delivered := false
	adapter.whServer.SetOnMessage(func(msg plugin.IncomingMessage) {
		mu.Lock()
		delivered = true
		mu.Unlock()
	})

	body := `{"header":{"event_type":"im.message.receive_v1"},"event":{"sender":{"sender_id":{"open_id":"ou1"}},"message":{"chat_id":"oc1","message_type":"text","content":"{\"text\":\"hi\"}"}}}`
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		ok := delivered
		mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("message was not delivered")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// encryptFeishuForTest builds a Feishu encrypted envelope: AES-256-CBC with
// key = SHA256(encryptKey), IV = first 16 bytes of ciphertext, PKCS#7 padding.
func encryptFeishuForTest(t *testing.T, plaintext, encryptKey string) string {
	t.Helper()
	key := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(plaintext)
	pad := aes.BlockSize - len(data)%aes.BlockSize
	data = append(data, bytes.Repeat([]byte{byte(pad)}, pad)...)

	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	mode := cipher.NewCBCEncrypter(block, iv)
	out := make([]byte, len(data))
	mode.CryptBlocks(out, data)

	return base64.StdEncoding.EncodeToString(append(iv, out...))
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
