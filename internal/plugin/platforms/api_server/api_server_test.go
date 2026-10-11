package apiserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
	"log/slog"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestAdapter(t *testing.T, authToken string) (*Adapter, string) {
	t.Helper()
	dir := t.TempDir()
	a := &Adapter{
		listenAddr:   "127.0.0.1:0",
		authToken:    authToken,
		httpClient:   &http.Client{Timeout: 5 * time.Second},
		logger:       testLogger(),
		channelsPath: filepath.Join(dir, "api_server_channels.json"),
		channels:     make(map[string]string),
		idem:         newIdempotencyCache(),
		replyWaiters: make(map[string]chan string),
	}
	return a, dir
}

func postJSON(t *testing.T, url string, headers map[string]string, body any) (*http.Response, []byte) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp, respBody
}

func TestInboundAndOutboundRoundtrip(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]any
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		mu.Lock()
		received = append(received, m)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	a, _ := newTestAdapter(t, "")

	callbacks := make(chan plugin.IncomingMessage, 8)
	a.OnMessage(func(msg plugin.IncomingMessage) { callbacks <- msg })

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	addr := "http://" + a.listener.Addr().String()

	// Health endpoint.
	resp, err := http.Get(addr + "/api/health")
	if err != nil {
		t.Fatalf("GET health: %v", err)
	}
	var health map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&health)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || health["status"] != "ok" {
		t.Fatalf("health: status=%d body=%v", resp.StatusCode, health)
	}

	// Inbound message with reply_url registers the channel.
	resp, body := postJSON(t, addr+"/api/message", nil, map[string]string{
		"channel_id": "c1",
		"user_id":    "u1",
		"user_name":  "alice",
		"text":       "hello",
		"reply_url":  target.URL + "/reply",
	})
	var accepted map[string]string
	_ = json.Unmarshal(body, &accepted)
	if resp.StatusCode != http.StatusOK || accepted["status"] != "accepted" {
		t.Fatalf("message: status=%d body=%s", resp.StatusCode, body)
	}

	select {
	case msg := <-callbacks:
		if msg.Platform != "api_server" || msg.ChannelID != "c1" || msg.UserID != "u1" || msg.Text != "hello" {
			t.Fatalf("unexpected callback: %+v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callback not invoked")
	}

	// Outbound message goes to the registered reply_url.
	if err := a.SendMessage(ctx, "c1", plugin.OutgoingMessage{Text: "reply text"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 || received[0]["text"] != "reply text" {
		t.Fatalf("reply URL got %v", received)
	}

	// Unknown channel without URL → explanatory error.
	if err := a.SendMessage(ctx, "unknown", plugin.OutgoingMessage{Text: "x"}); err == nil {
		t.Fatal("expected error for unknown channel")
	} else if !bytes.Contains([]byte(err.Error()), []byte("reply_url")) {
		t.Fatalf("error should mention reply_url: %v", err)
	}
}

func TestChannelPersistence(t *testing.T) {
	a, dir := newTestAdapter(t, "")
	a.channels["persisted"] = "http://example.com/hook"
	a.saveChannels()

	// A fresh adapter in the same directory loads the mapping.
	b := &Adapter{
		listenAddr:   "127.0.0.1:0",
		httpClient:   &http.Client{Timeout: 5 * time.Second},
		logger:       testLogger(),
		channelsPath: filepath.Join(dir, "api_server_channels.json"),
		channels:     make(map[string]string),
	}
	if err := b.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer b.Stop()
	if got := b.lookupChannel("persisted"); got != "http://example.com/hook" {
		t.Fatalf("persisted channel not loaded: %q", got)
	}
}

func TestAuthToken(t *testing.T) {
	a, _ := newTestAdapter(t, "s3cret")
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	addr := "http://" + a.listener.Addr().String()

	// Missing token → 401.
	resp, _ := postJSON(t, addr+"/api/message", nil, map[string]string{"text": "hi"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}

	// Wrong token → 401.
	resp, _ = postJSON(t, addr+"/api/message", map[string]string{"Authorization": "Bearer nope"}, map[string]string{"text": "hi"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", resp.StatusCode)
	}

	// Correct token → 200.
	resp, body := postJSON(t, addr+"/api/message", map[string]string{"Authorization": "Bearer s3cret"}, map[string]string{"text": "hi"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d: %s", resp.StatusCode, body)
	}
}

func TestInvalidPayloads(t *testing.T) {
	a, _ := newTestAdapter(t, "")
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	addr := "http://" + a.listener.Addr().String()

	resp, _ := postJSON(t, addr+"/api/message", nil, map[string]string{"channel_id": "c"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing text, got %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, addr+"/api/message", nil)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET message: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET, got %d", resp2.StatusCode)
	}
}

func TestIdempotencyKeyDedup(t *testing.T) {
	a, _ := newTestAdapter(t, "")
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	addr := "http://" + a.listener.Addr().String()

	callbacks := make(chan plugin.IncomingMessage, 8)
	a.OnMessage(func(msg plugin.IncomingMessage) { callbacks <- msg })

	headers := map[string]string{"Idempotency-Key": "req-42"}
	payload := map[string]string{"channel_id": "c1", "text": "once"}

	resp1, body1 := postJSON(t, addr+"/api/message", headers, payload)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first POST: status=%d body=%s", resp1.StatusCode, body1)
	}
	select {
	case <-callbacks:
	case <-time.After(2 * time.Second):
		t.Fatal("first delivery missing")
	}

	// Same key again: no re-dispatch, identical response replayed.
	resp2, body2 := postJSON(t, addr+"/api/message", headers, payload)
	if resp2.StatusCode != http.StatusOK || string(body2) != string(body1) {
		t.Fatalf("replay: status=%d body=%q want %q", resp2.StatusCode, body2, body1)
	}
	select {
	case msg := <-callbacks:
		t.Fatalf("duplicate dispatch for same Idempotency-Key: %+v", msg)
	case <-time.After(300 * time.Millisecond):
	}

	// Different key: dispatched normally.
	resp3, _ := postJSON(t, addr+"/api/message", map[string]string{"Idempotency-Key": "req-43"}, payload)
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("different key: status=%d", resp3.StatusCode)
	}
	select {
	case <-callbacks:
	case <-time.After(2 * time.Second):
		t.Fatal("second delivery missing for different key")
	}
}

func TestChatCompletions(t *testing.T) {
	a, _ := newTestAdapter(t, "")
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	addr := "http://" + a.listener.Addr().String()

	// The "agent pipeline" replies via SendMessage to the session channel;
	// the chat endpoint must capture that reply synchronously.
	var gotChannel atomic.Value
	a.OnMessage(func(msg plugin.IncomingMessage) {
		gotChannel.Store(msg.ChannelID)
		_ = a.SendMessage(context.Background(), msg.ChannelID, plugin.OutgoingMessage{Text: "agent answer", RequestID: msg.RequestID})
	})

	resp, body := postJSON(t, addr+"/v1/chat/completions", map[string]string{"X-Session-Id": "sess-1"}, map[string]any{
		"model": "whatever",
		"messages": []map[string]string{
			{"role": "system", "content": "you are helpful"},
			{"role": "user", "content": "hi agent"},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat: status=%d body=%s", resp.StatusCode, body)
	}

	var out struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Index   int `json:"index"`
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode chat response: %v", err)
	}
	if out.Object != "chat.completion" || out.ID == "" || out.Created == 0 {
		t.Fatalf("bad chat.completion envelope: %+v", out)
	}
	if len(out.Choices) != 1 || out.Choices[0].Index != 0 || out.Choices[0].FinishReason != "stop" {
		t.Fatalf("bad choices: %+v", out.Choices)
	}
	if out.Choices[0].Message.Role != "assistant" || out.Choices[0].Message.Content != "agent answer" {
		t.Fatalf("bad message: %+v", out.Choices[0].Message)
	}
	if gotChannel.Load() != "sess-1" {
		t.Fatalf("channel id = %v, want sess-1", gotChannel.Load())
	}

	// Default model when request omits it, default channel without header.
	resp2, body2 := postJSON(t, addr+"/v1/chat/completions", nil, map[string]any{
		"messages": []map[string]string{{"role": "user", "content": "again"}},
	})
	var out2 map[string]any
	if err := json.Unmarshal(body2, &out2); err != nil {
		t.Fatalf("decode default-model response: %v", err)
	}
	_ = resp2
	if out2["model"] != "covo-agent" {
		t.Fatalf("default model = %v", out2["model"])
	}
	if ch := gotChannel.Load().(string); !strings.HasPrefix(ch, "openai-") {
		t.Fatalf("default channel = %v, want isolated anonymous session", ch)
	}

	// Auth: chat endpoint enforces the same Bearer token as /api/message.
	b, _ := newTestAdapter(t, "s3cret")
	b.OnMessage(func(msg plugin.IncomingMessage) {
		_ = b.SendMessage(context.Background(), msg.ChannelID, plugin.OutgoingMessage{Text: "answer", RequestID: msg.RequestID})
	})
	if err := b.Start(context.Background()); err != nil {
		t.Fatalf("Start b: %v", err)
	}
	defer b.Stop()
	baddr := "http://" + b.listener.Addr().String()
	resp3, _ := postJSON(t, baddr+"/v1/chat/completions", nil, map[string]any{
		"messages": []map[string]string{{"role": "user", "content": "x"}},
	})
	if resp3.StatusCode != http.StatusUnauthorized {
		t.Fatalf("chat without token: expected 401, got %d", resp3.StatusCode)
	}
	resp4, _ := postJSON(t, baddr+"/v1/chat/completions", map[string]string{"Authorization": "Bearer s3cret"}, map[string]any{
		"messages": []map[string]string{{"role": "user", "content": "x"}},
	})
	if resp4.StatusCode != http.StatusOK {
		t.Fatalf("chat with token: expected 200, got %d", resp4.StatusCode)
	}
}

func TestChatCompletionsIdempotency(t *testing.T) {
	a, _ := newTestAdapter(t, "")
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	addr := "http://" + a.listener.Addr().String()

	count := make(chan struct{}, 8)
	a.OnMessage(func(msg plugin.IncomingMessage) {
		count <- struct{}{}
		_ = a.SendMessage(context.Background(), msg.ChannelID, plugin.OutgoingMessage{Text: "answer", RequestID: msg.RequestID})
	})

	headers := map[string]string{"Idempotency-Key": "chat-1"}
	payload := map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}}
	resp1, body1 := postJSON(t, addr+"/v1/chat/completions", headers, payload)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first chat: %d", resp1.StatusCode)
	}
	<-count

	resp2, body2 := postJSON(t, addr+"/v1/chat/completions", headers, payload)
	if resp2.StatusCode != http.StatusOK || string(body2) != string(body1) {
		t.Fatalf("chat replay mismatch: %q vs %q", body2, body1)
	}
	select {
	case <-count:
		t.Fatal("duplicate dispatch on chat endpoint for same Idempotency-Key")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestChatCompletionTimeoutAndLateReplyIsolation(t *testing.T) {
	a, _ := newTestAdapter(t, "")
	a.replyTimeout = 20 * time.Millisecond
	requests := make(chan plugin.IncomingMessage, 2)
	a.OnMessage(func(msg plugin.IncomingMessage) { requests <- msg })
	request := func() *http.Request {
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"test"}]}`))
		req.Header.Set("X-Session-Id", "same-session")
		return req
	}
	first := httptest.NewRecorder()
	a.serveChatCompletions(first, request())
	old := <-requests
	if first.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout reported success: %d %s", first.Code, first.Body.String())
	}
	a.replyTimeout = time.Second
	second := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); a.serveChatCompletions(second, request()) }()
	current := <-requests
	conflict := httptest.NewRecorder()
	a.serveChatCompletions(conflict, request())
	if conflict.Code != http.StatusConflict {
		t.Fatalf("concurrent session accepted: %d", conflict.Code)
	}
	if err := a.SendMessage(context.Background(), old.ChannelID, plugin.OutgoingMessage{Text: "old answer", RequestID: old.RequestID}); err == nil {
		t.Fatal("late response accepted")
	}
	if err := a.SendMessage(context.Background(), current.ChannelID, plugin.OutgoingMessage{Text: "current answer", RequestID: current.RequestID}); err != nil {
		t.Fatal(err)
	}
	<-done
	if second.Code != 200 || !strings.Contains(second.Body.String(), "current answer") || strings.Contains(second.Body.String(), "old answer") {
		t.Fatalf("response mismatch: %s", second.Body.String())
	}
}
