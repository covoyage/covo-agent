package line

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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

// newTestAdapter returns an adapter whose push API base URL points at the
// given handler (injected via the unexported apiBase field).
func newTestAdapter(t *testing.T, handler http.Handler) (*Adapter, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a := &Adapter{
		channelAccessToken: "test-token",
		channelSecret:      "test-secret",
		apiBase:            srv.URL,
		httpClient:         &http.Client{Timeout: 5 * time.Second},
		logger:             testLogger(),
	}
	return a, srv
}

// signLine computes the X-Line-Signature value for a body.
func signLine(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestValidate(t *testing.T) {
	a := &Adapter{}
	err := a.Validate()
	if err == nil || !strings.Contains(err.Error(), "LINE_CHANNEL_ACCESS_TOKEN") {
		t.Fatalf("expected LINE_CHANNEL_ACCESS_TOKEN error, got %v", err)
	}
	a.channelAccessToken = "tok"
	if err := a.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestSendMessageSuccess(t *testing.T) {
	var mu sync.Mutex
	var gotPath, gotAuth string
	var payload struct {
		To       string `json:"to"`
		Messages []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"messages"`
	}

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.Write([]byte(`{}`))
	}))

	if err := a.SendMessage(context.Background(), "U123", plugin.OutgoingMessage{Text: "hello line"}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/v2/bot/message/push" {
		t.Errorf("path = %q, want /v2/bot/message/push", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("authorization = %q, want Bearer test-token", gotAuth)
	}
	if payload.To != "U123" {
		t.Errorf("to = %q, want U123", payload.To)
	}
	if len(payload.Messages) != 1 || payload.Messages[0].Type != "text" || payload.Messages[0].Text != "hello line" {
		t.Errorf("unexpected messages: %+v", payload.Messages)
	}
}

func TestSendMessageChunkingAndBatching(t *testing.T) {
	var mu sync.Mutex
	pushRequests := 0
	messagesPerRequest := make([]int, 0, 2)

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []json.RawMessage `json:"messages"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		mu.Lock()
		pushRequests++
		messagesPerRequest = append(messagesPerRequest, len(payload.Messages))
		mu.Unlock()
		w.Write([]byte(`{}`))
	}))

	// 12000 runes → 3 chunks (5000+5000+2000), one push request.
	if err := a.SendMessage(context.Background(), "G1", plugin.OutgoingMessage{Text: strings.Repeat("a", 12000)}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}
	// 26000 runes → 6 chunks → 2 push requests (5 + 1).
	if err := a.SendMessage(context.Background(), "G1", plugin.OutgoingMessage{Text: strings.Repeat("b", 26000)}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if pushRequests != 3 {
		t.Errorf("push requests = %d, want 3", pushRequests)
	}
	if len(messagesPerRequest) != 3 || messagesPerRequest[0] != 3 || messagesPerRequest[1] != 5 || messagesPerRequest[2] != 1 {
		t.Errorf("messages per request = %v, want [3 5 1]", messagesPerRequest)
	}
}

func TestSendMessageAPIError(t *testing.T) {
	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"The request body has 2 errors"}`))
	}))

	err := a.SendMessage(context.Background(), "U123", plugin.OutgoingMessage{Text: "hi"})
	if err == nil || !strings.Contains(err.Error(), "The request body has 2 errors") {
		t.Fatalf("expected error containing API message, got %v", err)
	}
}

func TestWebhookSignatureValid(t *testing.T) {
	a := &Adapter{channelSecret: "test-secret", logger: testLogger()}
	callbacks := make(chan plugin.IncomingMessage, 4)
	a.OnMessage(func(m plugin.IncomingMessage) { callbacks <- m })

	srv := httptest.NewServer(http.HandlerFunc(a.handleWebhook))
	defer srv.Close()

	body := `{"events":[
		{"type":"message","replyToken":"r1","source":{"userId":"U1","groupId":"G1"},"message":{"id":"m1","type":"text","text":"hi group"},"timestamp":1700000000000},
		{"type":"message","replyToken":"r2","source":{"userId":"U1"},"message":{"id":"m2","type":"text","text":"hi direct"},"timestamp":1700000001000},
		{"type":"follow","replyToken":"r3","source":{"userId":"U2"}}
	]}`

	req, _ := http.NewRequest("POST", srv.URL, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Line-Signature", signLine("test-secret", []byte(body)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("webhook request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Group event: channelID = groupId takes precedence.
	select {
	case m := <-callbacks:
		if m.ChannelID != "G1" || m.UserID != "U1" || m.Text != "hi group" {
			t.Errorf("unexpected message: %+v", m)
		}
		if m.Platform != PlatformName {
			t.Errorf("platform = %q, want %q", m.Platform, PlatformName)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for group message callback")
	}

	// Direct message: channelID falls back to userId.
	select {
	case m := <-callbacks:
		if m.ChannelID != "U1" || m.Text != "hi direct" {
			t.Errorf("unexpected message: %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for direct message callback")
	}

	// The "follow" event must not be forwarded.
	select {
	case m := <-callbacks:
		t.Errorf("unexpected extra callback: %+v", m)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestWebhookSignatureInvalid(t *testing.T) {
	a := &Adapter{channelSecret: "test-secret", logger: testLogger()}
	callbacks := make(chan plugin.IncomingMessage, 1)
	a.OnMessage(func(m plugin.IncomingMessage) { callbacks <- m })

	srv := httptest.NewServer(http.HandlerFunc(a.handleWebhook))
	defer srv.Close()

	body := `{"events":[{"type":"message","source":{"userId":"U1"},"message":{"type":"text","text":"should not pass"}}]}`

	req, _ := http.NewRequest("POST", srv.URL, strings.NewReader(body))
	// Signed with the wrong secret.
	req.Header.Set("X-Line-Signature", signLine("wrong-secret", []byte(body)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("webhook request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}

	select {
	case m := <-callbacks:
		t.Errorf("unexpected callback: %+v", m)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestWebhookMissingSignature(t *testing.T) {
	a := &Adapter{channelSecret: "test-secret", logger: testLogger()}
	srv := httptest.NewServer(http.HandlerFunc(a.handleWebhook))
	defer srv.Close()

	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`{"events":[]}`))
	if err != nil {
		t.Fatalf("webhook request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

type lineAPIRecorder struct {
	mu        sync.Mutex
	replyPath int
	pushPath  int
	replyBody struct {
		ReplyToken string `json:"replyToken"`
		Messages   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"messages"`
	}
	pushBody struct {
		To       string `json:"to"`
		Messages []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"messages"`
	}
	replyStatus int
}

func (rec *lineAPIRecorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		defer rec.mu.Unlock()
		switch r.URL.Path {
		case "/v2/bot/message/reply":
			rec.replyPath++
			_ = json.Unmarshal(body, &rec.replyBody)
			if rec.replyStatus != 0 {
				w.WriteHeader(rec.replyStatus)
			}
			w.Write([]byte(`{}`))
		case "/v2/bot/message/push":
			rec.pushPath++
			_ = json.Unmarshal(body, &rec.pushBody)
			w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	})
}

func TestWebhookStoresReplyToken(t *testing.T) {
	a := &Adapter{channelSecret: "test-secret", logger: testLogger()}
	callbacks := make(chan plugin.IncomingMessage, 2)
	a.OnMessage(func(m plugin.IncomingMessage) { callbacks <- m })

	srv := httptest.NewServer(http.HandlerFunc(a.handleWebhook))
	defer srv.Close()

	body := `{"events":[{"type":"message","replyToken":"rt-1","source":{"userId":"U1"},"message":{"id":"m1","type":"text","text":"hi"},"timestamp":1700000000000}]}`
	req, _ := http.NewRequest("POST", srv.URL, strings.NewReader(body))
	req.Header.Set("X-Line-Signature", signLine("test-secret", []byte(body)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("webhook request failed: %v", err)
	}
	resp.Body.Close()

	select {
	case <-callbacks:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for callback")
	}

	if got := a.takeReplyToken("U1"); got != "rt-1" {
		t.Errorf("stored reply token = %q, want rt-1", got)
	}
	// Single use: gone after take.
	if got := a.takeReplyToken("U1"); got != "" {
		t.Errorf("reply token should be deleted after use, got %q", got)
	}
}

func TestSendMessageReplyTokenPreferred(t *testing.T) {
	rec := &lineAPIRecorder{}
	a, _ := newTestAdapter(t, rec.handler())
	a.rememberReplyToken("U1", "rt-1")

	if err := a.SendMessage(context.Background(), "U1", plugin.OutgoingMessage{Text: "hello"}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.replyPath != 1 || rec.pushPath != 0 {
		t.Fatalf("reply=%d push=%d, want reply=1 push=0", rec.replyPath, rec.pushPath)
	}
	if rec.replyBody.ReplyToken != "rt-1" {
		t.Errorf("replyToken = %q, want rt-1", rec.replyBody.ReplyToken)
	}
	if len(rec.replyBody.Messages) != 1 || rec.replyBody.Messages[0].Text != "hello" {
		t.Errorf("unexpected reply messages: %+v", rec.replyBody.Messages)
	}
}

func TestSendMessageReplyFailureFallsBackToPush(t *testing.T) {
	rec := &lineAPIRecorder{replyStatus: http.StatusBadRequest}
	a, _ := newTestAdapter(t, rec.handler())
	a.rememberReplyToken("U1", "rt-1")

	if err := a.SendMessage(context.Background(), "U1", plugin.OutgoingMessage{Text: "hello"}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.replyPath != 1 || rec.pushPath != 1 {
		t.Fatalf("reply=%d push=%d, want reply=1 push=1 (fallback)", rec.replyPath, rec.pushPath)
	}
	if rec.pushBody.To != "U1" || len(rec.pushBody.Messages) != 1 || rec.pushBody.Messages[0].Text != "hello" {
		t.Errorf("unexpected push payload: %+v", rec.pushBody)
	}
}

func TestSendMessageNoTokenPushesDirectly(t *testing.T) {
	rec := &lineAPIRecorder{}
	a, _ := newTestAdapter(t, rec.handler())

	if err := a.SendMessage(context.Background(), "U1", plugin.OutgoingMessage{Text: "hello"}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.replyPath != 0 || rec.pushPath != 1 {
		t.Fatalf("reply=%d push=%d, want reply=0 push=1", rec.replyPath, rec.pushPath)
	}
	if rec.pushBody.To != "U1" {
		t.Errorf("push to = %q, want U1", rec.pushBody.To)
	}
}

func TestReplyTokenTTLExpiry(t *testing.T) {
	a := &Adapter{logger: testLogger()}
	a.rememberReplyToken("U1", "rt-old")
	// Force-expire the entry.
	a.replyMu.Lock()
	e := a.replyTokens["U1"]
	e.expiresAt = time.Now().Add(-time.Second)
	a.replyTokens["U1"] = e
	a.replyMu.Unlock()

	if got := a.takeReplyToken("U1"); got != "" {
		t.Errorf("expired token should not be returned, got %q", got)
	}
}
