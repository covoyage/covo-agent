package wecom

import (
	"bytes"
	"context"
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
)

const (
	PlatformName = "wecom"
	apiBaseURL   = "https://qyapi.weixin.qq.com/cgi-bin"
	maxRetries   = 3
)

type Adapter struct {
	webhookURL string
	corpID     string
	corpSecret string
	agentID    string
	httpClient *http.Client
	logger     *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	whServer  *plugin.WebhookServer
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	return &Adapter{
		webhookURL: strings.TrimSpace(os.Getenv("WECOM_WEBHOOK_URL")),
		corpID:     strings.TrimSpace(os.Getenv("WECOM_CORP_ID")),
		corpSecret: strings.TrimSpace(os.Getenv("WECOM_CORP_SECRET")),
		agentID:    strings.TrimSpace(os.Getenv("WECOM_AGENT_ID")),
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
	if a.webhookURL == "" && (a.corpID == "" || a.corpSecret == "") {
		return fmt.Errorf("WECOM_WEBHOOK_URL or (WECOM_CORP_ID + WECOM_CORP_SECRET) must be set in environment")
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
		return fmt.Errorf("wecom: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("wecom: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	whPort := strings.TrimSpace(os.Getenv("WECOM_WEBHOOK_PORT"))
	if whPort == "" {
		whPort = os.Getenv("WEBHOOK_PORT")
	}
	if whPort != "" {
		addr := ":" + whPort
		a.whServer = plugin.NewWebhookServer(addr, a.logger)
		a.whServer.RegisterRoute("/wecom/webhook", a.handleWebhook)
		a.whServer.SetOnMessage(func(msg plugin.IncomingMessage) {
			a.mu.Lock()
			callback := a.onMessage
			a.mu.Unlock()
			if callback != nil {
				callback(msg)
			}
		})
		if err := a.whServer.Start(ctx); err != nil {
			a.logger.Warn("wecom: webhook server start failed", "error", err)
		}
	}

	a.logger.Info("wecom: started")
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
	a.logger.Info("wecom: stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" && msg.Media == nil {
		return nil
	}

	if a.webhookURL != "" {
		return a.sendViaWebhook(ctx, msg)
	}

	if a.corpID != "" {
		return a.sendViaAPI(ctx, channelID, msg)
	}

	return fmt.Errorf("wecom: no webhook URL or corp credentials configured")
}

func (a *Adapter) sendViaWebhook(ctx context.Context, msg plugin.OutgoingMessage) error {
	chunks := splitLongMessage(msg.Text)
	for i, chunk := range chunks {
		body := map[string]any{
			"msgtype": "text",
			"text": map[string]string{
				"content": chunk,
			},
		}

		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("wecom: marshal: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", a.webhookURL, bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("wecom: create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := a.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("wecom: webhook send chunk %d/%d: %w", i+1, len(chunks), err)
		}
		resp.Body.Close()
	}
	return nil
}

func (a *Adapter) sendViaAPI(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	token, err := a.getAccessToken(ctx)
	if err != nil {
		return err
	}

	body := map[string]any{
		"touser":  channelID,
		"msgtype": "text",
		"agentid": a.agentID,
		"text": map[string]string{
			"content": msg.Text,
		},
	}

	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", apiBaseURL+"/message/send?access_token="+token, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("wecom: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("wecom: api send: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("wecom: api send failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (a *Adapter) getAccessToken(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/gettoken?corpid=%s&corpsecret=%s", apiBaseURL, a.corpID, a.corpSecret)

	resp, err := a.httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("wecom: gettoken: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("wecom: decode token: %w", err)
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("wecom: gettoken failed: %s", result.ErrMsg)
	}

	return result.AccessToken, nil
}

func (a *Adapter) handleWebhook(ctx context.Context, body []byte, headers http.Header) (*plugin.IncomingMessage, error) {
	var payload struct {
		MsgType string `json:"msgtype"`
		Text    struct {
			Content string `json:"content"`
		} `json:"text"`
		FromUserName string `json:"fromUserName"`
		ToUserName   string `json:"toUserName"`
		ChatID       string `json:"chatid"`
		ChatType     string `json:"chattype"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, nil
	}

	if payload.MsgType != "text" || payload.Text.Content == "" {
		return nil, nil
	}

	channelID := payload.ChatID
	if channelID == "" {
		channelID = payload.FromUserName
	}

	return &plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: channelID,
		UserID:    payload.FromUserName,
		UserName:  payload.FromUserName,
		Text:      payload.Text.Content,
		Timestamp: time.Now(),
		Raw:       body,
	}, nil
}

func splitLongMessage(text string) []string {
	const maxLen = 4000
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	lines := strings.Split(text, "\n")
	var current strings.Builder
	for _, line := range lines {
		if current.Len()+len(line)+1 > maxLen && current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(line)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	if len(chunks) == 0 {
		chunks = append(chunks, text[:maxLen])
	}
	return chunks
}
