package zalouser

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
		accessToken: "user-token",
		listenAddr:  ":0",
		apiBase:     srv.URL,
		httpClient:  &http.Client{Timeout: 5 * time.Second},
		logger:      testLogger(),
	}
	return a, srv
}

func TestValidate(t *testing.T) {
	a := &Adapter{}
	err := a.Validate()
	if err == nil || !strings.Contains(err.Error(), "ZALOUSER_ACCESS_TOKEN") {
		t.Fatalf("expected ZALOUSER_ACCESS_TOKEN error, got %v", err)
	}
	a.accessToken = "tok"
	if err := a.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestSendMessageSuccess(t *testing.T) {
	var gotPath, gotQuery string
	var payload map[string]any
	var mu sync.Mutex

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		json.NewDecoder(r.Body).Decode(&payload)
		w.Write([]byte(`{"error":0,"message":"Success"}`))
	}))

	if err := a.Send(context.Background(), "to-id-1", "xin chao"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/v2.0/za/user/sendmsg/" {
		t.Fatalf("unexpected path %q", gotPath)
	}
	if gotQuery != "access_token=user-token" {
		t.Fatalf("unexpected query %q", gotQuery)
	}
	if payload["to_id"] != "to-id-1" || payload["type"] != "2" || payload["message"] != "xin chao" {
		t.Fatalf("unexpected payload: %v", payload)
	}
}

func TestSendMessageAPIErrorPropagated(t *testing.T) {
	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":-40,"message":"Invalid token"}`))
	}))

	err := a.Send(context.Background(), "to-id", "hi")
	if err == nil || !strings.Contains(err.Error(), "-40") {
		t.Fatalf("expected error -40, got %v", err)
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

	mux := http.NewServeMux()
	mux.HandleFunc("/zalouser/webhook", a.serveWebhook)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	event := `{"event_name":"user_send_text","sender":{"id":"u-9","name":"Lan"},"message":{"text":"hello there"}}`
	resp, err := http.Post(srv.URL+"/zalouser/webhook", "application/json", strings.NewReader(event))
	if err != nil {
		t.Fatalf("POST webhook: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != `{"status":1}` {
		t.Fatalf("expected {\"status\":1}, got %q", body)
	}

	// Non-text events are ignored but still acknowledged.
	resp2, err := http.Post(srv.URL+"/zalouser/webhook", "application/json", strings.NewReader(`{"event_name":"user_typing","sender":{"id":"u-1"}}`))
	if err != nil {
		t.Fatalf("POST other event: %v", err)
	}
	resp2.Body.Close()

	// Malformed JSON returns 400.
	resp3, err := http.Post(srv.URL+"/zalouser/webhook", "application/json", strings.NewReader("not-json"))
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
	if received[0].ChannelID != "u-9" || received[0].UserID != "u-9" || received[0].Text != "hello there" {
		t.Fatalf("unexpected incoming message: %+v", received[0])
	}
}

// TestWebhookSecret verifies optional x-bot-api-secret-token enforcement
// (positive and negative cases).
func TestWebhookSecret(t *testing.T) {
	event := `{"event_name":"user_send_text","sender":{"id":"u-1","name":"Nam"},"message":{"text":"hi"}}`

	a, _ := newTestAdapter(t, nil)
	a.webhookSecret = ""

	mux := http.NewServeMux()
	mux.HandleFunc("/zalouser/webhook", a.serveWebhook)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// No secret configured: requests without the header are accepted.
	resp, err := http.Post(srv.URL+"/zalouser/webhook", "application/json", strings.NewReader(event))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no secret configured: status = %d, want 200", resp.StatusCode)
	}

	// Secret configured: wrong/missing header is rejected with 403.
	a.webhookSecret = "s3cret"
	got := make(chan plugin.IncomingMessage, 1)
	a.onMessage = func(msg plugin.IncomingMessage) { got <- msg }

	resp, err = http.Post(srv.URL+"/zalouser/webhook", "application/json", strings.NewReader(event))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing header: status = %d, want 403", resp.StatusCode)
	}

	req, _ := http.NewRequest("POST", srv.URL+"/zalouser/webhook", strings.NewReader(event))
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

	// Correct header passes and reaches the callback.
	req, _ = http.NewRequest("POST", srv.URL+"/zalouser/webhook", strings.NewReader(event))
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
	case m := <-got:
		if m.Text != "hi" {
			t.Errorf("text = %q", m.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("callback not invoked for correctly signed webhook")
	}
}
