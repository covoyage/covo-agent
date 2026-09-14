package zalo

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestAdapter wires an adapter to an httptest-backed API endpoint.
func newTestAdapter(t *testing.T, handler http.Handler) (*Adapter, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a := &Adapter{
		accessToken: "bot-token",
		listenAddr:  ":0",
		apiBase:     srv.URL,
		httpClient:  &http.Client{Timeout: 5 * time.Second},
		logger:      testLogger(),
	}
	return a, srv
}

func TestValidate(t *testing.T) {
	a := &Adapter{}
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "ZALO_BOT_TOKEN") {
		t.Fatalf("expected ZALO_BOT_TOKEN error, got %v", err)
	}
	a.accessToken = "tok"
	if err := a.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

// TestNewTokenEnvFallback verifies the token env mapping: ZALO_BOT_TOKEN
// wins; ZALO_OA_ACCESS_TOKEN remains a compatibility fallback.
func TestNewTokenEnvFallback(t *testing.T) {
	t.Setenv("ZALO_BOT_TOKEN", "bot-tok")
	t.Setenv("ZALO_OA_ACCESS_TOKEN", "legacy-tok")
	a := New()
	if a.accessToken != "bot-tok" {
		t.Errorf("accessToken = %q, want bot-tok", a.accessToken)
	}

	// Setting "" behaves like unset for the adapter's env resolution.
	t.Setenv("ZALO_BOT_TOKEN", "")
	a = New()
	if a.accessToken != "legacy-tok" {
		t.Errorf("legacy fallback accessToken = %q, want legacy-tok", a.accessToken)
	}
}

func TestSendMessageSuccess(t *testing.T) {
	var gotPath string
	var payload map[string]any
	var mu sync.Mutex

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&payload)
		w.Write([]byte(`{"ok":true,"result":{"message_id":"m1"}}`))
	}))

	if err := a.Send(context.Background(), "chat-123", "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/botbot-token/sendMessage" {
		t.Fatalf("unexpected path %q", gotPath)
	}
	if payload["chat_id"] != "chat-123" {
		t.Fatalf("unexpected chat_id: %v", payload)
	}
	if payload["text"] != "hello" {
		t.Fatalf("unexpected text: %v", payload)
	}
}

func TestSendMessageAPIErrorPropagated(t *testing.T) {
	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":false,"error_code":-110,"description":"Access token invalid"}`))
	}))

	err := a.Send(context.Background(), "chat-1", "hi")
	if err == nil || !strings.Contains(err.Error(), "-110") {
		t.Fatalf("expected error -110, got %v", err)
	}
}

func TestSendMessageEmptyChannel(t *testing.T) {
	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API call")
	}))
	if err := a.Send(context.Background(), "", "hi"); err == nil {
		t.Fatalf("expected error for empty channel ID")
	}
}

func TestWebhook(t *testing.T) {
	var mu sync.Mutex
	var received []plugin.IncomingMessage

	a, _ := newTestAdapter(t, nil)
	a.onMessage = func(msg plugin.IncomingMessage) {
		mu.Lock()
		received = append(received, msg)
		mu.Unlock()
	}

	// Expose the webhook handler on an ephemeral server.
	mux := http.NewServeMux()
	mux.HandleFunc("/zalo/webhook", a.serveWebhook)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	event := `{"event_name":"message.text.received","update_id":42,"message":{"chat":{"id":"chat-9"},"text":"xin chao","from":{"id":"u-1","name":"Nam"}}}`
	resp, err := http.Post(srv.URL+"/zalo/webhook", "application/json", strings.NewReader(event))
	if err != nil {
		t.Fatalf("POST webhook: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != `{"status":1}` {
		t.Fatalf("expected {\"status\":1}, got %q", body)
	}

	// Non-text events are ignored but still acknowledged.
	other := `{"event_name":"message.image.received","update_id":43,"message":{"chat":{"id":"chat-9"},"from":{"id":"u-1"}}}`
	resp2, err := http.Post(srv.URL+"/zalo/webhook", "application/json", strings.NewReader(other))
	if err != nil {
		t.Fatalf("POST other event: %v", err)
	}
	resp2.Body.Close()

	// Malformed JSON returns 400.
	resp3, err := http.Post(srv.URL+"/zalo/webhook", "application/json", strings.NewReader("{invalid"))
	if err != nil {
		t.Fatalf("POST invalid: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", resp3.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 incoming message, got %d", len(received))
	}
	m := received[0]
	if m.ChannelID != "chat-9" || m.UserID != "u-1" || m.UserName != "Nam" || m.Text != "xin chao" {
		t.Fatalf("unexpected incoming message: %+v", m)
	}
}

// TestWebhookSecret verifies x-bot-api-secret-token enforcement: enforced
// only when ZALO_WEBHOOK_SECRET is configured.
func TestWebhookSecret(t *testing.T) {
	event := `{"event_name":"message.text.received","update_id":1,"message":{"chat":{"id":"c1"},"text":"hi","from":{"id":"u1"}}}`

	// No secret configured: requests without the header are accepted.
	a, _ := newTestAdapter(t, nil)
	a.webhookSecret = ""

	mux := http.NewServeMux()
	mux.HandleFunc("/zalo/webhook", a.serveWebhook)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/zalo/webhook", "application/json", strings.NewReader(event))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no secret configured: status = %d, want 200", resp.StatusCode)
	}

	// Secret configured: correct header passes, wrong/missing header is 403.
	a.webhookSecret = "s3cret"
	got := make(chan struct{}, 1)
	a.onMessage = func(msg plugin.IncomingMessage) { got <- struct{}{} }

	resp, err = http.Post(srv.URL+"/zalo/webhook", "application/json", strings.NewReader(event))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing header: status = %d, want 403", resp.StatusCode)
	}

	req, _ := http.NewRequest("POST", srv.URL+"/zalo/webhook", strings.NewReader(event))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Bot-Api-Secret-Token", "wrong")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong header: status = %d, want 403", resp.StatusCode)
	}

	req, _ = http.NewRequest("POST", srv.URL+"/zalo/webhook", strings.NewReader(event))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Bot-Api-Secret-Token", "s3cret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("correct header: status = %d, want 200", resp.StatusCode)
	}
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("callback not invoked for correctly signed webhook")
	}
}
