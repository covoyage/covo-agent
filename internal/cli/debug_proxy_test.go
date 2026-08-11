package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDebugProxyEnabled(t *testing.T) {
	tests := []struct {
		val  string
		want bool
	}{
		{"true", true},
		{"1", true},
		{"false", false},
		{"", false},
		{"0", false},
	}
	for _, tt := range tests {
		os.Setenv("COVO_DEBUG_PROXY", tt.val)
		got := DebugProxyEnabled()
		if got != tt.want {
			t.Errorf("DebugProxyEnabled with COVO_DEBUG_PROXY=%q = %v, want %v", tt.val, got, tt.want)
		}
	}
}

func TestDetectProvider(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://api.openai.com/v1/chat", "openai"},
		{"https://api.anthropic.com/v1/messages", "anthropic"},
		{"https://generativelanguage.googleapis.com/v1/models", "gemini"},
		{"https://api.deepseek.com/chat", "deepseek"},
		{"https://api.mistral.ai/v1", "mistral"},
		{"https://unknown.example.com/v1", "unknown"},
	}
	for _, tt := range tests {
		got := detectProvider(tt.url)
		if got != tt.want {
			t.Errorf("detectProvider(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestCaptureTransportRoundTrip(t *testing.T) {
	dir := t.TempDir()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response body"))
		if string(body) != "request body" {
			t.Errorf("unexpected request body: %q", string(body))
		}
	}))
	defer ts.Close()

	transport := &captureTransport{
		inner: http.DefaultTransport,
		dir:   dir,
	}

	reqBody := bytes.NewReader([]byte("request body"))
	req, _ := http.NewRequest("POST", ts.URL, reqBody)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Verify the capture file was written
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 capture file, got %d", len(entries))
	}

	data, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if !bytes.Contains(data, []byte("request body")) {
		t.Error("capture file missing request body")
	}
	if !bytes.Contains(data, []byte("response body")) {
		t.Error("capture file missing response body")
	}
	if !bytes.Contains(data, []byte(ts.URL)) {
		t.Error("capture file missing URL")
	}
}
