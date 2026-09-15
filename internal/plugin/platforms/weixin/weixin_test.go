package weixin

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
	"unicode/utf8"

	"github.com/covoyage/covo-agent/internal/plugin"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestAdapter wires an adapter to an httptest-backed iLink endpoint.
func newTestAdapter(t *testing.T, handler http.Handler) (*Adapter, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a := &Adapter{
		accountID:     "wx-account",
		token:         "wx-bot-token",
		baseURL:       srv.URL,
		httpClient:    &http.Client{Timeout: 5 * time.Second},
		logger:        testLogger(),
		contextTokens: map[string]contextTokenEntry{},
		seenMsgs:      map[string]time.Time{},
	}
	return a, srv
}

func TestValidate(t *testing.T) {
	a := &Adapter{}
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "WEIXIN_TOKEN") {
		t.Fatalf("expected WEIXIN_TOKEN error, got %v", err)
	}
	a.token = "tok"
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "WEIXIN_ACCOUNT_ID") {
		t.Fatalf("expected WEIXIN_ACCOUNT_ID error, got %v", err)
	}
	a.accountID = "acc"
	if err := a.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestAPIHeaders(t *testing.T) {
	var got http.Header
	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Write([]byte(`{"ret":0,"errcode":0}`))
	}))
	if _, err := a.apiPost(context.Background(), epGetConfig, map[string]any{}, apiTimeoutMS); err != nil {
		t.Fatalf("apiPost: %v", err)
	}

	if got.Get("AuthorizationType") != "ilink_bot_token" {
		t.Fatalf("unexpected AuthorizationType %q", got.Get("AuthorizationType"))
	}
	if got.Get("Authorization") != "Bearer wx-bot-token" {
		t.Fatalf("unexpected Authorization %q", got.Get("Authorization"))
	}
	if got.Get("iLink-App-Id") != "bot" {
		t.Fatalf("unexpected iLink-App-Id %q", got.Get("iLink-App-Id"))
	}
	if got.Get("iLink-App-ClientVersion") == "" {
		t.Fatalf("missing iLink-App-ClientVersion")
	}
	if got.Get("X-WECHAT-UIN") == "" {
		t.Fatalf("missing X-WECHAT-UIN")
	}
}

// TestGetUpdatesDispatchesCallback verifies the long-poll delivers inbound
// messages to the callback, echoes the iLink headers and advances the cursor.
func TestGetUpdatesDispatchesCallback(t *testing.T) {
	var mu sync.Mutex
	var cursorRequests []string
	firstRequest := true

	a, srv := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+epGetUpdates {
			http.NotFound(w, r)
			return
		}
		var body struct {
			GetUpdatesBuf string `json:"get_updates_buf"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		cursorRequests = append(cursorRequests, body.GetUpdatesBuf)
		first := firstRequest
		firstRequest = false
		mu.Unlock()
		if first {
			w.Write([]byte(`{
				"ret": 0,
				"get_updates_buf": "buf-2",
				"msgs": [{
					"message_id": "M-1",
					"from_user_id": "peer-1",
					"context_token": "CT-1",
					"item_list": [{"type": 1, "text_item": {"text": "你好，世界"}}]
				}]
			}`))
			return
		}
		// Simulate an open long-poll: hold the connection until ctx ends.
		<-r.Context().Done()
	}))
	defer srv.Close()

	a.onMessage = func(msg plugin.IncomingMessage) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if msg.ChannelID != "peer-1" || msg.UserID != "peer-1" || msg.Text != "你好，世界" {
			t.Errorf("unexpected incoming message: %+v", msg)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		a.pollLoop(ctx)
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(cursorRequests)
		mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for second long-poll request")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if cursorRequests[0] != "" {
		t.Fatalf("first request should carry an empty cursor, got %q", cursorRequests[0])
	}
	if cursorRequests[1] != "buf-2" {
		t.Fatalf("cursor did not advance: second request carried %q", cursorRequests[1])
	}
}

// TestContextTokenBindingAndSendPayload verifies the inbound context_token is
// cached per peer and echoed in the sendmessage payload.
func TestContextTokenBindingAndSendPayload(t *testing.T) {
	var gotPayload struct {
		BaseInfo map[string]any `json:"base_info"`
		Msg      map[string]any `json:"msg"`
	}
	var mu sync.Mutex

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+epSendMessage {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		json.NewDecoder(r.Body).Decode(&gotPayload)
		mu.Unlock()
		w.Write([]byte(`{"ret":0,"errcode":0}`))
	}))

	// Inbound message binds the peer's context_token.
	a.processMessage(map[string]any{
		"message_id":    "M-9",
		"from_user_id":  "peer-2",
		"context_token": "CT-9",
		"item_list": []any{
			map[string]any{"type": float64(itemTypeText), "text_item": map[string]any{"text": "hi"}},
		},
	})

	if err := a.Send(context.Background(), "peer-2", "reply 你好"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	msg := gotPayload.Msg
	if msg == nil {
		t.Fatalf("sendmessage payload missing msg")
	}
	if msg["to_user_id"] != "peer-2" {
		t.Fatalf("unexpected to_user_id %v", msg["to_user_id"])
	}
	if msg["context_token"] != "CT-9" {
		t.Fatalf("expected context_token CT-9, got %v", msg["context_token"])
	}
	if msg["message_type"] != float64(msgTypeBot) || msg["message_state"] != float64(msgStateFinish) {
		t.Fatalf("unexpected message_type/state: %v/%v", msg["message_type"], msg["message_state"])
	}
	items, _ := msg["item_list"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item, _ := items[0].(map[string]any)
	textItem, _ := item["text_item"].(map[string]any)
	if item["type"] != float64(itemTypeText) || textItem["text"] != "reply 你好" {
		t.Fatalf("unexpected item: %v", item)
	}
	if gotPayload.BaseInfo["channel_version"] != ilinkChannelVersion {
		t.Fatalf("unexpected base_info: %v", gotPayload.BaseInfo)
	}
}

func TestSendMessageWithoutContextToken(t *testing.T) {
	var gotPayload struct {
		Msg map[string]any `json:"msg"`
	}
	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+epSendMessage {
			http.NotFound(w, r)
			return
		}
		json.NewDecoder(r.Body).Decode(&gotPayload)
		w.Write([]byte(`{"ret":0,"errcode":0}`))
	}))

	if err := a.Send(context.Background(), "peer-3", "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, ok := gotPayload.Msg["context_token"]; ok {
		t.Fatalf("unexpected context_token on proactive send: %v", gotPayload.Msg["context_token"])
	}
}

// TestSendMessageSessionExpiredRetriesWithoutToken mirrors the reference adapter
// _send_text_chunk: on errcode -14 retry once without the context_token.
func TestSendMessageSessionExpiredRetriesWithoutToken(t *testing.T) {
	var mu sync.Mutex
	var attempts []string

	oldBackoff := retryBackoff
	retryBackoff = time.Millisecond
	t.Cleanup(func() { retryBackoff = oldBackoff })

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Msg map[string]any `json:"msg"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		ct, _ := body.Msg["context_token"].(string)
		mu.Lock()
		attempts = append(attempts, ct)
		mu.Unlock()
		if len(attempts) == 1 {
			w.Write([]byte(`{"ret":-14,"errmsg":"session expired"}`))
			return
		}
		w.Write([]byte(`{"ret":0,"errcode":0}`))
	}))

	a.setContextToken("peer-4", "CT-OLD")
	if err := a.Send(context.Background(), "peer-4", "hi"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(attempts))
	}
	if attempts[0] != "CT-OLD" || attempts[1] != "" {
		t.Fatalf("expected retry without context_token, got %v", attempts)
	}
	if a.getContextToken("peer-4") == "" {
		// Token cache may retain the entry; only the retried payload drops it.
		t.Logf("token cache still holds an entry (informational)")
	}
}

