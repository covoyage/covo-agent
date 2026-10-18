package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/covoyage/covo-agent/internal/safego"
	builtin "github.com/covoyage/covonaut/tools"
)

type browserCapturedExchange struct {
	exchange builtin.BrowserNetworkExchange
	request  network.RequestID
	readBody bool
}

func startBrowserNetworkCapture(ctx context.Context, cfg *builtin.BrowserNetworkConfig) error {
	if cfg == nil || cfg.OnExchange == nil {
		return nil
	}
	if cfg.AllowURL == nil {
		return fmt.Errorf("browser network capture requires an explicit URL policy")
	}
	var mu sync.Mutex
	pending := make(map[network.RequestID]builtin.BrowserNetworkExchange)
	redirected := make(map[network.RequestID]bool)
	queue := make(chan browserCapturedExchange, 64)
	var dropped int
	emit := func(item browserCapturedExchange) {
		select {
		case queue <- item:
		default:
			dropped++
		}
	}
	chromedp.ListenTarget(ctx, func(event any) {
		mu.Lock()
		defer mu.Unlock()
		switch message := event.(type) {
		case *network.EventRequestWillBeSent:
			if prior, found := pending[message.RequestID]; found {
				delete(pending, message.RequestID)
				prior.ResponseIncomplete = true
				if message.RedirectResponse != nil {
					prior.StatusCode = int(message.RedirectResponse.Status)
					prior.Protocol = message.RedirectResponse.Protocol
				}
				emit(browserCapturedExchange{exchange: prior})
			}
			request := message.Request
			if request == nil || !cfg.AllowURL(request.URL) {
				return
			}
			if len(pending) >= 128 {
				dropped++
				return
			}
			if message.RedirectResponse != nil {
				redirected[message.RequestID] = true
			}
			exchange := builtin.BrowserNetworkExchange{SessionID: string(chromedp.FromContext(ctx).Target.TargetID), RequestID: string(message.RequestID), Method: request.Method, URL: request.URL, HeadersIncomplete: true, RequestHeaders: make(map[string]string)}
			headerBytes := 0
			for name, value := range request.Headers {
				text, valid := value.(string)
				if valid && headerBytes+len(name)+len(text) <= 2000 {
					exchange.RequestHeaders[name] = text
					headerBytes += len(name) + len(text)
				}
			}
			if request.HasPostData {
				exchange.RequestIncomplete = len(request.PostDataEntries) == 0
				for _, entry := range request.PostDataEntries {
					if entry == nil || len(entry.Bytes) > 6000 {
						exchange.RequestIncomplete = true
						break
					}
					data, err := base64.StdEncoding.DecodeString(entry.Bytes)
					if err != nil || len(exchange.Request)+len(data) > 4000 {
						exchange.RequestIncomplete = true
						break
					}
					exchange.Request += string(data)
				}
			}
			pending[message.RequestID] = exchange
		case *network.EventRequestWillBeSentExtraInfo:
			if !redirected[message.RequestID] {
				if exchange, found := pending[message.RequestID]; found {
					exchange.RequestHeaders = make(map[string]string)
					exchange.HeadersIncomplete = false
					total := 0
					for name, value := range message.Headers {
						text, valid := value.(string)
						if !valid || total+len(name)+len(text)+4 > 2000 {
							exchange.HeadersIncomplete = true
							continue
						}
						exchange.RequestHeaders[name] = text
						total += len(name) + len(text) + 4
					}
					pending[message.RequestID] = exchange
				}
			}
		case *network.EventResponseReceived:
			if exchange, found := pending[message.RequestID]; found && message.Response != nil {
				exchange.StatusCode = int(message.Response.Status)
				exchange.Protocol = message.Response.Protocol
				pending[message.RequestID] = exchange
			}
		case *network.EventLoadingFinished:
			delete(redirected, message.RequestID)
			if exchange, found := pending[message.RequestID]; found {
				delete(pending, message.RequestID)
				readBody := message.EncodedDataLength <= 8000 && exchange.StatusCode != 0
				exchange.ResponseIncomplete = !readBody
				emit(browserCapturedExchange{exchange: exchange, request: message.RequestID, readBody: readBody})
			}
		case *network.EventLoadingFailed:
			delete(redirected, message.RequestID)
			if exchange, found := pending[message.RequestID]; found {
				delete(pending, message.RequestID)
				exchange.ResponseIncomplete = true
				exchange.Error = message.ErrorText
				emit(browserCapturedExchange{exchange: exchange})
			}
		}
	})
	enableCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err := chromedp.Run(enableCtx, network.Enable().WithMaxTotalBufferSize(1<<20).WithMaxResourceBufferSize(64<<10).WithMaxPostDataSize(4000))
	cancel()
	if err != nil {
		return err
	}
	safego.SafeGo(func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		defer func() {
			mu.Lock()
			unfinished := len(pending) + len(queue) + dropped
			mu.Unlock()
			if unfinished > 0 && cfg.OnError != nil {
				cfg.OnError(fmt.Errorf("browser capture closed with %d unfinished or dropped records", unfinished))
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				lost := dropped
				dropped = 0
				mu.Unlock()
				if lost > 0 && cfg.OnError != nil {
					cfg.OnError(fmt.Errorf("browser network capture dropped %d records", lost))
				}
			case item := <-queue:
				if item.readBody {
					bodyCtx, bodyCancel := context.WithTimeout(ctx, 5*time.Second)
					var data []byte
					err := chromedp.Run(bodyCtx, chromedp.ActionFunc(func(actionCtx context.Context) error {
						var err error
						data, err = network.GetResponseBody(item.request).Do(actionCtx)
						return err
					}))
					bodyCancel()
					if err != nil {
						item.exchange.ResponseIncomplete = true
					} else {
						if len(data) > 8000 {
							data = data[:8000]
							item.exchange.ResponseIncomplete = true
						}
						item.exchange.Response = strings.ToValidUTF8(string(data), "")
						item.exchange.ResponseIncomplete = item.exchange.ResponseIncomplete || item.exchange.Response != string(data)
					}
				}
				if ctx.Err() == nil && cfg.AllowURL(item.exchange.URL) {
					cfg.OnExchange(item.exchange)
				}
			}
		}
	}, nil)
	return nil
}
