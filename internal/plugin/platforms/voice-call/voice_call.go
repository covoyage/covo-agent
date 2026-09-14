// Package voicecall implements the voice-call platform adapter backed by
// Twilio.
//
// SendMessage has "outbound voice announcement" semantics: it places a real
// phone call to channelID (the destination phone number) and reads the text
// aloud via TwiML <Say>. Each text segment (max 4096 characters, the <Say>
// limit) results in one call.
//
// A local HTTP server optionally receives Twilio status callbacks
// (CallStatus/Digits); these are logged and optionally forwarded as events.
package voicecall

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
)

const (
	PlatformName = "voice-call"
	apiBase      = "https://api.twilio.com"

	defaultListenAddr   = ":8083"
	defaultIncomingPath = "/voice-call/status"

	maxSayLen  = 4096 // TwiML <Say> text limit
	maxRetries = 3
)

type Adapter struct {
	provider     string
	accountSID   string
	authToken    string
	fromNumber   string
	listenAddr   string
	incomingPath string
	apiBase      string // overridable for tests (httptest server)
	httpClient   *http.Client
	logger       *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	server    *http.Server
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	provider := strings.TrimSpace(os.Getenv("VOICE_CALL_PROVIDER"))
	if provider == "" {
		provider = "twilio"
	}

	listenAddr := strings.TrimSpace(os.Getenv("VOICE_LISTEN_ADDR"))
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}

	return &Adapter{
		provider:   provider,
		accountSID: strings.TrimSpace(os.Getenv("TWILIO_ACCOUNT_SID")),
		authToken:  strings.TrimSpace(os.Getenv("TWILIO_AUTH_TOKEN")),
		// Twilio credentials are shared with the sms adapter; the legacy
		// VOICE_CALL_FROM_NUMBER is kept as fallback.
		fromNumber:   envOr("TWILIO_FROM_NUMBER", "VOICE_CALL_FROM_NUMBER"),
		listenAddr:   listenAddr,
		incomingPath: defaultIncomingPath,
		apiBase:      apiBase,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		logger:       logger,
	}
}

// envOr returns the first non-empty environment variable value.
func envOr(names ...string) string {
	for _, name := range names {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return ""
}

func (a *Adapter) Name() string                      { return PlatformName }
func (a *Adapter) GetName() string                   { return PlatformName }
func (a *Adapter) GetID() string                     { return PlatformName }
func (a *Adapter) Category() plugin.Category         { return plugin.CategoryPlatform }
func (a *Adapter) GetCategory() plugin.Category      { return plugin.CategoryPlatform }
func (a *Adapter) ID() string                        { return PlatformName }
func (a *Adapter) Platform() plugin.PlatformProvider { return a }

func (a *Adapter) Validate() error {
	switch a.provider {
	case "twilio":
		if a.accountSID == "" {
			return fmt.Errorf("TWILIO_ACCOUNT_SID not set")
		}
		if a.authToken == "" {
			return fmt.Errorf("TWILIO_AUTH_TOKEN not set")
		}
	default:
		return fmt.Errorf("voice-call: unsupported provider %q (only twilio is implemented)", a.provider)
	}
	return nil
}

func (a *Adapter) OnMessage(callback func(plugin.IncomingMessage)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onMessage = callback
}

func (a *Adapter) Start(ctx context.Context) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("voice-call: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("voice-call: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc(a.incomingPath, a.handleStatusCallback)
	server := &http.Server{Handler: mux}

	ln, err := net.Listen("tcp", a.listenAddr)
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("voice-call: listen on %s: %w", a.listenAddr, err)
	}

	a.mu.Lock()
	a.server = server
	a.mu.Unlock()

	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			a.logger.Error("voice-call: status callback server failed", "error", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	a.logger.Info("voice-call: adapter started", "provider", a.provider, "listen", a.listenAddr)
	return nil
}

func (a *Adapter) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.running {
		return nil
	}
	if a.cancel != nil {
		a.cancel()
	}
	if a.server != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.server.Shutdown(shutdownCtx)
		a.server = nil
	}
	a.running = false
	a.logger.Info("voice-call: adapter stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

// SendMessage places voice calls that read the message text aloud. The
// channelID is the destination phone number; text is XML-escaped into a
// TwiML <Say> verb, split into segments of at most 4096 characters, with
// one call per segment.
func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" {
		return nil
	}
	if channelID == "" {
		return fmt.Errorf("voice-call: channel ID (destination phone number) is required")
	}
	if a.provider != "twilio" {
		return fmt.Errorf("voice-call: unsupported provider %q (only twilio is implemented)", a.provider)
	}

	segments := splitText(msg.Text, maxSayLen)
	for i, segment := range segments {
		if err := a.placeCall(ctx, channelID, segment); err != nil {
			return fmt.Errorf("voice-call: call %d/%d: %w", i+1, len(segments), err)
		}
	}
	return nil
}

