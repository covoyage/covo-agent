package whatsapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/covoyage/covo-agent/internal/plugin"
)

type recordingTransport struct {
	mu       sync.Mutex
	received []string
}

func (transport *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var body struct {
		Text struct {
			Body string `json:"body"`
		} `json:"text"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		return nil, err
	}
	transport.mu.Lock()
	transport.received = append(transport.received, body.Text.Body)
	transport.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
		Header:     make(http.Header),
		Request:    request,
	}, nil
}

func TestSendMessageSplitsLongUTF8Text(t *testing.T) {
	transport := &recordingTransport{}
	adapter := &Adapter{
		phoneNumberID: "phone",
		accessToken:   "token",
		httpClient:    &http.Client{Transport: transport},
	}
	text := strings.Repeat("\u804a", 2000)
	if err := adapter.SendMessage(context.Background(), "recipient", plugin.OutgoingMessage{Text: text}); err != nil {
		t.Fatal(err)
	}
	if len(transport.received) != 2 {
		t.Fatalf("requests = %d, want 2", len(transport.received))
	}
	for _, chunk := range transport.received {
		if len(chunk) > 4000 || !utf8.ValidString(chunk) {
			t.Fatalf("invalid chunk: bytes=%d utf8=%v", len(chunk), utf8.ValidString(chunk))
		}
	}
	if strings.Join(transport.received, "") != text {
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

func startWebhook(t *testing.T, adapter *Adapter) string {
	t.Helper()
	addr := freePort(t)
	adapter.whServer = plugin.NewWebhookServer(addr, adapter.logger)
	adapter.whServer.RegisterRouteWithRequest("/whatsapp/webhook", adapter.handleWebhook)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := adapter.whServer.Start(ctx); err != nil {
		t.Fatalf("webhook server start: %v", err)
	}
	return "http://" + addr + "/whatsapp/webhook"
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestWebhookGetVerificationEchoesChallenge(t *testing.T) {
	adapter := &Adapter{verifyToken: "my-verify-token", logger: testLogger()}
	url := startWebhook(t, adapter)

	resp, err := http.Get(url + "?hub.mode=subscribe&hub.verify_token=my-verify-token&hub.challenge=CHALLENGE_123")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) != "CHALLENGE_123" {
		t.Fatalf("body = %q, want CHALLENGE_123", string(body))
	}
}

func TestWebhookGetVerificationWrongToken(t *testing.T) {
	adapter := &Adapter{verifyToken: "my-verify-token", logger: testLogger()}
	url := startWebhook(t, adapter)

	resp, err := http.Get(url + "?hub.mode=subscribe&hub.verify_token=WRONG&hub.challenge=CHALLENGE_123")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestWebhookPostSignaturePositiveAndNegative(t *testing.T) {
	const secret = "app-secret"
	const payload = `{"object":"whatsapp_business_account","entry":[{"changes":[{"value":{"messages":[{"from":"15551234567","type":"text","text":{"body":"hello"}}]}}]}]}`

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	adapter := &Adapter{appSecret: secret, logger: testLogger()}
	url := startWebhook(t, adapter)

	var mu sync.Mutex
	delivered := ""
	adapter.whServer.SetOnMessage(func(msg plugin.IncomingMessage) {
		mu.Lock()
		delivered = msg.Text
		mu.Unlock()
	})

	// Positive case: valid signature -> delivered.
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", validSig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid signature rejected: status %d", resp.StatusCode)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := delivered
		mu.Unlock()
		if got == "hello" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("message was not delivered despite valid signature")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Negative case: bad signature -> 403, not delivered.
	req2, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(payload))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("bad signature accepted: status %d", resp2.StatusCode)
	}

	// Negative case: missing signature -> 403.
	req3, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(payload))
	req3.Header.Set("Content-Type", "application/json")
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusForbidden {
		t.Fatalf("missing signature accepted: status %d", resp3.StatusCode)
	}
}

func TestWebhookPostNoSecretConfiguredAccepts(t *testing.T) {
	payload := `{"object":"whatsapp_business_account","entry":[]}`

	adapter := &Adapter{logger: testLogger()}
	url := startWebhook(t, adapter)

	resp, err := http.Post(url, "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 when no secret configured", resp.StatusCode)
	}
}
