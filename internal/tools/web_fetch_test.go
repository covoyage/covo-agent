package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func callWebFetch(t *testing.T, fn func(context.Context, json.RawMessage) (any, error), args map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fn(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", got)
	}
	return m
}

func TestWebFetchMarkdownAndCache(t *testing.T) {
	var hits atomic.Int32
	htmlBody := `<!DOCTYPE html><html><head><title>Docs</title>
<script>alert(1)</script></head><body>
<h1>Hello</h1><p>See <a href="https://example.com/x">the link</a>.</p>
<ul><li>one</li><li>two</li></ul>
</body></html>`
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hits.Add(1)
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(htmlBody)),
			Request:    r,
		}, nil
	})
	cache := newWebFetchCache()
	tool := buildWebFetchTool(rt, cache)
	first := callWebFetch(t, tool.Func, map[string]any{"url": "https://example.com/docs"})
	if first["cached"] != false {
		t.Fatalf("first fetch should not be cached: %+v", first)
	}
	text, _ := first["text"].(string)
	if !strings.Contains(text, "# Hello") {
		t.Fatalf("expected markdown heading, got %q", text)
	}
	if !strings.Contains(text, "[the link](https://example.com/x)") {
		t.Fatalf("expected markdown link, got %q", text)
	}
	if first["title"] != "Docs" {
		t.Fatalf("title=%v", first["title"])
	}
	if strings.Contains(text, "alert") {
		t.Fatalf("script leaked into markdown: %q", text)
	}

	second := callWebFetch(t, tool.Func, map[string]any{"url": "https://example.com/docs"})
	if second["cached"] != true {
		t.Fatalf("second fetch should hit cache: %+v", second)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected 1 HTTP call, got %d", hits.Load())
	}
}

func TestWebFetchRejectsNonHTTP(t *testing.T) {
	tool := buildWebFetchTool(nil, newWebFetchCache())
	_, err := tool.Func(context.Background(), json.RawMessage(`{"url":"file:///etc/passwd"}`))
	if err == nil {
		t.Fatal("expected error for non-http url")
	}
}

func TestWebFetchMaxCharsTruncates(t *testing.T) {
	body := strings.Repeat("abcdefghij", 1000)
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})
	tool := buildWebFetchTool(rt, newWebFetchCache())
	raw, _ := json.Marshal(map[string]any{"url": "https://example.com/plain", "extract": "raw", "max_chars": 4000})
	got, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["truncated"] != true {
		t.Fatalf("expected truncated, got %+v", m)
	}
	text := m["text"].(string)
	if !strings.Contains(text, "[truncated]") {
		t.Fatalf("missing truncation marker: %q", text[:80])
	}
}

func TestWebFetchCustomHeadersSkipCache(t *testing.T) {
	var hits atomic.Int32
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hits.Add(1)
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("ok")),
			Request:    r,
		}, nil
	})
	tool := buildWebFetchTool(rt, newWebFetchCache())
	args := map[string]any{
		"url":     "https://example.com/h",
		"extract": "raw",
		"headers": map[string]string{"Authorization": "Bearer x"},
	}
	raw, _ := json.Marshal(args)
	if _, err := tool.Func(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Func(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatalf("custom-header requests must not cache, hits=%d", hits.Load())
	}
}

func TestWebFetchErrorsNotCached(t *testing.T) {
	var hits atomic.Int32
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hits.Add(1)
		return &http.Response{
			StatusCode: 500,
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("boom")),
			Request:    r,
		}, nil
	})
	tool := buildWebFetchTool(rt, newWebFetchCache())
	raw, _ := json.Marshal(map[string]any{"url": "https://example.com/err", "extract": "raw"})
	got, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["cached"] != false {
		t.Fatalf("error responses must not be cached: %+v", m)
	}
	if _, err := tool.Func(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatalf("5xx must not cache, hits=%d", hits.Load())
	}
}

func TestWebFetchBlockedChallengePage(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body:       io.NopCloser(strings.NewReader("<html><title>Just a moment...</title><body>Checking your browser before accessing example.com</body></html>")),
			Request:    r,
		}, nil
	})
	tool := buildWebFetchTool(rt, newWebFetchCache())
	m := callWebFetch(t, tool.Func, map[string]any{"url": "https://example.com/cf"})
	if m["blocked"] != true {
		t.Fatalf("expected blocked, got %+v", m)
	}
	errStr, _ := m["error"].(string)
	if !strings.Contains(errStr, "BLOCKED") {
		t.Fatalf("expected BLOCKED error, got %q", errStr)
	}
}

func TestWebFetchBlockedForbidden(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 403,
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader(strings.Repeat("nope ", 200))),
			Request:    r,
		}, nil
	})
	tool := buildWebFetchTool(rt, newWebFetchCache())
	m := callWebFetch(t, tool.Func, map[string]any{"url": "https://example.com/secret", "extract": "raw"})
	if m["blocked"] != true {
		t.Fatalf("expected 403 blocked, got %+v", m)
	}
	if m["reason"] != "forbidden" {
		t.Fatalf("reason=%v", m["reason"])
	}
}
