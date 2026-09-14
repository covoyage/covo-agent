// Package sms implements the SMS platform adapter backed by Twilio.
//
// Outbound messages are delivered via the Twilio Messages API
// (https://api.twilio.com/2010-04-01/Accounts/{sid}/Messages.json) and
// inbound messages are received from a local HTTP server exposing the
// Twilio webhook, verified with the X-Twilio-Signature HMAC-SHA1 header.
package sms

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
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
	PlatformName = "sms"
	apiBase      = "https://api.twilio.com"

	defaultListenAddr   = ":8082"
	defaultIncomingPath = "/twilio/incoming"

	maxSegmentLen = 1600 // Twilio: single message body limit
	maxRetries    = 3
)

type Adapter struct {
	accountSID          string
	authToken           string
	fromNumber          string
	messagingServiceSID string // TWILIO_MESSAGING_SERVICE_SID; replaces From when set
	publicWebhookURL    string // configured public base URL for signature validation
	listenAddr          string
	incomingPath        string
	apiBase             string // overridable for tests (httptest server)
	httpClient          *http.Client
	logger              *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	server    *http.Server
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	listenAddr := strings.TrimSpace(os.Getenv("TWILIO_LISTEN_ADDR"))
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}
	incomingPath := strings.TrimSpace(os.Getenv("TWILIO_INCOMING_PATH"))
	if incomingPath == "" {
		incomingPath = defaultIncomingPath
	}

	return &Adapter{
		// TWILIO_* are the canonical names; SMS_* kept as fallback.
		accountSID:          envOr("TWILIO_ACCOUNT_SID", "SMS_ACCOUNT_SID"),
		authToken:           envOr("TWILIO_AUTH_TOKEN", "SMS_AUTH_TOKEN"),
		fromNumber:          envOr("TWILIO_FROM_NUMBER", "SMS_FROM_NUMBER"),
		messagingServiceSID: strings.TrimSpace(os.Getenv("TWILIO_MESSAGING_SERVICE_SID")),
		publicWebhookURL:    strings.TrimRight(strings.TrimSpace(os.Getenv("TWILIO_PUBLIC_WEBHOOK_URL")), "/"),
		listenAddr:          listenAddr,
		incomingPath:        incomingPath,
		apiBase:             apiBase,
		httpClient:          &http.Client{Timeout: 30 * time.Second},
		logger:              logger,
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
	if a.accountSID == "" || a.authToken == "" || a.fromNumber == "" {
		return fmt.Errorf("TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN and TWILIO_FROM_NUMBER must be set in environment")
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
		return fmt.Errorf("sms: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("sms: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc(a.incomingPath, a.handleWebhook)
	server := &http.Server{Handler: mux}

	ln, err := net.Listen("tcp", a.listenAddr)
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("sms: listen on %s: %w", a.listenAddr, err)
	}

	a.mu.Lock()
	a.server = server
	a.mu.Unlock()

	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			a.logger.Error("sms: webhook server failed", "error", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	a.logger.Info("sms: listening", "addr", a.listenAddr, "path", a.incomingPath)
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
	a.logger.Info("sms: stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	hasMedia := msg.Media != nil && msg.Media.URL != ""
	if msg.Text == "" && !hasMedia {
		return nil
	}
	if channelID == "" {
		return fmt.Errorf("sms: channel ID (destination phone number) is required")
	}

	segments := splitSMS(msg.Text, maxSegmentLen)
	// The media URL is attached to the first outbound message only.
	for i, segment := range segments {
		mediaURL := ""
		if i == 0 && hasMedia {
			mediaURL = msg.Media.URL
		}
		if err := a.sendSMS(ctx, channelID, segment, mediaURL); err != nil {
			return fmt.Errorf("sms: send segment %d/%d: %w", i+1, len(segments), err)
		}
	}
	return nil
}

// sendSMS posts one message segment to the Twilio Messages API. When
// TWILIO_MESSAGING_SERVICE_SID is configured it replaces the From number;
// mediaURL, when non-empty, is attached as a MediaUrl form parameter
// (structure aligned with openclaw extensions/sms twilio outbound).
func (a *Adapter) sendSMS(ctx context.Context, to string, body string, mediaURL string) error {
	form := url.Values{}
	form.Set("To", to)
	if a.messagingServiceSID != "" {
		form.Set("MessagingServiceSid", a.messagingServiceSID)
	} else {
		form.Set("From", a.fromNumber)
	}
	if body != "" {
		form.Set("Body", body)
	}
	if mediaURL != "" {
		form.Set("MediaUrl", mediaURL)
	}

	endpoint := fmt.Sprintf("%s/2010-04-01/Accounts/%s/Messages.json", a.apiBase, a.accountSID)

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
			lastErr = fmt.Errorf("server error (status %d): %s", resp.StatusCode, twilioErrorMessage(result.Message, data))
			continue
		}
		if resp.StatusCode != http.StatusCreated {
			// 4xx: permanent failure, no retry.
			return fmt.Errorf("twilio rejected message (status %d): %s", resp.StatusCode, twilioErrorMessage(result.Message, data))
		}

		a.logger.Info("sms: message accepted", "sid", result.SID, "status", result.Status)
		return nil
	}
	return lastErr
}

// twilioErrorMessage prefers the JSON "message" field and falls back to the
// raw body.
func twilioErrorMessage(message string, raw []byte) string {
	if message != "" {
		return message
	}
	return string(raw)
}

// splitSMS splits text into segments of at most maxLen runes. Segments carry
// no "(i/n)" ordinal suffix, matching openclaw extensions/sms behaviour.
func splitSMS(text string, maxLen int) []string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}

	segments := make([]string, 0, (len(runes)+maxLen-1)/maxLen)
	for offset := 0; offset < len(runes); offset += maxLen {
		end := offset + maxLen
		if end > len(runes) {
			end = len(runes)
		}
		segments = append(segments, string(runes[offset:end]))
	}
	return segments
}

