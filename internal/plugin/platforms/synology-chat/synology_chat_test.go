package synologychat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
)

func testAdapter(webhookURL string) *Adapter {
	a := New()
	a.webhookURL = webhookURL
	return a
}

func TestValidate(t *testing.T) {
	a := New()
	if err := a.Validate(); err == nil {
		t.Fatal("expected validation error with empty webhook URL")
	}
	a.webhookURL = "http://example.com/webhook"
	if err := a.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendMessageSuccess(t *testing.T) {
	var mu sync.Mutex
	var gotPayload string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/x-www-form-urlencoded") {
			http.Error(w, "content type", http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		vals, err := url.ParseQuery(string(body))
		if err != nil {
			http.Error(w, "form", http.StatusBadRequest)
			return
		}
		mu.Lock()
		gotPayload = vals.Get("payload")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := testAdapter(srv.URL)
	if err := a.SendMessage(context.Background(), "ignored", plugin.OutgoingMessage{Text: "hello & <world>"}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(gotPayload), &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v (raw %q)", err, gotPayload)
	}
	if payload.Text != "hello & <world>" {
		t.Errorf("text = %q", payload.Text)
	}
}

func TestSendMessageEmptyText(t *testing.T) {
	a := testAdapter("http://127.0.0.1:1")
	if err := a.SendMessage(context.Background(), "c", plugin.OutgoingMessage{Text: ""}); err != nil {
		t.Fatalf("empty text should be a no-op, got: %v", err)
	}
}

func TestSendMessageHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad token", http.StatusUnauthorized)
	}))
	defer srv.Close()

	a := testAdapter(srv.URL)
	if err := a.SendMessage(context.Background(), "c", plugin.OutgoingMessage{Text: "hi"}); err == nil {
		t.Fatal("expected error on HTTP 401")
	}
}

func TestSendMessageChunking(t *testing.T) {
	var count int
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := testAdapter(srv.URL)
	long := strings.Repeat("b", maxMessageLen*2+100)
	if err := a.SendMessage(context.Background(), "c", plugin.OutgoingMessage{Text: long}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if count != 3 {
		t.Errorf("expected 3 chunk posts, got %d", count)
	}
}

func TestHandleIncomingPost(t *testing.T) {
	a := testAdapter("http://example.com/webhook")

	var mu sync.Mutex
	var received []plugin.IncomingMessage
	a.OnMessage(func(in plugin.IncomingMessage) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, in)
	})

	srv := httptest.NewServer(http.HandlerFunc(a.handleIncoming))
	defer srv.Close()

	// Synology probes with GET first.
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET probe failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET probe status = %d", resp.StatusCode)
	}

	// Flat form fields, the real Synology Chat wire format.
	resp, err = http.PostForm(srv.URL, url.Values{
		"token":    {"tok"},
		"user_id":  {"42"},
		"username": {"bob"},
		"text":     {"hi there"},
	})
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST status = %d", resp.StatusCode)
	}

	// Flat fields with the "message" alias and no user_id fall back to username.
	resp, err = http.PostForm(srv.URL, url.Values{"message": {"no id"}, "username": {"carol"}})
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	resp.Body.Close()

	// Legacy JSON payload field is still accepted for compatibility.
	payload, _ := json.Marshal(map[string]any{"text": "legacy", "user_id": "9"})
	resp, err = http.PostForm(srv.URL, url.Values{"payload": {string(payload)}})
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("legacy payload status = %d", resp.StatusCode)
	}

	// Missing text is rejected.
	resp, err = http.PostForm(srv.URL, url.Values{"user_id": {"1"}, "username": {"dave"}})
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing text status = %d, want 400", resp.StatusCode)
	}

	// Malformed legacy payload is rejected.
	resp, err = http.PostForm(srv.URL, url.Values{"payload": {"not-json"}})
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed payload status = %d, want 400", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 {
		t.Fatalf("expected 3 callbacks, got %d", len(received))
	}
	if received[0].ChannelID != "42" || received[0].UserID != "42" || received[0].UserName != "bob" || received[0].Text != "hi there" {
		t.Errorf("unexpected message: %+v", received[0])
	}
	if received[1].ChannelID != "carol" || received[1].Text != "no id" {
		t.Errorf("unexpected fallback message: %+v", received[1])
	}
	if received[2].ChannelID != "9" || received[2].Text != "legacy" {
		t.Errorf("unexpected legacy message: %+v", received[2])
	}
}

func TestHandleIncomingQueryParams(t *testing.T) {
	a := testAdapter("http://example.com/webhook")

	var got plugin.IncomingMessage
	a.OnMessage(func(in plugin.IncomingMessage) { got = in })

	srv := httptest.NewServer(http.HandlerFunc(a.handleIncoming))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"?token=tok&user_id=7&username=eve&text=via+query", "application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("query params status = %d", resp.StatusCode)
	}
	if got.ChannelID != "7" || got.Text != "via query" || got.UserName != "eve" {
		t.Fatalf("unexpected message: %+v", got)
	}
}

func TestHandleIncomingTokenValidation(t *testing.T) {
	a := testAdapter("http://example.com/webhook")
	a.incomingToken = "s3cret"

	var calls int
	a.OnMessage(func(in plugin.IncomingMessage) { calls++ })

	srv := httptest.NewServer(http.HandlerFunc(a.handleIncoming))
	defer srv.Close()

	// Missing token → 403.
	resp, err := http.PostForm(srv.URL, url.Values{"user_id": {"1"}, "text": {"hi"}})
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("missing token status = %d, want 403", resp.StatusCode)
	}

	// Wrong token → 403.
	resp, err = http.PostForm(srv.URL, url.Values{"token": {"wrong"}, "text": {"hi"}})
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("wrong token status = %d, want 403", resp.StatusCode)
	}

	// Correct token → delivered.
	resp, err = http.PostForm(srv.URL, url.Values{"token": {"s3cret"}, "user_id": {"1"}, "text": {"hi"}})
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("correct token status = %d, want 200", resp.StatusCode)
	}

	// Legacy payload carrying the correct token → delivered.
	payload, _ := json.Marshal(map[string]any{"token": "s3cret", "text": "legacy tok", "user_id": "2"})
	resp, err = http.PostForm(srv.URL, url.Values{"payload": {string(payload)}})
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("legacy token status = %d, want 200", resp.StatusCode)
	}

	if calls != 2 {
		t.Errorf("callbacks = %d, want 2", calls)
	}
}

func TestStartStop(t *testing.T) {
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer webhook.Close()

	a := testAdapter(webhook.URL)
	a.listenAddr = "127.0.0.1:0"
	a.incomingPath = "/test-in"

	var mu sync.Mutex
	received := 0
	a.OnMessage(func(in plugin.IncomingMessage) {
		mu.Lock()
		received++
		mu.Unlock()
	})

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	addr := a.listener.Addr().String()

	payload, _ := json.Marshal(map[string]any{"text": "live", "user_id": "7"})
	resp, err := http.PostForm("http://"+addr+"/test-in", url.Values{"payload": {string(payload)}})
	if err != nil {
		t.Fatalf("POST to live server failed: %v", err)
	}
	resp.Body.Close()

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if received != 1 {
		t.Errorf("received = %d, want 1", received)
	}
	mu.Unlock()

	if err := a.Stop(); err != nil {
		t.Errorf("Stop failed: %v", err)
	}

	// Server is closed: further requests should fail.
	if _, err := http.PostForm("http://"+addr+"/test-in", url.Values{"payload": {"{}"}}); err == nil {
		t.Error("expected request to closed server to fail")
	}
}
