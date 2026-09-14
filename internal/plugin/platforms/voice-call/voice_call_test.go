package voicecall

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestAdapter returns an adapter whose Twilio API base URL points at the
// given handler (injected via the unexported apiBase field).
func newTestAdapter(t *testing.T, handler http.Handler) (*Adapter, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a := &Adapter{
		provider:   "twilio",
		accountSID: "AC123",
		authToken:  "test-token",
		fromNumber: "+15550000001",
		apiBase:    srv.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		logger:     testLogger(),
	}
	return a, srv
}

func TestValidate(t *testing.T) {
	a := &Adapter{provider: "twilio"}
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "TWILIO_ACCOUNT_SID") {
		t.Fatalf("expected TWILIO_ACCOUNT_SID error, got %v", err)
	}
	a.accountSID = "AC123"
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "TWILIO_AUTH_TOKEN") {
		t.Fatalf("expected TWILIO_AUTH_TOKEN error, got %v", err)
	}
	a.authToken = "token"
	if err := a.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	a.provider = "unknown"
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("expected unsupported provider error, got %v", err)
	}
}

func TestSendMessagePlacesCall(t *testing.T) {
	var mu sync.Mutex
	var gotPath, gotAuth string
	var form url.Values

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gotPath = r.URL.Path
		gotUser, gotPass, _ := r.BasicAuth()
		gotAuth = gotUser + ":" + gotPass
		_ = r.ParseForm()
		form = r.PostForm
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sid":"CA7777","status":"queued"}`))
	}))

	if err := a.SendMessage(context.Background(), "+15557654321", plugin.OutgoingMessage{Text: "hello <world> & friends"}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	wantPath := "/2010-04-01/Accounts/AC123/Calls.json"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
	if gotAuth != "AC123:test-token" {
		t.Errorf("basic auth = %q, want AC123:test-token", gotAuth)
	}
	if form.Get("To") != "+15557654321" {
		t.Errorf("To = %q, want +15557654321", form.Get("To"))
	}
	if form.Get("From") != "+15550000001" {
		t.Errorf("From = %q, want +15550000001", form.Get("From"))
	}
	twiml := form.Get("Twiml")
	if !strings.HasPrefix(twiml, "<Response><Say>") || !strings.HasSuffix(twiml, "</Say></Response>") {
		t.Errorf("unexpected TwiML: %q", twiml)
	}
	if !strings.Contains(twiml, "hello &lt;world&gt; &amp; friends") {
		t.Errorf("TwiML text not XML-escaped: %q", twiml)
	}
}

func TestSendMessageMultiCall(t *testing.T) {
	var mu sync.Mutex
	calls := 0

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sid":"CA1","status":"queued"}`))
	}))

	// 9000 runes → 3 segments of at most 4096 → 3 calls.
	if err := a.SendMessage(context.Background(), "+15557654321", plugin.OutgoingMessage{Text: strings.Repeat("z", 9000)}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestSendMessageError(t *testing.T) {
	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"Unable to create record: The 'To' number is not a valid phone number"}`))
	}))

	err := a.SendMessage(context.Background(), "not-a-number", plugin.OutgoingMessage{Text: "hi"})
	if err == nil || !strings.Contains(err.Error(), "The 'To' number is not a valid phone number") {
		t.Fatalf("expected error containing Twilio message, got %v", err)
	}
}

func TestSendMessageUnsupportedProvider(t *testing.T) {
	a := &Adapter{provider: "telnyx", logger: testLogger()}
	if err := a.SendMessage(context.Background(), "+15557654321", plugin.OutgoingMessage{Text: "hi"}); err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}

func TestStatusCallback(t *testing.T) {
	a := &Adapter{logger: testLogger()}
	callbacks := make(chan plugin.IncomingMessage, 4)
	a.OnMessage(func(m plugin.IncomingMessage) { callbacks <- m })

	srv := httptest.NewServer(http.HandlerFunc(a.handleStatusCallback))
	defer srv.Close()

	form := strings.NewReader("CallSid=CA1&CallStatus=completed&Digits=1&From=%2B15551234567&To=%2B15550000001")
	resp, err := http.Post(srv.URL, "application/x-www-form-urlencoded", form)
	if err != nil {
		t.Fatalf("status callback request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	select {
	case m := <-callbacks:
		if m.Text != "<call status>" {
			t.Errorf("text = %q, want <call status>", m.Text)
		}
		raw, ok := m.Raw.(map[string]string)
		if !ok || raw["CallStatus"] != "completed" || raw["Digits"] != "1" {
			t.Errorf("unexpected raw payload: %+v", m.Raw)
		}
		if m.ChannelID != "+15550000001" {
			t.Errorf("channelID = %q, want +15550000001", m.ChannelID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for callback")
	}
}

// signTwilio computes the X-Twilio-Signature value for a request URL and
// POST parameters, following Twilio's validation algorithm.
func signTwilio(token, requestURL string, params url.Values) string {
	mac := hmac.New(sha1.New, []byte(token))
	mac.Write([]byte(requestURL))
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, value := range params[key] {
			mac.Write([]byte(key))
			mac.Write([]byte(value))
		}
	}
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// TestStatusCallbackSignatureValid verifies that a correctly signed status
// callback is accepted when an auth token is configured.
func TestStatusCallbackSignatureValid(t *testing.T) {
	a := &Adapter{authToken: "test-token", logger: testLogger()}
	callbacks := make(chan plugin.IncomingMessage, 4)
	a.OnMessage(func(m plugin.IncomingMessage) { callbacks <- m })

	srv := httptest.NewServer(http.HandlerFunc(a.handleStatusCallback))
	defer srv.Close()

	params := url.Values{"CallSid": {"CA9"}, "CallStatus": {"ringing"}, "From": {"+15551234567"}, "To": {"+15550000001"}}
	requestURL := srv.URL + "/voice-call/status"
	req, _ := http.NewRequest("POST", requestURL, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", signTwilio("test-token", requestURL, params))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("status callback request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	select {
	case m := <-callbacks:
		if raw, ok := m.Raw.(map[string]string); !ok || raw["CallSid"] != "CA9" {
			t.Errorf("unexpected message: %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for callback")
	}
}

// TestStatusCallbackSignatureInvalid verifies that callbacks with a missing,
// wrong-URL-signed or forged signature are rejected with 403.
func TestStatusCallbackSignatureInvalid(t *testing.T) {
	params := url.Values{"CallSid": {"CA9"}, "CallStatus": {"completed"}}

	cases := []struct {
		name string
		sig  string
	}{
		{"missing signature", ""},
		{"signed for another URL", signTwilio("test-token", "https://attacker.example/voice-call/status", params)},
		{"wrong token", signTwilio("other-token", "", params)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Adapter{authToken: "test-token", logger: testLogger()}
			callbacks := make(chan plugin.IncomingMessage, 1)
			a.OnMessage(func(m plugin.IncomingMessage) { callbacks <- m })

			srv := httptest.NewServer(http.HandlerFunc(a.handleStatusCallback))
			defer srv.Close()

			requestURL := srv.URL + "/voice-call/status"
			req, _ := http.NewRequest("POST", requestURL, strings.NewReader(params.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.sig != "" {
				req.Header.Set("X-Twilio-Signature", tc.sig)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("status callback request failed: %v", err)
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
		})
	}
}

func TestBuildTwiML(t *testing.T) {
	got := buildTwiML(`say "hi" & <bye>`)
	want := `<Response><Say>say &#34;hi&#34; &amp; &lt;bye&gt;</Say></Response>`
	if got != want {
		t.Errorf("buildTwiML = %q, want %q", got, want)
	}
}
