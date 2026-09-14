package googlechat

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/messageutil"
)

const (
	PlatformName = "googlechat"
	tokenScope   = "https://www.googleapis.com/auth/chat.bot"

	defaultTokenURL   = "https://oauth2.googleapis.com/token"
	defaultAPIBaseURL = "https://chat.googleapis.com"

	defaultWebhookAddr = ":8092"
	webhookPath        = "/googlechat/events"

	maxMessageBytes = 4000
	tokenExpirySkew = 60 * time.Second

	// Inbound JWT verification: Google Chat signs its webhook bearer tokens
	// with the chat service account; keys come from the public JWKS endpoint.
	googleChatIssuer = "chat@system.gserviceaccount.com"
	defaultJWKSURL   = "https://www.googleapis.com/oauth2/v3/certs"
	jwksCacheTTL     = time.Hour
)

// statusError carries the HTTP status a handler error should map to.
type statusError struct {
	status int
	err    error
}

func (e *statusError) Error() string { return e.err.Error() }
func (e *statusError) Unwrap() error { return e.err }

func errUnauthorized(msg string) error {
	return &statusError{status: http.StatusUnauthorized, err: errors.New(msg)}
}

type Adapter struct {
	serviceAccount  string // service account JSON full text or path to the file
	spaceID         string // default space, used when channelID is empty
	webhookToken    string // optional static bearer token for inbound webhook auth
	webhookAudience string // expected JWT aud; enables Google signature verification
	webhookAddr     string
	httpClient      *http.Client
	logger          *slog.Logger

	// Overridable for tests (inject httptest server URLs).
	tokenURL string
	apiBase  string
	jwksURL  string

	mu         sync.Mutex
	running    bool
	cancel     context.CancelFunc
	onMessage  func(plugin.IncomingMessage)
	whListener net.Listener
	whHTTP     *http.Server

	tokMu       sync.Mutex
	accessToken string
	tokenExpiry time.Time

	jwksMu        sync.Mutex
	jwksCache     *jwksDocument
	jwksFetchedAt time.Time
	authWarnOnce  sync.Once
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	webhookAddr := strings.TrimSpace(os.Getenv("GOOGLE_CHAT_WEBHOOK_LISTEN_ADDR"))
	if webhookAddr == "" {
		webhookAddr = defaultWebhookAddr
	}

	return &Adapter{
		serviceAccount:  strings.TrimSpace(os.Getenv("GOOGLE_CHAT_SERVICE_ACCOUNT_JSON")),
		spaceID:         strings.TrimSpace(os.Getenv("GOOGLE_CHAT_SPACE_ID")),
		webhookToken:    strings.TrimSpace(os.Getenv("GOOGLE_CHAT_WEBHOOK_TOKEN")),
		webhookAudience: strings.TrimSpace(os.Getenv("GOOGLE_CHAT_AUDIENCE")),
		webhookAddr:     webhookAddr,
		httpClient:      &http.Client{Timeout: 30 * time.Second},
		logger:          logger,
		tokenURL:        defaultTokenURL,
		apiBase:         defaultAPIBaseURL,
		jwksURL:         defaultJWKSURL,
	}
}

func (a *Adapter) Name() string                      { return PlatformName }
func (a *Adapter) GetName() string                   { return PlatformName }
func (a *Adapter) GetID() string                     { return PlatformName }
func (a *Adapter) Category() plugin.Category         { return plugin.CategoryPlatform }
func (a *Adapter) GetCategory() plugin.Category      { return plugin.CategoryPlatform }
func (a *Adapter) ID() string                        { return PlatformName }
func (a *Adapter) Platform() plugin.PlatformProvider { return a }

