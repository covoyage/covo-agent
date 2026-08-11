package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// DebugProxyEnabled returns true when COVO_DEBUG_PROXY=true.
func DebugProxyEnabled() bool {
	return os.Getenv("COVO_DEBUG_PROXY") == "true" || os.Getenv("COVO_DEBUG_PROXY") == "1"
}

var providerURLPatterns = []struct {
	name   string
	prefix string
}{
	{"openai", "api.openai.com"},
	{"anthropic", "api.anthropic.com"},
	{"gemini", "generativelanguage.googleapis.com"},
	{"deepseek", "api.deepseek.com"},
	{"xai", "api.x.ai"},
	{"qwen-oauth", "dashscope.aliyuncs.com"},
	{"zai", "api.z.ai"},
	{"stepfun", "api.stepfun.com"},
	{"nvidia", "integrate.api.nvidia.com"},
	{"huggingface", "api-inference.huggingface.co"},
	{"kilocode", "api.kilo.ai"},
	{"perplexity", "api.perplexity.ai"},
	{"mistral", "api.mistral.ai"},
	{"openrouter", "openrouter.ai"},
	{"xiaomi", "token-plan-cn.xiaomimimo.com"},
	{"cloudflare", "gateway.ai.cloudflare.com"},
	{"vercel", "gateway.ai.vercel.com"},
	{"minimax", "api.minimax.io"},
	{"minimax-cn", "api.minimaxi.com"},
	{"opencode-zen", "opencode.ai"},
}

func detectProvider(url string) string {
	u := strings.ToLower(url)
	for _, p := range providerURLPatterns {
		if strings.Contains(u, p.prefix) {
			return p.name
		}
	}
	return "unknown"
}

func debugCaptureDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".covo-agent", "debug")
}

type captureEntry struct {
	Timestamp       string      `json:"timestamp"`
	Provider        string      `json:"provider,omitempty"`
	Method          string      `json:"method"`
	URL             string      `json:"url"`
	StatusCode      int         `json:"status_code"`
	Duration        string      `json:"duration"`
	RequestHeaders  http.Header `json:"request_headers,omitempty"`
	RequestBody     string      `json:"request_body,omitempty"`
	ResponseHeaders http.Header `json:"response_headers,omitempty"`
	ResponseBody    string      `json:"response_body,omitempty"`
}

type captureTransport struct {
	inner http.RoundTripper
	dir   string
	seq   atomic.Int64
}

func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	seq := t.seq.Add(1)
	ts := time.Now()

	reqBody, _ := io.ReadAll(req.Body)
	req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(reqBody))

	resp, err := t.inner.RoundTrip(req)
	duration := time.Since(ts)

	if err == nil && resp != nil {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		entry := captureEntry{
			Timestamp:       ts.UTC().Format(time.RFC3339Nano),
			Provider:        detectProvider(req.URL.String()),
			Method:          req.Method,
			URL:             req.URL.String(),
			StatusCode:      resp.StatusCode,
			Duration:        duration.Round(time.Millisecond).String(),
			RequestHeaders:  req.Header,
			RequestBody:     string(reqBody),
			ResponseHeaders: resp.Header,
			ResponseBody:    string(respBody),
		}

		data, _ := json.MarshalIndent(entry, "", "  ")
		filename := fmt.Sprintf("%s_%04d.json", ts.UTC().Format("20060102_150405"), seq)
		os.WriteFile(filepath.Join(t.dir, filename), data, 0600)
	}

	return resp, err
}

func init() {
	if DebugProxyEnabled() {
		dir := debugCaptureDir()
		if dir == "" {
			return
		}
		os.MkdirAll(dir, 0700)
		http.DefaultTransport = &captureTransport{
			inner: http.DefaultTransport,
			dir:   dir,
		}
	}
}
