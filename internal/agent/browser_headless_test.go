package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/cyber"
	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tools"
)

func TestBrowserHeadlessFromEnv(t *testing.T) {
	t.Setenv("BROWSER_HEADLESS", "")
	t.Setenv("AGENT_BROWSER_HEADED", "")
	if !browserHeadlessFromEnv() {
		t.Fatal("default should be headless")
	}

	t.Setenv("AGENT_BROWSER_HEADED", "1")
	if browserHeadlessFromEnv() {
		t.Fatal("AGENT_BROWSER_HEADED=1 should launch a visible automation window")
	}

	t.Setenv("AGENT_BROWSER_HEADED", "true")
	if browserHeadlessFromEnv() {
		t.Fatal("AGENT_BROWSER_HEADED=true should launch a visible automation window")
	}

	t.Setenv("AGENT_BROWSER_HEADED", "")
	t.Setenv("BROWSER_HEADLESS", "false")
	if browserHeadlessFromEnv() {
		t.Fatal("BROWSER_HEADLESS=false should launch a visible automation window")
	}

	t.Setenv("AGENT_BROWSER_HEADED", "1")
	t.Setenv("BROWSER_HEADLESS", "true")
	if browserHeadlessFromEnv() {
		t.Fatal("AGENT_BROWSER_HEADED should take precedence over BROWSER_HEADLESS")
	}
}

func TestCyberBrowserNetworkScopeAndFiltering(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	config := cyberBrowserNetworkConfig(catalog, slog.New(slog.NewTextHandler(io.Discard, nil)))
	item := tools.BrowserNetworkExchange{Method: "POST", URL: "https://example.test/a?%74oken=private-query&id=1", RequestHeaders: map[string]string{"Authorization": "private-header", "Content-Type": "application/json"}, Request: `{"id":9007199254740993,"nested":{"password":"private-body"}}`, Response: `{"token":"private-response","id":1}`, HeadersIncomplete: true}
	if config.AllowURL(item.URL) {
		t.Fatal("disabled intake captured browser traffic")
	}
	if _, err := catalog.SetIntake(cyber.IntakeConfig{Enabled: true, Hosts: []string{"example.test"}}); err != nil {
		t.Fatal(err)
	}
	if !config.AllowURL(item.URL) || config.AllowURL("https://outside.test/") || config.AllowURL("https://example.test.outside.test/") || config.AllowURL("https://user:pass@example.test/") {
		t.Fatal("browser capture scope was broadened")
	}
	config.OnExchange(item)
	items, err := catalog.ListExchanges()
	if err != nil || len(items) != 1 || !items[0].Redacted || !items[0].HeadersTruncated || items[0].Identity != "" || !strings.Contains(items[0].Request, "9007199254740993") {
		t.Fatalf("incorrect browser evidence: %+v err=%v", items, err)
	}
	for _, secret := range []string{"private-query", "private-header", "private-body", "private-response"} {
		if strings.Contains(items[0].URL+items[0].ReqHeaders+items[0].Request+items[0].Response, secret) {
			t.Fatal("secret persisted", secret)
		}
	}
	if _, err := catalog.SetIntake(cyber.IntakeConfig{}); err != nil {
		t.Fatal(err)
	}
	config.OnExchange(item)
	items, _ = catalog.ListExchanges()
	if len(items) != 1 {
		t.Fatal("scope revoked before persistence but evidence saved")
	}
}

func TestBrowserNetworkUnknownBodyIsIncomplete(t *testing.T) {
	exchange, err := browserNetworkEvidence(tools.BrowserNetworkExchange{URL: "https://example.test/a", Method: "POST", RequestHeaders: map[string]string{"Content-Type": "text/plain"}, Request: "private arbitrary body", Response: "private HTML or arbitrary body"})
	if err != nil || exchange.Request != "" || exchange.Response != "" || !exchange.RequestTruncated || !exchange.ResponseTruncated || !exchange.Redacted {
		t.Fatalf("unfiltered body saved: %+v err=%v", exchange, err)
	}
}

func TestBrowserNetworkRejectsAmbiguousJSON(t *testing.T) {
	for _, raw := range []string{`{"id":1,"id":2}`, `{"nested":{"id":1,"\u0069d":2}}`, `{"id":`} {
		if _, _, err := redactBrowserJSON(raw); err == nil {
			t.Fatal("ambiguous JSON accepted", raw)
		}
	}
	filtered, changed, err := redactBrowserJSON(`{"items":[{"password":123}],"id":9007199254740993}`)
	if err != nil || !changed || strings.Contains(filtered, "123") || !strings.Contains(filtered, "9007199254740993") {
		t.Fatalf("incorrect JSON filtering: %s changed=%v err=%v", filtered, changed, err)
	}
}

func TestCyberBrowserNetworkRealLedger(t *testing.T) {
	if _, err := tools.FindChrome(); err != nil {
		t.Skip("Chrome unavailable: ", err)
	}
	t.Setenv("AGENT_BROWSER_ENABLED", "false")
	t.Setenv("AGENT_BROWSER_PATH", "")
	catalog := cyber.NewCatalog(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/page" {
			writer.Header().Set("Content-Type", "text/html")
			writer.Write([]byte(`<html><head><link rel="icon" href="data:,"></head><body>test<script>fetch('/api',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({id:17,password:'private-value'})})</script></body></html>`))
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	if _, err := catalog.SetIntake(cyber.IntakeConfig{Enabled: true, Hosts: []string{parsed.Hostname()}}); err != nil {
		t.Fatal(err)
	}
	config := cyberBrowserNetworkConfig(catalog, slog.New(slog.NewTextHandler(io.Discard, nil)))
	saved := make(chan struct{}, 8)
	callback := config.OnExchange
	config.OnExchange = func(exchange tools.BrowserNetworkExchange) {
		callback(exchange)
		saved <- struct{}{}
	}
	ext := tools.NewExtension(tools.ExtensionConfig{Browser: &tools.BrowserToolConfig{Headless: true, AllowPrivate: true, Engine: "chrome", NetworkObservation: config}})
	core := agentcore.New(agentcore.Config{})
	defer core.Close()
	defer ext.Dispose()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := ext.Init(ctx, core); err != nil {
		t.Fatal(err)
	}
	for _, tool := range ext.Tools() {
		if tool.Name == "browser" {
			args, _ := json.Marshal(map[string]string{"action": "navigate", "url": server.URL + "/page"})
			if _, err := tool.Func(ctx, args); err != nil {
				t.Fatal(err)
			}
		}
	}
	for count := 0; count < 2; count++ {
		select {
		case <-saved:
		case <-ctx.Done():
			t.Fatal("browser evidence never reached catalog")
		}
	}
	items, err := catalog.ListExchanges()
	if err != nil || len(items) != 2 {
		t.Fatalf("browser evidence=%+v err=%v", items, err)
	}
	for _, item := range items {
		if item.Source != "browser" || item.BrowserSessionID == "" || item.BrowserRequestID == "" || strings.Contains(item.Request, "private-value") {
			t.Fatalf("missing provenance or leaked credentials: %+v", item)
		}
		if strings.HasSuffix(item.URL, "/api") {
			context, err := catalog.RequestContext(item.ID)
			if err != nil || context.Total != 1 || !item.Redacted || item.Request == "" {
				t.Fatalf("browser observation missing: %+v err=%v", context, err)
			}
		}
	}
}