func (a *Adapter) Validate() error {
	if a.serviceAccount == "" {
		return fmt.Errorf("GOOGLE_CHAT_SERVICE_ACCOUNT_JSON not set in environment")
	}
	if _, err := a.loadServiceAccount(); err != nil {
		return fmt.Errorf("googlechat: invalid service account: %w", err)
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
		return fmt.Errorf("googlechat: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("googlechat: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	listener, err := net.Listen("tcp", a.webhookAddr)
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("googlechat: webhook listen on %s: %w", a.webhookAddr, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(webhookPath, a.handleWebhookHTTP)
	a.whListener = listener
	a.whHTTP = &http.Server{Handler: mux}

	go func() {
		<-ctx.Done()
		_ = a.whHTTP.Close()
	}()
	go func() {
		if err := a.whHTTP.Serve(listener); err != nil && err != http.ErrServerClosed {
			a.logger.Error("googlechat: webhook server stopped", "error", err)
		}
	}()

	a.logger.Info("googlechat: adapter started", "webhook", listener.Addr().String()+webhookPath)
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
	if a.whHTTP != nil {
		_ = a.whHTTP.Close()
		a.whHTTP = nil
		a.whListener = nil
	}
	a.running = false
	a.logger.Info("googlechat: adapter stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" && msg.Media == nil {
		return nil
	}
	if msg.Media != nil {
		a.logger.Warn("googlechat: media not supported, sending text only")
	}
	if msg.Text == "" {
		return nil
	}

	space := channelID
	if space == "" {
		space = a.spaceID
	}
	if space == "" {
		return fmt.Errorf("googlechat: no channel ID and GOOGLE_CHAT_SPACE_ID not set")
	}

	chunks := messageutil.SplitLongText(msg.Text, maxMessageBytes)
	for i, chunk := range chunks {
		if err := a.sendText(ctx, space, chunk); err != nil {
			return fmt.Errorf("googlechat: send chunk %d/%d: %w", i+1, len(chunks), err)
		}
	}
	return nil
}

func (a *Adapter) sendText(ctx context.Context, space, text string) error {
	token, err := a.getToken(ctx)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	url := fmt.Sprintf("%s/v1/%s/messages", strings.TrimRight(a.apiBase, "/"), normalizeSpace(space))
	payload, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post message: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("send message failed (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

func normalizeSpace(space string) string {
	space = strings.TrimSpace(space)
	if space == "" {
		return space
	}
	if !strings.HasPrefix(space, "spaces/") {
		return "spaces/" + space
	}
	return space
}

// serviceAccountKey is the subset of a Google service account JSON we need.
type serviceAccountKey struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

func (a *Adapter) loadServiceAccount() (*serviceAccountKey, error) {
	raw := a.serviceAccount
	if !strings.HasPrefix(raw, "{") {
		data, err := os.ReadFile(raw)
		if err != nil {
			return nil, fmt.Errorf("read service account file: %w", err)
		}
		raw = string(data)
	}
	var key serviceAccountKey
	if err := json.Unmarshal([]byte(raw), &key); err != nil {
		return nil, fmt.Errorf("parse service account JSON: %w", err)
	}
	if key.ClientEmail == "" || key.PrivateKey == "" {
		return nil, fmt.Errorf("service account JSON missing client_email or private_key")
	}
	return &key, nil
}

type cachedToken struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// getToken returns a cached access token, refreshing it ahead of expiry.
func (a *Adapter) getToken(ctx context.Context) (string, error) {
	a.tokMu.Lock()
	if a.accessToken != "" && time.Now().Add(tokenExpirySkew).Before(a.tokenExpiry) {
		token := a.accessToken
		a.tokMu.Unlock()
		return token, nil
	}
	a.tokMu.Unlock()

	key, err := a.loadServiceAccount()
	if err != nil {
		return "", fmt.Errorf("load service account: %w", err)
	}

	assertion, err := signJWT(key, tokenScope, a.tokenURL)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token request failed (status %d): %s", resp.StatusCode, string(body))
	}

	var tok cachedToken
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("token response missing access_token")
	}

	expiresIn := time.Duration(tok.ExpiresIn) * time.Second
	if expiresIn <= 0 {
		expiresIn = time.Hour
	}

	a.tokMu.Lock()
	a.accessToken = tok.AccessToken
	a.tokenExpiry = time.Now().Add(expiresIn)
	a.tokMu.Unlock()

	return tok.AccessToken, nil
}

func signJWT(key *serviceAccountKey, scope, audience string) (string, error) {
	priv, err := parsePrivateKey(key.PrivateKey)
	if err != nil {
		return "", err
	}

	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims, err := json.Marshal(map[string]any{
		"iss":   key.ClientEmail,
		"scope": scope,
		"aud":   audience,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", err
	}

	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func parsePrivateKey(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		if parsed, err = x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
	}
	priv, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA")
	}
	return priv, nil
}

// handleWebhookHTTP serves the inbound Google Chat event endpoint, mapping
// auth failures to 401 and malformed payloads to 400.
func (a *Adapter) handleWebhookHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	msg, err := a.handleChatEvent(r.Context(), body, r.Header)
	if err != nil {
		status := http.StatusInternalServerError
		var se *statusError
		if errors.As(err, &se) {
			status = se.status
		}
		a.logger.Warn("googlechat: webhook request rejected", "status", status, "error", err)
		http.Error(w, "request rejected", status)
		return
	}
	if msg == nil {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// handleChatEvent authenticates and processes a Google Chat interaction event
// POST body. When GOOGLE_CHAT_AUDIENCE is configured, the request must carry a
// Google-signed RS256 JWT (iss chat@system.gserviceaccount.com, matching aud,
// unexpired); otherwise the optional static bearer token applies.
func (a *Adapter) handleChatEvent(ctx context.Context, body []byte, headers http.Header) (*plugin.IncomingMessage, error) {
	switch {
	case a.webhookAudience != "":
		if err := a.verifyGoogleChatJWT(ctx, headers.Get("Authorization")); err != nil {
			return nil, err
		}
	case a.webhookToken != "":
		auth := strings.TrimSpace(headers.Get("Authorization"))
		if auth != "Bearer "+a.webhookToken {
			return nil, errUnauthorized("googlechat: unauthorized webhook request")
		}
	default:
		a.authWarnOnce.Do(func() {
			a.logger.Warn("googlechat: inbound authentication disabled; set GOOGLE_CHAT_AUDIENCE (or GOOGLE_CHAT_WEBHOOK_TOKEN) to require signed JWT (or static bearer) auth")
		})
	}

	var event chatEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("googlechat: parse event: %w", err)
	}
	if event.Type != "MESSAGE" {
		return nil, nil
	}
	if event.Message.Text == "" {
		return nil, nil
	}

	msg := &plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: event.Message.Space.Name,
		UserID:    event.Message.Sender.Name,
		UserName:  event.Message.Sender.DisplayName,
		Text:      event.Message.Text,
		Timestamp: time.Now(),
		Raw:       event,
	}

	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()
	if callback != nil {
		callback(*msg)
	}
	return msg, nil
}

type chatEvent struct {
	Type    string     `json:"type"`
	Message chatDetail `json:"message"`
}

type chatDetail struct {
	Text   string     `json:"text"`
	Sender chatSender `json:"sender"`
	Space  chatSpace  `json:"space"`
}

type chatSender struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

type chatSpace struct {
	Name string `json:"name"`
}

// jwksDocument is a JWKS endpoint response (RFC 7517).
type jwksDocument struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// verifyGoogleChatJWT validates the Authorization bearer JWT against Google's
// published JWKS: RS256 signature, iss, aud (a string or list containing the
// configured audience) and exp.
func (a *Adapter) verifyGoogleChatJWT(ctx context.Context, authHeader string) error {
	bearer := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(authHeader), "Bearer "))
	if bearer == "" {
		return errUnauthorized("googlechat: missing bearer token")
	}

	parts := strings.Split(bearer, ".")
	if len(parts) != 3 {
		return errUnauthorized("googlechat: malformed JWT")
	}

	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return errUnauthorized("googlechat: malformed JWT header")
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return errUnauthorized("googlechat: malformed JWT header")
	}
	if header.Alg != "RS256" {
		return errUnauthorized("googlechat: unsupported JWT algorithm " + header.Alg)
	}

	var claims map[string]any
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return errUnauthorized("googlechat: malformed JWT claims")
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return errUnauthorized("googlechat: malformed JWT claims")
	}

	keys, err := a.getJWKS(ctx)
	if err != nil {
		return fmt.Errorf("googlechat: fetch JWKS: %w", err)
	}
	pub, err := selectJWK(keys, header.Kid)
	if err != nil {
		return errUnauthorized("googlechat: no matching signing key")
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return errUnauthorized("googlechat: malformed JWT signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return errUnauthorized("googlechat: invalid JWT signature")
	}

	if iss, _ := claims["iss"].(string); iss != googleChatIssuer {
		return errUnauthorized("googlechat: invalid JWT issuer")
	}
	if !audienceMatches(claims["aud"], a.webhookAudience) {
		return errUnauthorized("googlechat: JWT audience mismatch")
	}
	exp, ok := numericClaim(claims["exp"])
	if !ok {
		return errUnauthorized("googlechat: missing JWT exp")
	}
	if time.Now().Add(tokenExpirySkew).Unix() > exp {
		return errUnauthorized("googlechat: expired JWT")
	}
	return nil
}

