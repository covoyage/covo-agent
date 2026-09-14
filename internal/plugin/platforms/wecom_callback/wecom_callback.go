package wecomcallback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
)

const (
	PlatformName   = "wecom_callback"
	defaultAPIBase = "https://qyapi.weixin.qq.com/cgi-bin"
	defaultListen  = ":8091"
	// maxTextBytes caps outbound text content (WeCom message/send limit).
	maxTextBytes = 2048
	// msgDedupTTL bounds how long a MsgId is remembered for duplicate
	// suppression (WeCom retries callbacks on timeout).
	msgDedupTTL = 10 * time.Minute
)

// tokenInvalidCodes are WeCom errcodes meaning the access_token is invalid
// or expired and must be force-refreshed (mirrors hermes
// callback_adapter.py: 40014 invalid token, 40001 invalid credential,
// 42001 token expired).
var tokenInvalidCodes = map[int]bool{40014: true, 40001: true, 42001: true}

type wecomAPIError struct {
	Code int    `json:"errcode"`
	Msg  string `json:"errmsg"`
}

func (e *wecomAPIError) Error() string {
	return fmt.Sprintf("errcode=%d errmsg=%s", e.Code, e.Msg)
}

type Adapter struct {
	corpID         string
	corpSecret     string
	agentID        string
	token          string
	encodingAESKey string
	listenAddr     string
	apiBase        string
	aesKey         []byte
	httpClient     *http.Client
	logger         *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	listener  net.Listener
	server    *http.Server

	tokenMu       sync.Mutex
	tokenValue    string
	tokenExpireAt time.Time

	msgMu   sync.Mutex
	msgSeen map[string]time.Time
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	addr := strings.TrimSpace(os.Getenv("WECOM_CALLBACK_LISTEN_ADDR"))
	if addr == "" {
		addr = defaultListen
	}

	return &Adapter{
		corpID:         strings.TrimSpace(os.Getenv("WECOM_CALLBACK_CORP_ID")),
		corpSecret:     strings.TrimSpace(os.Getenv("WECOM_CALLBACK_CORP_SECRET")),
		agentID:        strings.TrimSpace(os.Getenv("WECOM_CALLBACK_AGENT_ID")),
		token:          strings.TrimSpace(os.Getenv("WECOM_CALLBACK_TOKEN")),
		encodingAESKey: strings.TrimSpace(os.Getenv("WECOM_CALLBACK_ENCODING_AES_KEY")),
		listenAddr:     addr,
		apiBase:        defaultAPIBase,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
		logger:         logger,
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
	if a.corpID == "" || a.token == "" || a.encodingAESKey == "" {
		return fmt.Errorf("WECOM_CALLBACK_CORP_ID, WECOM_CALLBACK_TOKEN and WECOM_CALLBACK_ENCODING_AES_KEY must be set in environment")
	}
	if a.corpSecret == "" {
		return fmt.Errorf("WECOM_CALLBACK_CORP_SECRET not set in environment")
	}
	if a.agentID == "" {
		return fmt.Errorf("WECOM_CALLBACK_AGENT_ID not set in environment")
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
		return fmt.Errorf("wecom_callback: validation failed: %w", err)
	}

	key, err := aesKeyFromEncodingKey(a.encodingAESKey)
	if err != nil {
		return fmt.Errorf("wecom_callback: invalid WECOM_CALLBACK_ENCODING_AES_KEY: %w", err)
	}
	a.aesKey = key

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("wecom_callback: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	if _, err := a.getAccessToken(ctx); err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("wecom_callback: get access token failed: %w", err)
	}

	handler := http.NewServeMux()
	handler.HandleFunc("/", a.serveCallback)

	ln, err := net.Listen("tcp", a.listenAddr)
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("wecom_callback: listen on %s: %w", a.listenAddr, err)
	}
	a.listener = ln
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	a.server = srv

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed && ctx.Err() == nil {
			a.logger.Warn("wecom_callback: http server error", "error", err)
		}
	}()

	a.logger.Info("wecom_callback: listening", "addr", ln.Addr().String())
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
		if err := a.server.Close(); err != nil {
			a.logger.Warn("wecom_callback: server close", "error", err)
		}
	}
	a.server = nil
	a.listener = nil
	a.running = false
	a.logger.Info("wecom_callback: stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" {
		return nil
	}

	token, err := a.getAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("wecom_callback: get access token: %w", err)
	}

	text := truncateRunes(msg.Text, maxTextBytes)
	if err := a.postMessage(ctx, token, channelID, text); err != nil {
		var apiErr *wecomAPIError
		if !errors.As(err, &apiErr) || !tokenInvalidCodes[apiErr.Code] {
			return err
		}
		// Token rejected/expired: force refresh and retry once.
		a.invalidateToken()
		token, err = a.getAccessToken(ctx)
		if err != nil {
			return fmt.Errorf("wecom_callback: refresh access token: %w", err)
		}
		if err := a.postMessage(ctx, token, channelID, text); err != nil {
			return err
		}
	}
	return nil
}

