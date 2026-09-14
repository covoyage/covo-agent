package msteams

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

	"github.com/covoyage/covo-agent/internal/plugin"
)

type testServer struct {
	*httptest.Server

	mu       sync.Mutex
	tokenN   int
	sentMsgs []graphSend
	messages []chatMessage // served by the messages list endpoint
	listN    int
}

type graphSend struct {
	ChatID string
	Body   chatBody
	Auth   string
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	ts := &testServer{}

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if got := r.FormValue("grant_type"); got != "client_credentials" {
			t.Errorf("grant_type = %q", got)
		}
		if got := r.FormValue("scope"); got != graphScope {
			t.Errorf("scope = %q", got)
		}
		if got := r.FormValue("client_id"); got != "app-1" {
			t.Errorf("client_id = %q", got)
		}
		ts.mu.Lock()
		ts.tokenN++
		ts.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-1", "expires_in": 3600})
	})
	mux.HandleFunc("/v1.0/chats/", func(w http.ResponseWriter, r *http.Request) {
		chatID := strings.TrimPrefix(r.URL.Path, "/v1.0/chats/")
		if idx := strings.Index(chatID, "/"); idx >= 0 {
			chatID = chatID[:idx]
		}
		switch {
		case r.Method == http.MethodPost:
			var body struct {
				Body chatBody `json:"body"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			ts.mu.Lock()
			ts.sentMsgs = append(ts.sentMsgs, graphSend{ChatID: chatID, Body: body.Body, Auth: r.Header.Get("Authorization")})
			ts.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"m-new"}`))
		case r.Method == http.MethodGet:
			ts.mu.Lock()
			msgs := append([]chatMessage(nil), ts.messages...)
			ts.listN++
			ts.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"value": msgs})
		}
	})

	ts.Server = httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func newTestAdapter(t *testing.T, ts *testServer) *Adapter {
	t.Helper()
	return &Adapter{
		tenantID:     "tenant-1",
		clientID:     "app-1",
		clientSecret: "secret-1",
		chatID:       "19:a@thread.tacv2",
		pollSeconds:  1,
		httpClient:   ts.Client(),
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		tokenURL:     ts.URL + "/token",
		graphBase:    ts.URL,
		seen:         make(map[string]struct{}),
	}
}

func TestValidateRequiresCredentials(t *testing.T) {
	a := &Adapter{}
	err := a.Validate()
	if err == nil || !strings.Contains(err.Error(), "MSTEAMS_TENANT_ID") {
		t.Fatalf("Validate() = %v, want credential env error", err)
	}
}