// handleWebhook receives Twilio inbound message webhooks, verifies the
// X-Twilio-Signature header and forwards the message to the callback.
// Media-only messages (empty Body with MediaUrlN fields) are forwarded with
// the media URLs joined as the text, matching openclaw extensions/sms
// inbound handling.
func (a *Adapter) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	requestURL := requestURLFor(a.publicWebhookURL, r)
	if !a.verifyTwilioSignature(r.Header.Get("X-Twilio-Signature"), requestURL, r.PostForm) {
		a.logger.Warn("sms: webhook signature verification failed")
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	from := r.PostFormValue("From")
	body := r.PostFormValue("Body")

	// Collect inbound media entries (MediaUrl0/MediaContentType0, ...).
	var media []plugin.Attachment
	var mediaURLs []string
	for i := 0; ; i++ {
		u := strings.TrimSpace(r.PostFormValue(fmt.Sprintf("MediaUrl%d", i)))
		if u == "" {
			break
		}
		contentType := r.PostFormValue(fmt.Sprintf("MediaContentType%d", i))
		media = append(media, plugin.Attachment{
			Type:     attachmentTypeFor(contentType),
			URL:      u,
			MimeType: contentType,
		})
		mediaURLs = append(mediaURLs, u)
	}

	// Allow media-only messages: empty Body with at least one media entry.
	if from == "" || (body == "" && len(media) == 0) {
		w.WriteHeader(http.StatusOK)
		return
	}
	text := body
	if text == "" {
		text = "[media] " + strings.Join(mediaURLs, " ")
	}

	incoming := plugin.IncomingMessage{
		Platform:    PlatformName,
		ChannelID:   from,
		UserID:      from,
		UserName:    from,
		Text:        text,
		Timestamp:   time.Now(),
		Attachments: media,
		Raw: map[string]string{
			"To":         r.PostFormValue("To"),
			"From":       from,
			"Body":       body,
			"MessageSid": r.PostFormValue("MessageSid"),
		},
	}

	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()
	if callback != nil {
		callback(incoming)
	}

	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`))
}

// attachmentTypeFor maps a MIME type to a plugin attachment type.
func attachmentTypeFor(mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return plugin.AttachmentTypeImage
	case strings.HasPrefix(mimeType, "video/"):
		return plugin.AttachmentTypeVideo
	case strings.HasPrefix(mimeType, "audio/"):
		return plugin.AttachmentTypeAudio
	default:
		return plugin.AttachmentTypeFile
	}
}

// requestURLFor reconstructs the full request URL used for Twilio signature
// validation. Twilio signs the complete URL including the query string.
//
// When publicBase is configured (TWILIO_PUBLIC_WEBHOOK_URL), it is used as
// the base URL (fragment stripped, per openclaw resolveTwilioWebhookSignatureUrl)
// with the request's query string appended unless the configured URL already
// carries one. Otherwise the base is reconstructed from the request, with
// X-Forwarded-Proto / X-Forwarded-Host taking priority (TLS-terminating
// proxies), falling back to the TLS state of the request.
func requestURLFor(publicBase string, r *http.Request) string {
	if publicBase != "" {
		// Strip fragment without URL reserialization so exact bytes survive.
		base := publicBase
		if i := strings.Index(base, "#"); i != -1 {
			base = base[:i]
		}
		if strings.Contains(base, "?") || r.URL.RawQuery == "" {
			return base
		}
		return base + "?" + r.URL.RawQuery
	}

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
// Twilio's algorithm: base64(HMAC-SHA1(authToken, url + sortedParams))
// where the parameters are the POST form entries concatenated as
// key+value in lexicographic key order.
func (a *Adapter) verifyTwilioSignature(signature string, requestURL string, params url.Values) bool {
	if signature == "" || a.authToken == "" {
		return false
	}

	mac := hmac.New(sha1.New, []byte(a.authToken))
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
