package feishu

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/messageutil"
)

const (
	PlatformName = "feishu"
	apiBaseURL   = "https://open.feishu.cn/open-apis"
	maxRetries   = 3
)

type Adapter struct {
	webhookURL        string
	secret            string
	appID             string
	appSecret         string
	verificationToken string
	encryptKey        string
	accessToken       string
	tokenExpiry       time.Time
	httpClient        *http.Client
	logger            *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	whServer  *plugin.WebhookServer
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	return &Adapter{
		webhookURL:        strings.TrimSpace(os.Getenv("FEISHU_WEBHOOK_URL")),
		secret:            strings.TrimSpace(os.Getenv("FEISHU_SECRET")),
		appID:             strings.TrimSpace(os.Getenv("FEISHU_APP_ID")),
		appSecret:         strings.TrimSpace(os.Getenv("FEISHU_APP_SECRET")),
		verificationToken: strings.TrimSpace(os.Getenv("FEISHU_VERIFICATION_TOKEN")),
		encryptKey:        strings.TrimSpace(os.Getenv("FEISHU_ENCRYPT_KEY")),
		httpClient:        &http.Client{Timeout: 30 * time.Second},
		logger:            logger,
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
	if a.webhookURL == "" && (a.appID == "" || a.appSecret == "") {
		return fmt.Errorf("FEISHU_WEBHOOK_URL or (FEISHU_APP_ID + FEISHU_APP_SECRET) must be set in environment")
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
		return fmt.Errorf("feishu: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("feishu: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	if a.appID != "" && a.appSecret != "" {
		if err := a.refreshToken(ctx); err != nil {
			a.logger.Warn("feishu: token refresh failed", "error", err)
		}
	}

	whPort := strings.TrimSpace(os.Getenv("FEISHU_WEBHOOK_PORT"))
	if whPort == "" {
		whPort = os.Getenv("WEBHOOK_PORT")
	}
	if whPort != "" {
		addr := ":" + whPort
		a.whServer = plugin.NewWebhookServer(addr, a.logger)
		a.whServer.RegisterRouteWithRequest("/feishu/webhook", a.handleWebhook)
		a.whServer.SetOnMessage(func(msg plugin.IncomingMessage) {
			a.mu.Lock()
			callback := a.onMessage
			a.mu.Unlock()
			if callback != nil {
				callback(msg)
			}
		})
		if err := a.whServer.Start(ctx); err != nil {
			a.logger.Warn("feishu: webhook server start failed", "error", err)
		}
	}

	a.logger.Info("feishu: started")
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
	if a.whServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.whServer.Stop(ctx)
	}
	a.running = false
	a.logger.Info("feishu: stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" && msg.Media == nil {
		return nil
	}

	for _, chunk := range splitLongMessage(msg.Text) {
		chunkMessage := msg
		chunkMessage.Text = chunk
		if a.webhookURL != "" {
			if err := a.sendViaWebhook(ctx, chunk); err != nil {
				return err
			}
			continue
		}
		if a.appID != "" {
			if err := a.sendViaAPI(ctx, channelID, chunkMessage); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("feishu: no webhook URL or app credentials configured")
	}
	return nil
}

func (a *Adapter) sendViaWebhook(ctx context.Context, text string) error {
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	sign := signFeishu(timestamp, a.secret)

	body := map[string]any{
		"timestamp": timestamp,
		"sign":      sign,
		"msg_type":  "text",
		"content": map[string]any{
			"text": text,
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("feishu: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.webhookURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("feishu: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("feishu: webhook send: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("feishu: webhook send failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (a *Adapter) sendViaAPI(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if err := a.refreshToken(ctx); err != nil {
		return err
	}

	body := map[string]any{
		"receive_id": channelID,
		"msg_type":   "text",
		"content":    fmt.Sprintf(`{"text":"%s"}`, escapeJSON(msg.Text)),
	}

	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", apiBaseURL+"/im/v1/messages?receive_id_type=chat_id", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("feishu: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.accessToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("feishu: api send: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("feishu: api send failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (a *Adapter) refreshToken(ctx context.Context) error {
	a.mu.Lock()
	if time.Now().Before(a.tokenExpiry) && a.accessToken != "" {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()

	body := map[string]string{
		"app_id":     a.appID,
		"app_secret": a.appSecret,
	}
	data, _ := json.Marshal(body)

	resp, err := a.httpClient.Post(apiBaseURL+"/auth/v3/tenant_access_token/internal", "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("feishu: get token: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("feishu: decode token: %w", err)
	}
	if result.Code != 0 {
		return fmt.Errorf("feishu: get token failed: %s", result.Msg)
	}

	a.mu.Lock()
	a.accessToken = result.TenantAccessToken
	a.tokenExpiry = time.Now().Add(time.Duration(result.Expire-60) * time.Second)
	a.mu.Unlock()

	return nil
}

func (a *Adapter) handleWebhook(ctx context.Context, body []byte, r *http.Request) (*plugin.WebhookResponse, error) {
	raw := body

	// Encrypted envelope: {"encrypt": "<base64 ciphertext>"}. AES-256-CBC
	// with key = SHA256(encrypt_key), IV = first 16 bytes of the ciphertext
	// (the scheme used by the Lark/Feishu SDK the reference implementation relies on).
	var envelope struct {
		Encrypt string `json:"encrypt"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Encrypt != "" {
		if a.encryptKey == "" {
			a.logger.Warn("feishu: received encrypted event but FEISHU_ENCRYPT_KEY is not set")
			return nil, nil
		}
		plaintext, err := decryptFeishuEnvelope(envelope.Encrypt, a.encryptKey)
		if err != nil {
			a.logger.Warn("feishu: decrypt event failed", "error", err)
			return nil, nil
		}
		raw = plaintext
	}

	var payload struct {
		Challenge string `json:"challenge"`
		Token     string `json:"token"`
		Type      string `json:"type"`
		Header    struct {
			EventType string `json:"event_type"`
			Token     string `json:"token"`
		} `json:"header"`
		Event struct {
			Sender struct {
				SenderID struct {
					OpenID string `json:"open_id"`
					UserID string `json:"user_id"`
				} `json:"sender_id"`
			} `json:"sender"`
			Message struct {
				ChatID  string `json:"chat_id"`
				Content string `json:"content"`
				MsgType string `json:"message_type"`
			} `json:"message"`
		} `json:"event"`
	}

	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, nil
	}

	// Verification token check: enforced only when configured.
	if a.verificationToken != "" {
		token := payload.Token
		if token == "" {
			token = payload.Header.Token
		}
		if token != a.verificationToken {
			a.logger.Warn("feishu: webhook token mismatch")
			return &plugin.WebhookResponse{StatusCode: http.StatusUnauthorized, Body: map[string]string{"error": "token mismatch"}}, nil
		}
	}

	if payload.Type == "url_verification" {
		// Echo the challenge back so event subscription can be enabled.
		return &plugin.WebhookResponse{Body: map[string]string{"challenge": payload.Challenge}}, nil
	}

	if payload.Header.EventType != "im.message.receive_v1" {
		return nil, nil
	}

	text := payload.Event.Message.Content
	if payload.Event.Message.MsgType == "text" {
		var content struct {
			Text string `json:"text"`
		}
		json.Unmarshal([]byte(text), &content)
		text = content.Text
	}

	if text == "" {
		return nil, nil
	}

	senderID := payload.Event.Sender.SenderID.OpenID
	if senderID == "" {
		senderID = payload.Event.Sender.SenderID.UserID
	}

	return &plugin.WebhookResponse{Message: &plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: payload.Event.Message.ChatID,
		UserID:    senderID,
		UserName:  senderID,
		Text:      text,
		Timestamp: time.Now(),
		Raw:       raw,
	}}, nil
}

// decryptFeishuEnvelope decrypts a Feishu encrypted callback body:
// base64-decode the ciphertext, use the first 16 bytes as the IV, decrypt
// with AES-256-CBC keyed by SHA256(encryptKey) and strip PKCS#7 padding.
func decryptFeishuEnvelope(ciphertextB64, encryptKey string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	if len(ciphertext) < aes.BlockSize || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("invalid ciphertext length %d", len(ciphertext))
	}

	key := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}

	iv := ciphertext[:aes.BlockSize]
	data := ciphertext[aes.BlockSize:]
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(data, data)

	if pad := int(data[len(data)-1]); pad > 0 && pad <= aes.BlockSize {
		data = data[:len(data)-pad]
	}
	return data, nil
}

func signFeishu(timestamp, secret string) string {
	msg := timestamp + "\n" + secret
	mac := hmac.New(sha256.New, []byte(msg))
	mac.Write([]byte(""))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}

func splitLongMessage(text string) []string {
	return messageutil.SplitLongText(text, 4000)
}