func TestSendMessageHTMLBody(t *testing.T) {
	ts := newTestServer(t)
	a := newTestAdapter(t, ts)

	if err := a.SendMessage(context.Background(), "19:a@thread.tacv2", plugin.OutgoingMessage{Text: "hello"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.sentMsgs) != 1 {
		t.Fatalf("sends = %d, want 1", len(ts.sentMsgs))
	}
	m := ts.sentMsgs[0]
	if m.Auth != "Bearer tok-1" {
		t.Errorf("Authorization = %q", m.Auth)
	}
	if m.ChatID != "19:a@thread.tacv2" {
		t.Errorf("chat id = %q", m.ChatID)
	}
	if m.Body.ContentType != "html" || m.Body.Content != "hello" {
		t.Errorf("body = %+v", m.Body)
	}
}

func TestSendMessageChunksLongText(t *testing.T) {
	ts := newTestServer(t)
	a := newTestAdapter(t, ts)

	text := strings.Repeat("x", 28000+5000)
	if err := a.SendMessage(context.Background(), "19:a@thread.tacv2", plugin.OutgoingMessage{Text: text}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.sentMsgs) != 2 {
		t.Fatalf("chunks = %d, want 2", len(ts.sentMsgs))
	}
	var joined strings.Builder
	for _, m := range ts.sentMsgs {
		if len(m.Body.Content) > maxMessageBytes {
			t.Errorf("chunk too long: %d", len(m.Body.Content))
		}
		joined.WriteString(m.Body.Content)
	}
	if joined.String() != text {
		t.Fatal("chunks do not preserve text")
	}
}

func TestSendMessageAPIError(t *testing.T) {
	ts := newTestServer(t)
	a := newTestAdapter(t, ts)

	// The shared handler returns 201 for POSTs, so simulate an error by
	// pointing the adapter at a graphBase whose path never matches.
	a.graphBase = ts.URL
	err := a.SendMessage(context.Background(), "19:a@thread.tacv2", plugin.OutgoingMessage{Text: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Error parse: use a chat id that triggers 4xx via a broken endpoint.
	a2 := newTestAdapter(t, ts)
	a2.graphBase = ts.URL + "/nope"
	err = a2.SendMessage(context.Background(), "19:b@thread.tacv2", plugin.OutgoingMessage{Text: "hi"})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("SendMessage error = %v, want 404 failure", err)
	}
}

func TestTokenCached(t *testing.T) {
	ts := newTestServer(t)
	a := newTestAdapter(t, ts)
	for i := 0; i < 3; i++ {
		if _, err := a.getToken(context.Background()); err != nil {
			t.Fatalf("getToken call %d: %v", i, err)
		}
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.tokenN != 1 {
		t.Fatalf("token endpoint called %d times, want 1", ts.tokenN)
	}
}

func TestPollIncrementalDedupAndSelfSkip(t *testing.T) {
	ts := newTestServer(t)
	a := newTestAdapter(t, ts)

	var mu sync.Mutex
	var got []plugin.IncomingMessage
	a.OnMessage(func(msg plugin.IncomingMessage) {
		mu.Lock()
		got = append(got, msg)
		mu.Unlock()
	})

	// Initial state: one message from our own application (must be skipped
	// even on later polls) and one from a real user.
	ts.mu.Lock()
	ts.messages = []chatMessage{
		{ID: "m1", LastModifiedDateTime: "2026-01-01T00:00:00Z",
			From: &chatFrom{Application: &chatIdentity{ID: "app-1", DisplayName: "bot"}},
			Body: chatBody{ContentType: "html", Content: "bot msg"}},
		{ID: "m2", LastModifiedDateTime: "2026-01-01T00:01:00Z",
			From: &chatFrom{User: &chatIdentity{ID: "user-1", DisplayName: "Alice"}},
			Body: chatBody{ContentType: "html", Content: "hi there"}},
	}
	ts.mu.Unlock()

	// First pass: unseen user messages are emitted; self-application
	// messages are filtered out.
	if err := a.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce: %v", err)
	}
	mu.Lock()
	if len(got) != 1 {
		mu.Unlock()
		t.Fatalf("callbacks = %d, want 1", len(got))
	}
	msg := got[0]
	mu.Unlock()
	if msg.ChannelID != "19:a@thread.tacv2" || msg.UserID != "user-1" || msg.Text != "hi there" {
		t.Fatalf("message = %+v", msg)
	}
	if msg.UserName != "Alice" {
		t.Errorf("user name = %q", msg.UserName)
	}

	// New message arrives; only it is emitted. Self-application messages
	// are filtered out.
	ts.mu.Lock()
	ts.messages = append([]chatMessage{
		{ID: "m3", LastModifiedDateTime: "2026-01-01T00:02:00Z",
			From: &chatFrom{Application: &chatIdentity{ID: "app-1"}},
			Body: chatBody{Content: "self echo"}},
		{ID: "m4", LastModifiedDateTime: "2026-01-01T00:03:00Z",
			From: &chatFrom{User: &chatIdentity{ID: "user-2", DisplayName: "Bob"}},
			Body: chatBody{Content: "from bob"}},
	}, ts.messages...)
	ts.mu.Unlock()

	if err := a.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce 2: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("callbacks = %d, want 2", len(got))
	}
	if got[1].UserID != "user-2" || got[1].Text != "from bob" {
		t.Fatalf("second message = %+v", got[1])
	}
}

func TestPollSkipsMessagesWithoutSender(t *testing.T) {
	ts := newTestServer(t)
	a := newTestAdapter(t, ts)

	var mu sync.Mutex
	var got []plugin.IncomingMessage
	a.OnMessage(func(msg plugin.IncomingMessage) {
		mu.Lock()
		got = append(got, msg)
		mu.Unlock()
	})

	ts.mu.Lock()
	ts.messages = []chatMessage{
		{ID: "m1", Body: chatBody{Content: "no sender"}},
		{ID: "m2", From: &chatFrom{User: &chatIdentity{ID: "user-1"}}, Body: chatBody{Content: "ok"}},
	}
	ts.mu.Unlock()

	// Direct pass so the sender filter is exercised.
	if err := a.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Text != "ok" {
		t.Fatalf("callbacks = %+v", got)
	}
}

func TestStartVerifiesToken(t *testing.T) {
	ts := newTestServer(t)
	a := newTestAdapter(t, ts)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = a.Stop() }()
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.tokenN == 0 {
		t.Fatal("Start did not acquire a token")
	}
}

func TestPollListChatsFallback(t *testing.T) {
	// When chatID is empty the adapter enumerates chats first. The test
	// server serves an empty chat list for that path, so the poll must
	// succeed without emitting anything.
	ts := newTestServer(t)
	a := newTestAdapter(t, ts)
	a.chatID = ""
	if err := a.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce: %v", err)
	}
}