// placeCall posts one call to the Twilio Calls API with a <Say> TwiML.
func (a *Adapter) placeCall(ctx context.Context, to string, text string) error {
	form := url.Values{}
	form.Set("To", to)
	form.Set("From", a.fromNumber)
	form.Set("Twiml", buildTwiML(text))

	endpoint := fmt.Sprintf("%s/2010-04-01/Accounts/%s/Calls.json", a.apiBase, a.accountSID)

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth(a.accountSID, a.authToken)

		resp, err := a.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			continue
		}

		var result struct {
			SID     string `json:"sid"`
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &result)

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("server error (status %d): %s", resp.StatusCode, errorMessage(result.Message, data))
			continue
		}
		if resp.StatusCode != http.StatusCreated {
			// 4xx: permanent failure, no retry.
			return fmt.Errorf("twilio rejected call (status %d): %s", resp.StatusCode, errorMessage(result.Message, data))
		}

		a.logger.Info("voice-call: call placed", "call_sid", result.SID, "status", result.Status)
		return nil
	}
	return lastErr
}

// buildTwiML renders a <Response><Say>document with the text XML-escaped.
func buildTwiML(text string) string {
	var b strings.Builder
	b.WriteString("<Response><Say>")
	if err := xml.EscapeText(&b, []byte(text)); err != nil {
		// EscapeText on a strings.Builder never fails; fall back to raw text.
		b.WriteString(text)
	}
	b.WriteString("</Say></Response>")
	return b.String()
}

// errorMessage prefers the JSON "message" field and falls back to the raw body.
func errorMessage(message string, raw []byte) string {
	if message != "" {
		return message
	}
	return string(raw)
}

// splitText splits text into chunks of at most maxLen runes.
func splitText(text string, maxLen int) []string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(runes) > 0 {
		n := maxLen
		if len(runes) < n {
			n = len(runes)
		}
		chunks = append(chunks, string(runes[:n]))
		runes = runes[n:]
	}
	return chunks
}

// handleStatusCallback receives Twilio call status callbacks (form fields
// CallStatus/Digits). When an auth token is configured, the
// X-Twilio-Signature header is verified first (HMAC-SHA1 over the full
// request URL plus sorted POST parameters — the same algorithm Twilio uses
// for its webhooks). These are informational only: the event is logged and
// optionally forwarded to the callback with a "<call status>" text.
func (a *Adapter) handleStatusCallback(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if a.authToken != "" {
		if !verifyTwilioSignature(a.authToken, signatureRequestURL(r), r.Header.Get("X-Twilio-Signature"), r.PostForm) {
			a.logger.Warn("voice-call: status callback signature verification failed")
			http.Error(w, "invalid signature", http.StatusForbidden)
			return
		}
	}

	status := r.PostFormValue("CallStatus")
	callSID := r.PostFormValue("CallSid")
	digits := r.PostFormValue("Digits")
	a.logger.Info("voice-call: status callback", "call_sid", callSID, "status", status, "digits", digits)

	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()
	if callback != nil {
		callback(plugin.IncomingMessage{
			Platform:  PlatformName,
			ChannelID: r.PostFormValue("To"),
			UserID:    r.PostFormValue("From"),
			UserName:  r.PostFormValue("From"),
			Text:      "<call status>",
			Timestamp: time.Now(),
			Raw: map[string]string{
				"CallSid":    callSID,
				"CallStatus": status,
				"Digits":     digits,
			},
		})
	}

	w.WriteHeader(http.StatusOK)
}

// signatureRequestURL reconstructs the full request URL (including query
// string) used as the Twilio signature input. The proto is taken from
// X-Forwarded-Proto when present, otherwise from the TLS state.
func signatureRequestURL(r *http.Request) string {
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return proto + "://" + host + r.URL.RequestURI()
}

// verifyTwilioSignature validates the X-Twilio-Signature header using
// Twilio's algorithm: base64(HMAC-SHA1(authToken, url + sortedParams)) where
// the parameters are the POST form entries concatenated as key+value in
// lexicographic key order. This is a package-local copy of the Twilio
// signature algorithm (the sms package keeps its own private copy; the
// function is deliberately not shared across packages).
func verifyTwilioSignature(authToken, requestURL, signature string, params url.Values) bool {
	if signature == "" || authToken == "" {
		return false
	}

	mac := hmac.New(sha1.New, []byte(authToken))
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

	got, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	return hmac.Equal(mac.Sum(nil), got)
}