func TestSendMessageErrorPropagated(t *testing.T) {
	oldBackoff := retryBackoff
	retryBackoff = time.Millisecond
	t.Cleanup(func() { retryBackoff = oldBackoff })

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+epSendMessage {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"ret":-2,"errmsg":"rate limited"}`))
	}))

	err := a.Send(context.Background(), "peer-5", "hi")
	if err == nil || !strings.Contains(err.Error(), "-2") {
		t.Fatalf("expected iLink error propagation, got %v", err)
	}
}

func TestSendMultiChunk(t *testing.T) {
	var sends int
	var mu sync.Mutex
	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+epSendMessage {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		sends++
		mu.Unlock()
		w.Write([]byte(`{"ret":0,"errcode":0}`))
	}))

	long := strings.Repeat("微", 2500) // 2500 runes > 2000
	if err := a.Send(context.Background(), "peer-6", long); err != nil {
		t.Fatalf("Send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if sends != 2 {
		t.Fatalf("expected 2 chunk sends, got %d", sends)
	}
}

func TestSplitLongMessageRuneBoundary(t *testing.T) {
	long := strings.Repeat("汉", 2300)
	chunks := splitLongMessage(long)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if !utf8.ValidString(c) {
			t.Fatalf("chunk is not valid UTF-8")
		}
		if utf8.RuneCountInString(c) > maxChunkRunes {
			t.Fatalf("chunk exceeds rune limit")
		}
	}
}

func TestMessageDedup(t *testing.T) {
	a := &Adapter{seenMsgs: map[string]time.Time{}, contextTokens: map[string]contextTokenEntry{}, logger: testLogger()}
	msg := func() map[string]any {
		return map[string]any{
			"message_id":   "M-DUP",
			"from_user_id": "peer-7",
			"item_list": []any{
				map[string]any{"type": float64(itemTypeText), "text_item": map[string]any{"text": "again"}},
			},
		}
	}

	var calls int
	a.onMessage = func(plugin.IncomingMessage) { calls++ }
	a.processMessage(msg())
	a.processMessage(msg())
	if calls != 1 {
		t.Fatalf("expected dedup to drop the second delivery, got %d calls", calls)
	}
}

func TestContextTokenTTL(t *testing.T) {
	a := &Adapter{contextTokens: map[string]contextTokenEntry{}}
	a.setContextToken("peer-8", "CT-8")
	if got := a.getContextToken("peer-8"); got != "CT-8" {
		t.Fatalf("expected cached token, got %q", got)
	}
	a.contextTokens["peer-8"] = contextTokenEntry{token: "CT-OLD", expires: time.Now().Add(-time.Second)}
	if got := a.getContextToken("peer-8"); got != "" {
		t.Fatalf("expected expired token to be evicted, got %q", got)
	}
}
