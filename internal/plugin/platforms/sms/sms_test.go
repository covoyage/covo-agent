package sms

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
		accountSID: "AC123",
		authToken:  "test-token",
		fromNumber: "+15550000001",
		apiBase:    srv.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		logger:     testLogger(),
	}
	return a, srv
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

func TestValidate(t *testing.T) {
	a := &Adapter{}
	err := a.Validate()
	if err == nil || !strings.Contains(err.Error(), "must be set in environment") {
		t.Fatalf("expected missing credentials error, got %v", err)
	}
	a.accountSID = "AC123"
	a.authToken = "token"
	a.fromNumber = "+15550000001"
	if err := a.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestSendMessageSuccess(t *testing.T) {
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
		w.Write([]byte(`{"sid":"SM8888","status":"queued"}`))
	}))

	if err := a.SendMessage(context.Background(), "+15557654321", plugin.OutgoingMessage{Text: "hello sms"}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	wantPath := "/2010-04-01/Accounts/AC123/Messages.json"
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
	if form.Get("Body") != "hello sms" {
		t.Errorf("Body = %q, want hello sms", form.Get("Body"))
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

func TestSendMessageMultiSegment(t *testing.T) {
	var mu sync.Mutex
	var bodies []string

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		bodies = append(bodies, r.PostFormValue("Body"))
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sid":"SM1","status":"queued"}`))
	}))

	text := strings.Repeat("x", 2500)
	if err := a.SendMessage(context.Background(), "+15557654321", plugin.OutgoingMessage{Text: text}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("segments = %d, want 2 (bodies: %v)", len(bodies), bodies)
	}
	for i, b := range bodies {
		want := 1600
		if i == 1 {
			want = 900 // 2500 runes total
		}
		if len([]rune(b)) != want {
			t.Errorf("segment %d length = %d, want %d", i+1, len([]rune(b)), want)
		}
		if strings.Contains(b, "(1/2)") || strings.Contains(b, "(2/2)") {
			t.Errorf("segment %d carries an ordinal suffix: %q", i+1, b[len(b)-10:])
		}
	}
}

