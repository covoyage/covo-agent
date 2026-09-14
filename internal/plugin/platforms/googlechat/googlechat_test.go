package googlechat

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
)

// newTestServiceAccount generates an RSA keypair and returns the service
// account JSON plus the parsed private key.
func newTestServiceAccount(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	pemStr := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	sa, err := json.Marshal(map[string]string{
		"client_email": "bot@test.iam.gserviceaccount.com",
		"private_key":  string(pemStr),
	})
	if err != nil {
		t.Fatalf("marshal service account: %v", err)
	}
	return string(sa), key
}

// newTestServer spins up an httptest server that simulates both the Google
// OAuth token endpoint and the Chat API, recording the requests it receives.
type testServer struct {
	*httptest.Server

	mu         sync.Mutex
	assertions []string      // captured JWT assertions from token requests
	messages   []chatRequest // captured message POSTs
	tokenCalls int
}

type chatRequest struct {
	Space string
	Text  string
	Auth  string
}

func newTestServer(t *testing.T, messageStatus int, messageBody string) (*testServer, func() *rsa.PrivateKey) {
	t.Helper()

	ts := &testServer{}
	var saKey *rsa.PrivateKey

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse token form: %v", err)
		}
		ts.mu.Lock()
		ts.assertions = append(ts.assertions, r.FormValue("assertion"))
		ts.tokenCalls++
		ts.mu.Unlock()
		if r.FormValue("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			t.Errorf("grant_type = %q", r.FormValue("grant_type"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-123", "expires_in": 3600})
	})
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		ts.mu.Lock()
		ts.messages = append(ts.messages, chatRequest{
			Space: r.URL.Path,
			Text:  body.Text,
			Auth:  r.Header.Get("Authorization"),
		})
		ts.mu.Unlock()
		w.WriteHeader(messageStatus)
		_, _ = w.Write([]byte(messageBody))
	})

	ts.Server = httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, func() *rsa.PrivateKey { return saKey }
}

func newTestAdapter(t *testing.T) (*Adapter, *testServer) {
	t.Helper()
	ts, _ := newTestServer(t, http.StatusOK, "{}")
	saJSON, _ := newTestServiceAccount(t)
	adapter := &Adapter{
		serviceAccount: saJSON,
		httpClient:     ts.Client(),
		logger:         discardLogger(t),
		tokenURL:       ts.URL + "/token",
		apiBase:        ts.URL,
	}
	return adapter, ts
}

func discardLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestValidateRequiresServiceAccount(t *testing.T) {
	a := &Adapter{}
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "GOOGLE_CHAT_SERVICE_ACCOUNT_JSON") {
		t.Fatalf("Validate() = %v, want service account env error", err)
	}
}

