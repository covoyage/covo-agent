package dingtalk

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/messageutil"
)

const (
	PlatformName = "dingtalk"
	apiBaseURL   = "https://oapi.dingtalk.com"
	maxRetries   = 3
)

type Adapter struct {
	webhookURL  string
	secret      string
	appKey      string
	appSecret   string
	accessToken string
	tokenExpiry time.Time
	httpClient  *http.Client
	logger      *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	whServer  *plugin.WebhookServer
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	return &Adapter{
		webhookURL: strings.TrimSpace(os.Getenv("DINGTALK_WEBHOOK_URL")),
		secret:     strings.TrimSpace(os.Getenv("DINGTALK_SECRET")),
		appKey:     strings.TrimSpace(os.Getenv("DINGTALK_APP_KEY")),
		appSecret:  strings.TrimSpace(os.Getenv("DINGTALK_APP_SECRET")),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     logger,
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
	if a.webhookURL == "" && (a.appKey == "" || a.appSecret == "") {
		return fmt.Errorf("DINGTALK_WEBHOOK_URL or (DINGTALK_APP_KEY + DINGTALK_APP_SECRET) must be set in environment")
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
		return fmt.Errorf("dingtalk: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("dingtalk: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	if a.appKey != "" && a.appSecret != "" {
		if err := a.refreshToken(ctx); err != nil {
			a.logger.Warn("dingtalk: token refresh failed", "error", err)
		}
	}

	whPort := strings.TrimSpace(os.Getenv("DINGTALK_WEBHOOK_PORT"))
	if whPort == "" {
		whPort = os.Getenv("WEBHOOK_PORT")
	}
	if whPort != "" {
		addr := ":" + whPort
		a.whServer = plugin.NewWebhookServer(addr, a.logger)
		a.whServer.RegisterRoute("/dingtalk/webhook", a.handleWebhook)
		a.whServer.SetOnMessage(func(msg plugin.IncomingMessage) {
			a.mu.Lock()
			callback := a.onMessage
			a.mu.Unlock()
			if callback != nil {
				callback(msg)
			}
		})
		if err := a.whServer.Start(ctx); err != nil {
			a.logger.Warn("dingtalk: webhook server start failed", "error", err)
		}
	}

	a.logger.Info("dingtalk: started")
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
	a.logger.Info("dingtalk: stopped")
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
		if a.appKey != "" {
			if err := a.sendViaAPI(ctx, channelID, chunkMessage); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("dingtalk: no webhook URL or app credentials configured")
	}
	return nil
}

func (a *Adapter) sendViaWebhook(ctx context.Context, text string) error {
	whURL := a.webhookURL

	if a.secret != "" {
		timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
		sign := signDingTalk(timestamp, a.secret)
		whURL = whURL + "&timestamp=" + timestamp + "&sign=" + sign
	}

	body := map[string]any{
		"msgtype": "text",
		"text": map[string]string{
			"content": text,
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("dingtalk: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", whURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("dingtalk: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("dingtalk: webhook send: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("dingtalk: webhook send failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (a *Adapter) sendViaAPI(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if err := a.refreshToken(ctx); err != nil {
		return err
	}

	body := map[string]any{
		"msgtype": "text",
		"text": map[string]string{
			"content": msg.Text,
		},
	}
	if channelID != "" {
		body["chatid"] = channelID
	}

	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", apiBaseURL+"/robot/send?access_token="+a.accessToken, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("dingtalk: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("dingtalk: api send: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("dingtalk: api send failed (status %d): %s", resp.StatusCode, string(respBody))
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

	resp, err := a.httpClient.Get(fmt.Sprintf("%s/gettoken?appkey=%s&appsecret=%s", apiBaseURL, a.appKey, a.appSecret))
	if err != nil {
		return fmt.Errorf("dingtalk: gettoken: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("dingtalk: decode token: %w", err)
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("dingtalk: gettoken failed: %s", result.ErrMsg)
	}

	a.mu.Lock()
	a.accessToken = result.AccessToken
	a.tokenExpiry = time.Now().Add(time.Duration(result.ExpiresIn-60) * time.Second)
	a.mu.Unlock()

	return nil
}

func (a *Adapter) handleWebhook(ctx context.Context, body []byte, headers http.Header) (*plugin.IncomingMessage, error) {
	var payload struct {
		MsgType string `json:"msgtype"`
		Text    struct {
			Content string `json:"content"`
		} `json:"text"`
		SenderID   string `json:"senderId"`
		SenderNick string `json:"senderNick"`
		ChatID     string `json:"chatid"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, nil
	}

	if payload.MsgType != "text" || payload.Text.Content == "" {
		return nil, nil
	}

	return &plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: payload.ChatID,
		UserID:    payload.SenderID,
		UserName:  payload.SenderNick,
		Text:      payload.Text.Content,
		Timestamp: time.Now(),
		Raw:       body,
	}, nil
}

func signDingTalk(timestamp, secret string) string {
	msg := timestamp + "\n" + secret
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return url.QueryEscape(sign)
}

func splitLongMessage(text string) []string {
	return messageutil.SplitLongText(text, 4000)
}