func TestWebhookSignatureValid(t *testing.T) {
	a := &Adapter{authToken: "test-token", logger: testLogger()}
	callbacks := make(chan plugin.IncomingMessage, 4)
	a.OnMessage(func(m plugin.IncomingMessage) { callbacks <- m })

	srv := httptest.NewServer(http.HandlerFunc(a.handleWebhook))
	defer srv.Close()

	params := url.Values{
		"From":       {"+15551234567"},
		"To":         {"+15550000001"},
		"Body":       {"hello from sms"},
		"MessageSid": {"SM9999"},
	}

	requestURL := srv.URL + "/twilio/incoming"
	req, _ := http.NewRequest("POST", requestURL, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", signTwilio("test-token", requestURL, params))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("webhook request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	select {
	case m := <-callbacks:
		if m.ChannelID != "+15551234567" || m.UserID != "+15551234567" || m.Text != "hello from sms" {
			t.Errorf("unexpected message: %+v", m)
		}
		if m.Platform != PlatformName {
			t.Errorf("platform = %q, want %q", m.Platform, PlatformName)
		}
		raw, ok := m.Raw.(map[string]string)
		if !ok || raw["MessageSid"] != "SM9999" {
			t.Errorf("unexpected raw payload: %+v", m.Raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for callback")
	}
}

func TestWebhookSignatureInvalid(t *testing.T) {
	a := &Adapter{authToken: "test-token", logger: testLogger()}
	callbacks := make(chan plugin.IncomingMessage, 1)
	a.OnMessage(func(m plugin.IncomingMessage) { callbacks <- m })

	srv := httptest.NewServer(http.HandlerFunc(a.handleWebhook))
	defer srv.Close()

	params := url.Values{"From": {"+15551234567"}, "Body": {"should not pass"}}
	requestURL := srv.URL + "/twilio/incoming"
	req, _ := http.NewRequest("POST", requestURL, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Signed for a different URL.
	req.Header.Set("X-Twilio-Signature", signTwilio("test-token", "https://attacker.example/twilio/incoming", params))
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
	a := &Adapter{authToken: "test-token", logger: testLogger()}
	srv := httptest.NewServer(http.HandlerFunc(a.handleWebhook))
	defer srv.Close()

	resp, err := http.Post(srv.URL, "application/x-www-form-urlencoded", strings.NewReader("From=%2B15551234567&Body=hi"))
	if err != nil {
		t.Fatalf("webhook request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestSplitSMS(t *testing.T) {
	if got := splitSMS("short", 1600); len(got) != 1 || got[0] != "short" {
		t.Errorf("single segment expected, got %v", got)
	}
	got := splitSMS(strings.Repeat("y", 3200), 1600)
	if len(got) != 2 {
		t.Fatalf("segments = %d, want 2", len(got))
	}
	if got[0] != strings.Repeat("y", 1600) || got[1] != strings.Repeat("y", 1600) {
		t.Errorf("segments carry unexpected content or suffixes: %q", got)
	}
}

// TestWebhookSignatureWithQuery verifies that the signature input includes
// the request query string (Twilio signs the full URL).
func TestWebhookSignatureWithQuery(t *testing.T) {
	a := &Adapter{authToken: "test-token", logger: testLogger()}
	callbacks := make(chan plugin.IncomingMessage, 4)
	a.OnMessage(func(m plugin.IncomingMessage) { callbacks <- m })

	srv := httptest.NewServer(http.HandlerFunc(a.handleWebhook))
	defer srv.Close()

	params := url.Values{
		"From": {"+15551234567"},
		"Body": {"hello with query"},
	}
	requestURL := srv.URL + "/twilio/incoming?accountSid=AC123&timestamp=1700000000"
	req, _ := http.NewRequest("POST", requestURL, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", signTwilio("test-token", requestURL, params))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("webhook request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	select {
	case m := <-callbacks:
		if m.Text != "hello with query" {
			t.Errorf("text = %q", m.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for callback")
	}

	// A signature computed over the URL without the query must be rejected.
	params2 := url.Values{"From": {"+15551234567"}, "Body": {"forged"}}
	req2, _ := http.NewRequest("POST", requestURL, strings.NewReader(params2.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Header.Set("X-Twilio-Signature", signTwilio("test-token", srv.URL+"/twilio/incoming", params2))
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("webhook request failed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 when query is missing from signed URL", resp2.StatusCode)
	}
}

// TestWebhookSignatureWithForwardedHeaders verifies the proxy header priority
// in URL reconstruction.
func TestWebhookSignatureWithForwardedHeaders(t *testing.T) {
	a := &Adapter{authToken: "test-token", logger: testLogger()}

	params := url.Values{"From": {"+15551234567"}, "Body": {"hi"}}
	r := httptest.NewRequest("POST", "http://internal:8082/twilio/incoming", strings.NewReader(params.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "sms.example.com")
	_ = r.ParseForm()

	// Signed for the public https URL.
	sig := signTwilio("test-token", "https://sms.example.com/twilio/incoming", params)
	r.Header.Set("X-Twilio-Signature", sig)
	if !a.verifyTwilioSignature(sig, requestURLFor("", r), r.PostForm) {
		t.Error("signature should validate with forwarded proto/host in the URL")
	}
}

// TestWebhookMediaOnly verifies inbound media-only messages (empty Body with
// MediaUrlN) are forwarded with joined media URLs as the text.
func TestWebhookMediaOnly(t *testing.T) {
	a := &Adapter{authToken: "test-token", logger: testLogger()}
	callbacks := make(chan plugin.IncomingMessage, 4)
	a.OnMessage(func(m plugin.IncomingMessage) { callbacks <- m })

	srv := httptest.NewServer(http.HandlerFunc(a.handleWebhook))
	defer srv.Close()

	params := url.Values{
		"From":               {"+15551234567"},
		"NumMedia":           {"2"},
		"MediaUrl0":          {"https://api.twilio.com/media/photo.png"},
		"MediaContentType0":  {"image/png"},
		"MediaUrl1":          {"https://api.twilio.com/media/clip.mp4"},
		"MediaContentType1":  {"video/mp4"},
		"MessageSid":         {"SM777"},
	}
	requestURL := srv.URL + "/twilio/incoming"
	req, _ := http.NewRequest("POST", requestURL, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", signTwilio("test-token", requestURL, params))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("webhook request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	select {
	case m := <-callbacks:
		if !strings.Contains(m.Text, "https://api.twilio.com/media/photo.png") ||
			!strings.Contains(m.Text, "https://api.twilio.com/media/clip.mp4") {
			t.Errorf("text should contain media URLs, got %q", m.Text)
		}
		if len(m.Attachments) != 2 {
			t.Fatalf("attachments = %d, want 2", len(m.Attachments))
		}
		if m.Attachments[0].URL != "https://api.twilio.com/media/photo.png" ||
			m.Attachments[0].MimeType != "image/png" ||
			m.Attachments[0].Type != plugin.AttachmentTypeImage {
			t.Errorf("unexpected attachment 0: %+v", m.Attachments[0])
		}
		if m.Attachments[1].Type != plugin.AttachmentTypeVideo {
			t.Errorf("unexpected attachment 1 type: %+v", m.Attachments[1])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for callback")
	}
}

// TestSendMessageWithMedia verifies the MediaUrl form parameter is attached
// to the first outbound request.
func TestSendMessageWithMedia(t *testing.T) {
	var mu sync.Mutex
	var forms []url.Values

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		forms = append(forms, r.PostForm)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sid":"SM1","status":"queued"}`))
	}))

	msg := plugin.OutgoingMessage{
		Text:  "see attachment",
		Media: &plugin.MediaInfo{Type: plugin.AttachmentTypeImage, URL: "https://example.com/cat.png"},
	}
	if err := a.SendMessage(context.Background(), "+15557654321", msg); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(forms) != 1 {
		t.Fatalf("requests = %d, want 1", len(forms))
	}
	if forms[0].Get("MediaUrl") != "https://example.com/cat.png" {
		t.Errorf("MediaUrl = %q", forms[0].Get("MediaUrl"))
	}
	if forms[0].Get("Body") != "see attachment" {
		t.Errorf("Body = %q", forms[0].Get("Body"))
	}
}

// TestSendMessageMediaOnly verifies sending a message with media and no text.
func TestSendMessageMediaOnly(t *testing.T) {
	var mu sync.Mutex
	var forms []url.Values

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		forms = append(forms, r.PostForm)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sid":"SM2","status":"queued"}`))
	}))

	msg := plugin.OutgoingMessage{
		Media: &plugin.MediaInfo{Type: plugin.AttachmentTypeImage, URL: "https://example.com/dog.png"},
	}
	if err := a.SendMessage(context.Background(), "+15557654321", msg); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(forms) != 1 {
		t.Fatalf("requests = %d, want 1", len(forms))
	}
	if forms[0].Get("MediaUrl") != "https://example.com/dog.png" {
		t.Errorf("MediaUrl = %q", forms[0].Get("MediaUrl"))
	}
}

// TestSendMessageMessagingServiceSid verifies TWILIO_MESSAGING_SERVICE_SID
// replaces the From number.
func TestSendMessageMessagingServiceSid(t *testing.T) {
	var mu sync.Mutex
	var forms []url.Values

	a, _ := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		forms = append(forms, r.PostForm)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sid":"SM3","status":"queued"}`))
	}))
	a.messagingServiceSID = "MG9999"

	if err := a.SendMessage(context.Background(), "+15557654321", plugin.OutgoingMessage{Text: "hi"}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if forms[0].Get("MessagingServiceSid") != "MG9999" {
		t.Errorf("MessagingServiceSid = %q", forms[0].Get("MessagingServiceSid"))
	}
	if forms[0].Get("From") != "" {
		t.Errorf("From should be absent when messaging service sid is set, got %q", forms[0].Get("From"))
	}
}

// TestRequestURLForPublicBase verifies the configured public webhook base URL
// handling (fragment stripping, query preservation).
func TestRequestURLForPublicBase(t *testing.T) {
	r := httptest.NewRequest("POST", "http://internal/twilio/incoming?foo=bar", nil)

	got := requestURLFor("https://public.example.com/twilio/incoming", r)
	if got != "https://public.example.com/twilio/incoming?foo=bar" {
		t.Errorf("requestURL = %q", got)
	}

	got = requestURLFor("https://public.example.com/twilio/incoming?fixed=1", r)
	if got != "https://public.example.com/twilio/incoming?fixed=1" {
		t.Errorf("configured query should win, got %q", got)
	}

	got = requestURLFor("https://public.example.com/twilio/incoming#frag", r)
	if got != "https://public.example.com/twilio/incoming?foo=bar" {
		t.Errorf("fragment should be stripped, got %q", got)
	}
}