// getJWKS returns the cached JWKS, refreshing it when the cache TTL expired.
func (a *Adapter) getJWKS(ctx context.Context) (*jwksDocument, error) {
	a.jwksMu.Lock()
	defer a.jwksMu.Unlock()
	if a.jwksCache != nil && time.Since(a.jwksFetchedAt) < jwksCacheTTL {
		return a.jwksCache, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.jwksURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS endpoint returned status %d", resp.StatusCode)
	}
	var doc jwksDocument
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse JWKS: %w", err)
	}
	if len(doc.Keys) == 0 {
		return nil, fmt.Errorf("JWKS has no keys")
	}
	a.jwksCache = &doc
	a.jwksFetchedAt = time.Now()
	return &doc, nil
}

// selectJWK picks the RSA signing key matching kid (or the only key when the
// token carries no kid) and decodes it.
func selectJWK(doc *jwksDocument, kid string) (*rsa.PublicKey, error) {
	var candidates []jwkKey
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		if kid == "" || k.Kid == kid {
			candidates = append(candidates, k)
			if kid != "" {
				break
			}
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("kid %q not found in JWKS", kid)
	}
	k := candidates[0]
	n, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decode key modulus: %w", err)
	}
	e, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decode key exponent: %w", err)
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(n),
		E: int(new(big.Int).SetBytes(e).Int64()),
	}, nil
}

// audienceMatches reports whether the JWT aud claim (string or list) contains
// the configured audience.
func audienceMatches(aud any, want string) bool {
	switch v := aud.(type) {
	case string:
		return v == want
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

// numericClaim extracts a numeric (JSON number or float64) claim value.
func numericClaim(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}