// truncateRunes caps s at maxBytes without splitting a UTF-8 rune.
func truncateRunes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	b := s[:maxBytes]
	for len(b) > 0 {
		r, size := utf8.DecodeLastRuneInString(b)
		if r != utf8.RuneError || size != 1 {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}

// seenMsgID records a MsgId and reports whether it was delivered within the
// dedup TTL already (WeCom retries callbacks on timeout → duplicates).
func (a *Adapter) seenMsgID(id string) bool {
	if id == "" {
		return false
	}
	now := time.Now()
	a.msgMu.Lock()
	defer a.msgMu.Unlock()
	if a.msgSeen == nil {
		a.msgSeen = make(map[string]time.Time)
	}
	if ts, ok := a.msgSeen[id]; ok && now.Sub(ts) < msgDedupTTL {
		return true
	}
	for k, ts := range a.msgSeen {
		if now.Sub(ts) >= msgDedupTTL {
			delete(a.msgSeen, k)
		}
	}
	a.msgSeen[id] = now
	return false
}

func (a *Adapter) postMessage(ctx context.Context, token, channelID, text string) error {
	agentID := any(a.agentID)
	if n, err := strconv.Atoi(a.agentID); err == nil {
		agentID = n
	}

	body := map[string]any{
		"touser":  channelID,
		"msgtype": "text",
		"agentid": agentID,
		"text":    map[string]string{"content": text},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("wecom_callback: marshal message: %w", err)
	}

	reqURL := fmt.Sprintf("%s/message/send?access_token=%s", a.apiBase, url.QueryEscape(token))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("wecom_callback: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("wecom_callback: message/send: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("wecom_callback: read message/send response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("wecom_callback: message/send failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result wecomAPIError
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("wecom_callback: decode message/send response: %w", err)
	}
	if result.Code != 0 {
		return fmt.Errorf("wecom_callback: message/send failed: %w", &result)
	}
	return nil
}

func (a *Adapter) getAccessToken(ctx context.Context) (string, error) {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()

	if a.tokenValue != "" && time.Now().Before(a.tokenExpireAt) {
		return a.tokenValue, nil
	}

	reqURL := fmt.Sprintf("%s/gettoken?corpid=%s&corpsecret=%s",
		a.apiBase, url.QueryEscape(a.corpID), url.QueryEscape(a.corpSecret))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("wecom_callback: create gettoken request: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("wecom_callback: gettoken: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("wecom_callback: read gettoken response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("wecom_callback: gettoken failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("wecom_callback: decode gettoken response: %w", err)
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("wecom_callback: gettoken failed: errcode=%d errmsg=%s", result.ErrCode, result.ErrMsg)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("wecom_callback: gettoken returned empty access_token")
	}

	a.tokenValue = result.AccessToken
	a.tokenExpireAt = time.Now().Add(time.Duration(result.ExpiresIn-60) * time.Second)
	return a.tokenValue, nil
}

func (a *Adapter) invalidateToken() {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()
	a.tokenValue = ""
	a.tokenExpireAt = time.Time{}
}

// serveCallback handles both the GET URL verification handshake and the
// POST encrypted message callbacks.
func (a *Adapter) serveCallback(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.serveVerify(w, r)
	case http.MethodPost:
		a.serveReceive(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// serveVerify implements WeCom's "verify URL validity" handshake: verify the
// signature of echostr, decrypt it, and echo the plaintext back verbatim.
func (a *Adapter) serveVerify(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	msgSignature := q.Get("msg_signature")
	timestamp := q.Get("timestamp")
	nonce := q.Get("nonce")
	echostr := q.Get("echostr")

	if msgSignature == "" || echostr == "" {
		http.Error(w, "missing msg_signature or echostr", http.StatusBadRequest)
		return
	}
	if !verifySignature(a.token, timestamp, nonce, echostr, msgSignature) {
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}

	plain, receiveID, err := decryptWeCom(a.aesKey, echostr)
	if err != nil {
		a.logger.Warn("wecom_callback: decrypt echostr failed", "error", err)
		http.Error(w, "decrypt failed", http.StatusBadRequest)
		return
	}
	if receiveID != a.corpID {
		a.logger.Warn("wecom_callback: echostr receive_id mismatch", "got", receiveID, "want", a.corpID)
		http.Error(w, "receive_id mismatch", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(plain)
}

// serveReceive handles encrypted inbound messages: verify the signature over
// the encrypted XML body, decrypt, parse, and dispatch text messages.
func (a *Adapter) serveReceive(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	msgSignature := q.Get("msg_signature")
	timestamp := q.Get("timestamp")
	nonce := q.Get("nonce")

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	var envelope struct {
		Encrypt string `xml:"Encrypt"`
	}
	if err := xmlUnmarshal(body, &envelope); err != nil || envelope.Encrypt == "" {
		http.Error(w, "invalid encrypted XML", http.StatusBadRequest)
		return
	}

	if !verifySignature(a.token, timestamp, nonce, envelope.Encrypt, msgSignature) {
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}

	plain, receiveID, err := decryptWeCom(a.aesKey, envelope.Encrypt)
	if err != nil {
		a.logger.Warn("wecom_callback: decrypt message failed", "error", err)
		http.Error(w, "decrypt failed", http.StatusBadRequest)
		return
	}
	if receiveID != a.corpID {
		a.logger.Warn("wecom_callback: message receive_id mismatch", "got", receiveID, "want", a.corpID)
		http.Error(w, "receive_id mismatch", http.StatusBadRequest)
		return
	}

	var msg struct {
		ToUserName   string `xml:"ToUserName"`
		FromUserName string `xml:"FromUserName"`
		CreateTime   int64  `xml:"CreateTime"`
		MsgType      string `xml:"MsgType"`
		Content      string `xml:"Content"`
		MsgId        string `xml:"MsgId"`
	}
	if err := xmlUnmarshal(plain, &msg); err != nil {
		a.logger.Warn("wecom_callback: parse message XML failed", "error", err)
		_, _ = w.Write([]byte("success"))
		return
	}

	// Duplicate delivery (WeCom retries on timeout): ack without
	// re-dispatching (mirrors hermes callback_adapter.py MsgId dedup).
	if a.seenMsgID(msg.MsgId) {
		_, _ = w.Write([]byte("success"))
		return
	}

	if msg.MsgType == "text" && msg.Content != "" {
		ts := time.Now()
		if msg.CreateTime > 0 {
			ts = time.Unix(msg.CreateTime, 0)
		}
		incoming := plugin.IncomingMessage{
			Platform:  PlatformName,
			ChannelID: msg.FromUserName,
			UserID:    msg.FromUserName,
			UserName:  msg.FromUserName,
			Text:      msg.Content,
			Timestamp: ts,
			Raw:       plain,
		}
		a.mu.Lock()
		callback := a.onMessage
		a.mu.Unlock()
		if callback != nil {
			callback(incoming)
		}
	}

	// WeCom expects the literal string "success" to acknowledge receipt.
	_, _ = w.Write([]byte("success"))
}
