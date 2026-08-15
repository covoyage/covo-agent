package whatsapp

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
	"github.com/covoyage/covo-agent/internal/plugin/platforms/messageutil"
)

const (
	PlatformName = "whatsapp"
	apiBaseURL   = "https://graph.facebook.com/v22.0"
	maxRetries   = 3
)

type Adapter struct {
	phoneNumberID string
	accessToken   string
	verifyToken   string
	httpClient    *http.Client
	logger        *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	whServer  *plugin.WebhookServer
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	return &Adapter{
		phoneNumberID: strings.TrimSpace(os.Getenv("WHATSAPP_PHONE_NUMBER_ID")),
		accessToken:   strings.TrimSpace(os.Getenv("WHATSAPP_ACCESS_TOKEN")),
		verifyToken:   strings.TrimSpace(os.Getenv("WHATSAPP_VERIFY_TOKEN")),
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		logger:        logger,
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
	if a.phoneNumberID == "" || a.accessToken == "" {
		return fmt.Errorf("WHATSAPP_PHONE_NUMBER_ID and WHATSAPP_ACCESS_TOKEN must be set in environment")
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
		return fmt.Errorf("whatsapp: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("whatsapp: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	whPort := strings.TrimSpace(os.Getenv("WHATSAPP_WEBHOOK_PORT"))
	if whPort == "" {
		whPort = os.Getenv("WEBHOOK_PORT")
	}
	if whPort == "" {
		whPort = "8080"
	}
	addr := ":" + whPort
	a.whServer = plugin.NewWebhookServer(addr, a.logger)
	a.whServer.RegisterRoute("/whatsapp/webhook", a.handleWebhook)
	a.whServer.SetOnMessage(func(msg plugin.IncomingMessage) {
		a.mu.Lock()
		callback := a.onMessage
		a.mu.Unlock()
		if callback != nil {
			callback(msg)
		}
	})
	if err := a.whServer.Start(ctx); err != nil {
		a.logger.Warn("whatsapp: webhook server start failed", "error", err)
	}

	a.logger.Info("whatsapp: started")
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
	a.logger.Info("whatsapp: stopped")
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
		if err := a.sendText(ctx, channelID, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (a *Adapter) sendText(ctx context.Context, channelID, text string) error {
	url := fmt.Sprintf("%s/%s/messages", apiBaseURL, a.phoneNumberID)

	body := map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                channelID,
		"type":              "text",
		"text": map[string]string{
			"body": text,
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("whatsapp: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("whatsapp: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.accessToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("whatsapp: send: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("whatsapp: send failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (a *Adapter) handleWebhook(ctx context.Context, body []byte, headers http.Header) (*plugin.IncomingMessage, error) {
	if a.verifyToken != "" {
		qs := headers.Get("hub.verify_token")
		if qs == a.verifyToken {
			return nil, nil
		}
	}

	var payload struct {
		Object string `json:"object"`
		Entry  []struct {
			Changes []struct {
				Value struct {
					Messages []struct {
						From string `json:"from"`
						ID   string `json:"id"`
						Text struct {
							Body string `json:"body"`
						} `json:"text"`
						Type string `json:"type"`
					} `json:"messages"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, nil
	}

	if payload.Object != "whatsapp_business_account" {
		return nil, nil
	}

	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			for _, msg := range change.Value.Messages {
				if msg.Type != "text" || msg.Text.Body == "" {
					continue
				}

				return &plugin.IncomingMessage{
					Platform:  PlatformName,
					ChannelID: msg.From,
					UserID:    msg.From,
					UserName:  msg.From,
					Text:      msg.Text.Body,
					Timestamp: time.Now(),
					Raw:       body,
				}, nil
			}
		}
	}

	return nil, nil
}

func splitLongMessage(text string) []string {
	return messageutil.SplitLongText(text, 4000)
}
