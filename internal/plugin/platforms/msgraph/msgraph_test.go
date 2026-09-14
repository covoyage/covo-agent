package msgraph

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

type graphSend struct {
	Recipient string
	Subject   string
	Content   string
	SaveSent  bool
	Auth      string
}

type testServer struct {
	*httptest.Server

	mu        sync.Mutex
	tokenN    int
	sentMails []graphSend
	messages  []mailMessage // served by the inbox listing endpoint
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
		ts.mu.Lock()
		ts.tokenN++
		ts.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-1", "expires_in": 3600})
	})
	mux.HandleFunc("/v1.0/users/bot@example.com/sendMail", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Message struct {
				Subject      string       `json:"subject"`
				Body         chatBodyLike `json:"body"`
				ToRecipients []struct {
					EmailAddress struct {
						Address string `json:"address"`
					} `json:"emailAddress"`
				} `json:"toRecipients"`
			} `json:"message"`
			SaveToSentItems bool `json:"saveToSentItems"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		ts.mu.Lock()
		ts.sentMails = append(ts.sentMails, graphSend{
			Recipient: body.Message.ToRecipients[0].EmailAddress.Address,
			Subject:   body.Message.Subject,
			Content:   body.Message.Body.Content,
			SaveSent:  body.SaveToSentItems,
			Auth:      r.Header.Get("Authorization"),
		})
		ts.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("/v1.0/users/bot@example.com/mailFolders/inbox/messages", func(w http.ResponseWriter, r *http.Request) {
		ts.mu.Lock()
		msgs := append([]mailMessage(nil), ts.messages...)
		ts.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"value": msgs})
	})

	ts.Server = httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

type chatBodyLike struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

func newTestAdapter(t *testing.T, ts *testServer) *Adapter {
	t.Helper()
	return &Adapter{
		tenantID:     "tenant-1",
		clientID:     "app-1",
		clientSecret: "secret-1",
		mailbox:      "bot@example.com",
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
	if err == nil || !strings.Contains(err.Error(), "MSGRAPH_TENANT_ID") {
		t.Fatalf("Validate() = %v, want credential env error", err)
	}
}

func TestSendMail(t *testing.T) {
	ts := newTestServer(t)
	a := newTestAdapter(t, ts)

	err := a.SendMessage(context.Background(), "alice@example.com", plugin.OutgoingMessage{
		Text:      "weekly status",
		ParseMode: "Weekly Report",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.sentMails) != 1 {
		t.Fatalf("mails = %d, want 1", len(ts.sentMails))
	}
	m := ts.sentMails[0]
	if m.Auth != "Bearer tok-1" {
		t.Errorf("Authorization = %q", m.Auth)
	}
	if m.Recipient != "alice@example.com" {
		t.Errorf("recipient = %q", m.Recipient)
	}
	if m.Subject != "Weekly Report" {
		t.Errorf("subject = %q", m.Subject)
	}
	if m.Content != "weekly status" {
		t.Errorf("content = %q", m.Content)
	}
	if !m.SaveSent {
		t.Error("saveToSentItems = false, want true")
	}
}

func TestSendMailNoSubjectWithoutParseMode(t *testing.T) {
	ts := newTestServer(t)
	a := newTestAdapter(t, ts)
	if err := a.SendMessage(context.Background(), "alice@example.com", plugin.OutgoingMessage{Text: "hi"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.sentMails[0].Subject != "" {
		t.Errorf("subject = %q, want empty", ts.sentMails[0].Subject)
	}
}

func TestSendMailErrorParsing(t *testing.T) {
	ts := newTestServer(t)
	a := newTestAdapter(t, ts)
	a.graphBase = ts.URL + "/nope"

	err := a.SendMessage(context.Background(), "alice@example.com", plugin.OutgoingMessage{Text: "hi"})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("SendMessage error = %v, want 404 failure", err)
	}
}

func TestSendMailRequiresMailbox(t *testing.T) {
	ts := newTestServer(t)
	a := newTestAdapter(t, ts)
	a.mailbox = ""
	err := a.SendMessage(context.Background(), "alice@example.com", plugin.OutgoingMessage{Text: "hi"})
	if err == nil || !strings.Contains(err.Error(), "MSGRAPH_MAILBOX") {
		t.Fatalf("SendMessage error = %v, want mailbox env error", err)
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

	ts.mu.Lock()
	ts.messages = []mailMessage{
		{ID: "e1", Subject: "self mail", BodyPreview: "from myself",
			From: &mailFrom{EmailAddress: &mailAddress{Name: "Bot", Address: "bot@example.com"}}},
		{ID: "e2", Subject: "Hello", BodyPreview: "how are you",
			From: &mailFrom{EmailAddress: &mailAddress{Name: "Alice", Address: "alice@example.com"}}},
	}
	ts.mu.Unlock()

	// First pass: user mail emitted, self mail skipped.
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
	if msg.ChannelID != "alice@example.com" || msg.UserID != "alice@example.com" {
		t.Errorf("channel/user = %q/%q", msg.ChannelID, msg.UserID)
	}
	if want := "Hello\n\nhow are you"; msg.Text != want {
		t.Errorf("text = %q, want %q", msg.Text, want)
	}
	if msg.UserName != "Alice" {
		t.Errorf("user name = %q", msg.UserName)
	}

	// Second pass over the same data: fully deduplicated.
	if err := a.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce 2: %v", err)
	}
	mu.Lock()
	if len(got) != 1 {
		mu.Unlock()
		t.Fatalf("callbacks after dedup = %d, want 1", len(got))
	}
	mu.Unlock()

	// New mail arrives; only it is emitted.
	ts.mu.Lock()
	ts.messages = append([]mailMessage{
		{ID: "e3", Subject: "Ping", BodyPreview: "ping",
			From: &mailFrom{EmailAddress: &mailAddress{Name: "Bob", Address: "bob@example.com"}}},
	}, ts.messages...)
	ts.mu.Unlock()

	if err := a.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce 3: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("callbacks = %d, want 2", len(got))
	}
	if got[1].ChannelID != "bob@example.com" || !strings.HasPrefix(got[1].Text, "Ping") {
		t.Fatalf("second message = %+v", got[1])
	}
}

func TestStartRequiresMailboxAndVerifiesToken(t *testing.T) {
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

	b := newTestAdapter(t, ts)
	b.mailbox = ""
	if err := b.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "MSGRAPH_MAILBOX") {
		t.Fatalf("Start without mailbox = %v, want env error", err)
	}
}