func TestTokenRequestJWTFormat(t *testing.T) {
	adapter, ts := newTestAdapter(t)

	token, err := adapter.getToken(context.Background())
	if err != nil {
		t.Fatalf("getToken: %v", err)
	}
	if token != "tok-123" {
		t.Fatalf("token = %q, want tok-123", token)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.assertions) != 1 {
		t.Fatalf("token calls = %d, want 1", len(ts.assertions))
	}

	parts := strings.Split(ts.assertions[0], ".")
	if len(parts) != 3 {
		t.Fatalf("JWT has %d segments, want 3", len(parts))
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if header.Alg != "RS256" || header.Typ != "JWT" {
		t.Fatalf("header = %+v, want RS256/JWT", header)
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims struct {
		Iss   string `json:"iss"`
		Scope string `json:"scope"`
		Aud   string `json:"aud"`
		Iat   int64  `json:"iat"`
		Exp   int64  `json:"exp"`
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if claims.Iss != "bot@test.iam.gserviceaccount.com" {
		t.Errorf("iss = %q", claims.Iss)
	}
	if claims.Scope != tokenScope {
		t.Errorf("scope = %q, want %q", claims.Scope, tokenScope)
	}
	if claims.Aud != adapter.tokenURL {
		t.Errorf("aud = %q, want %q", claims.Aud, adapter.tokenURL)
	}
	if claims.Exp <= claims.Iat {
		t.Errorf("exp %d not after iat %d", claims.Exp, claims.Iat)
	}
}

func TestTokenCached(t *testing.T) {
	adapter, ts := newTestAdapter(t)
	for i := 0; i < 3; i++ {
		if _, err := adapter.getToken(context.Background()); err != nil {
			t.Fatalf("getToken call %d: %v", i, err)
		}
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.tokenCalls != 1 {
		t.Fatalf("token endpoint called %d times, want 1 (cached)", ts.tokenCalls)
	}
}

func TestSendMessageChunksAndAuth(t *testing.T) {
	adapter, ts := newTestAdapter(t)

	text := strings.Repeat("a", 9000)
	if err := adapter.SendMessage(context.Background(), "spaces/AAAA", plugin.OutgoingMessage{Text: text}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.messages) != 3 {
		t.Fatalf("chunks = %d, want 3", len(ts.messages))
	}
	var joined strings.Builder
	for _, m := range ts.messages {
		if m.Auth != "Bearer tok-123" {
			t.Errorf("Authorization = %q", m.Auth)
		}
		if m.Space != "/v1/spaces/AAAA/messages" {
			t.Errorf("path = %q", m.Space)
		}
		if len(m.Text) > maxMessageBytes {
			t.Errorf("chunk too long: %d", len(m.Text))
		}
		joined.WriteString(m.Text)
	}
	if joined.String() != text {
		t.Fatal("chunks do not preserve text")
	}
}

func TestSendMessageBareSpaceID(t *testing.T) {
	adapter, ts := newTestAdapter(t)
	if err := adapter.Send(context.Background(), "BBBB", "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.messages) != 1 || ts.messages[0].Space != "/v1/spaces/BBBB/messages" {
		t.Fatalf("messages = %+v", ts.messages)
	}
}

func TestSendMessageAPIError(t *testing.T) {
	ts, _ := newTestServer(t, http.StatusForbidden, `{"error":{"message":"denied"}}`)
	saJSON, _ := newTestServiceAccount(t)
	adapter := &Adapter{
		serviceAccount: saJSON,
		httpClient:     ts.Client(),
		logger:         discardLogger(t),
		tokenURL:       ts.URL + "/token",
		apiBase:        ts.URL,
	}
	err := adapter.SendMessage(context.Background(), "spaces/AAAA", plugin.OutgoingMessage{Text: "hi"})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("SendMessage error = %v, want 403 failure", err)
	}
}

func TestWebhookMessageEvent(t *testing.T) {
	adapter := &Adapter{webhookToken: "s3cret", logger: discardLogger(t)}

	var got plugin.IncomingMessage
	adapter.OnMessage(func(msg plugin.IncomingMessage) { got = msg })

	body := []byte(`{
		"type": "MESSAGE",
		"message": {
			"text": "hello bot",
			"sender": {"name": "users/1234", "displayName": "Alice"},
			"space": {"name": "spaces/AAAA"}
		}
	}`)

	msg, err := adapter.handleChatEvent(context.Background(), body, http.Header{"Authorization": {"Bearer s3cret"}})
	if err != nil {
		t.Fatalf("handleChatEvent: %v", err)
	}
	if msg == nil {
		t.Fatal("msg = nil, want incoming message")
	}
	if got.ChannelID != "spaces/AAAA" || got.UserID != "users/1234" || got.UserName != "Alice" || got.Text != "hello bot" {
		t.Fatalf("callback message = %+v", got)
	}
	if got.Platform != PlatformName {
		t.Errorf("platform = %q", got.Platform)
	}
}

func TestWebhookAuth(t *testing.T) {
	adapter := &Adapter{webhookToken: "s3cret", logger: discardLogger(t)}
	body := []byte(`{"type":"MESSAGE","message":{"text":"x","space":{"name":"spaces/AAAA"}}}`)

	if _, err := adapter.handleChatEvent(context.Background(), body, http.Header{}); err == nil {
		t.Fatal("missing bearer accepted")
	}
	if _, err := adapter.handleChatEvent(context.Background(), body, http.Header{"Authorization": {"Bearer wrong"}}); err == nil {
		t.Fatal("wrong bearer accepted")
	}
}

// newTestJWKS serves a JWKS document for the given RSA key(s) on an
// httptest server, so JWKS fetching never touches the real network.
func newTestJWKS(t *testing.T, kid string, keys ...*rsa.PrivateKey) string {
	t.Helper()
	doc := jwksDocument{}
	for _, key := range keys {
		doc.Keys = append(doc.Keys, jwkKey{
			Kty: "RSA",
			Kid: kid,
			Alg: "RS256",
			Use: "sig",
			N:   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
			E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// signTestJWT builds and signs an RS256 JWT with the given claims.
func signTestJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// newJWTTestAdapter returns an adapter with JWT auth enabled against a local
// JWKS endpoint.
func newJWTTestAdapter(t *testing.T, jwksURL string) *Adapter {
	t.Helper()
	return &Adapter{
		webhookAudience: "https://example.com/chat-app",
		jwksURL:         jwksURL,
		logger:          discardLogger(t),
		httpClient:      &http.Client{Timeout: 5 * time.Second},
	}
}

const testChatEventBody = `{"type":"MESSAGE","message":{"text":"hello bot","sender":{"name":"users/1234","displayName":"Alice"},"space":{"name":"spaces/AAAA"}}}`

func TestWebhookGoogleJWTAudit(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}
	jwksURL := newTestJWKS(t, "test-key", key)
	a := newJWTTestAdapter(t, jwksURL)

	var got plugin.IncomingMessage
	a.OnMessage(func(msg plugin.IncomingMessage) { got = msg })

	validClaims := func() map[string]any {
		return map[string]any{
			"iss": "chat@system.gserviceaccount.com",
			"aud": "https://example.com/chat-app",
			"iat": time.Now().Unix(),
			"exp": time.Now().Add(time.Hour).Unix(),
		}
	}

	t.Run("valid token", func(t *testing.T) {
		token := signTestJWT(t, key, "test-key", validClaims())
		msg, err := a.handleChatEvent(context.Background(), []byte(testChatEventBody),
			http.Header{"Authorization": {"Bearer " + token}})
		if err != nil {
			t.Fatalf("valid JWT rejected: %v", err)
		}
		if msg == nil || msg.Text != "hello bot" || msg.ChannelID != "spaces/AAAA" {
			t.Fatalf("message = %+v", msg)
		}
		if got.Text != "hello bot" {
			t.Fatalf("callback = %+v", got)
		}
	})

	t.Run("aud as list", func(t *testing.T) {
		claims := validClaims()
		claims["aud"] = []any{"other-aud", "https://example.com/chat-app"}
		token := signTestJWT(t, key, "test-key", claims)
		if _, err := a.handleChatEvent(context.Background(), []byte(testChatEventBody),
			http.Header{"Authorization": {"Bearer " + token}}); err != nil {
			t.Fatalf("list aud rejected: %v", err)
		}
	})

	t.Run("wrong signature", func(t *testing.T) {
		token := signTestJWT(t, otherKey, "test-key", validClaims())
		_, err := a.handleChatEvent(context.Background(), []byte(testChatEventBody),
			http.Header{"Authorization": {"Bearer " + token}})
		if err == nil {
			t.Fatal("wrong-signature token accepted")
		}
	})

	t.Run("wrong issuer", func(t *testing.T) {
		claims := validClaims()
		claims["iss"] = "evil@attacker.example"
		token := signTestJWT(t, key, "test-key", claims)
		_, err := a.handleChatEvent(context.Background(), []byte(testChatEventBody),
			http.Header{"Authorization": {"Bearer " + token}})
		if err == nil {
			t.Fatal("wrong-issuer token accepted")
		}
	})

	t.Run("expired", func(t *testing.T) {
		claims := validClaims()
		claims["exp"] = time.Now().Add(-2 * tokenExpirySkew).Unix()
		token := signTestJWT(t, key, "test-key", claims)
		_, err := a.handleChatEvent(context.Background(), []byte(testChatEventBody),
			http.Header{"Authorization": {"Bearer " + token}})
		if err == nil {
			t.Fatal("expired token accepted")
		}
	})

	t.Run("audience mismatch", func(t *testing.T) {
		claims := validClaims()
		claims["aud"] = "https://other.example/app"
		token := signTestJWT(t, key, "test-key", claims)
		_, err := a.handleChatEvent(context.Background(), []byte(testChatEventBody),
			http.Header{"Authorization": {"Bearer " + token}})
		if err == nil {
			t.Fatal("audience-mismatch token accepted")
		}
	})

	t.Run("missing token is 401 over http", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(a.handleWebhookHTTP))
		defer srv.Close()
		resp, err := http.Post(srv.URL, "application/json", strings.NewReader(testChatEventBody))
		if err != nil {
			t.Fatalf("POST failed: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("valid token accepted over http", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(a.handleWebhookHTTP))
		defer srv.Close()
		token := signTestJWT(t, key, "test-key", validClaims())
		req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(testChatEventBody))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST failed: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	})
}

func TestWebhookNonMessageEvent(t *testing.T) {
	adapter := &Adapter{logger: discardLogger(t)}
	msg, err := adapter.handleChatEvent(context.Background(), []byte(`{"type":"ADDED_TO_SPACE"}`), http.Header{})
	if err != nil || msg != nil {
		t.Fatalf("handleChatEvent = (%v, %v), want (nil, nil)", msg, err)
	}
}

func TestNormalizeSpace(t *testing.T) {
	if got := normalizeSpace("spaces/AAAA"); got != "spaces/AAAA" {
		t.Errorf("normalizeSpace full = %q", got)
	}
	if got := normalizeSpace("AAAA"); got != "spaces/AAAA" {
		t.Errorf("normalizeSpace bare = %q", got)
	}
}
