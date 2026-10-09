package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/covoyage/covo-agent/internal/cyber"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
)

func TestWebFetchEvidenceIsInstanceLocal(t *testing.T) {
	first := cyber.NewCatalog(t.TempDir())
	second := cyber.NewCatalog(t.TempDir())
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("raw evidence")), Request: req}, nil
	})
	firstTool := buildWebFetchTool(transport, newWebFetchCache(), catalogExchangeRecorder(first))
	secondTool := buildWebFetchTool(transport, newWebFetchCache(), catalogExchangeRecorder(second))
	args := map[string]any{"url": "https://example.test/same"}
	callWebFetch(t, firstTool.Func, args)
	result := callWebFetch(t, secondTool.Func, args)
	if result["cached"] != false {
		t.Fatal("second instance reused first instance evidence")
	}
	callWebFetch(t, firstTool.Func, map[string]any{"url": "https://example.test/private", "headers": map[string]string{"Authorization": "Bearer alice", "Cookie": "session=alice"}})
	firstItems, err := first.ListExchanges()
	if err != nil || len(firstItems) != 2 {
		t.Fatalf("first evidence: %+v, %v", firstItems, err)
	}
	secondItems, err := second.ListExchanges()
	if err != nil || len(secondItems) != 1 {
		t.Fatalf("second evidence: %+v, %v", secondItems, err)
	}
	if !strings.Contains(firstItems[1].ReqHeaders, "Authorization: Bearer alice") || !strings.Contains(firstItems[1].ReqHeaders, "Cookie: session=alice") {
		t.Fatalf("authentication headers were lost: %q", firstItems[1].ReqHeaders)
	}
	if _, err := first.SetAllowedHosts([]string{"example.test"}); err != nil {
		t.Fatal(err)
	}
	replayTransport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "Bearer alice" || req.Header.Get("Cookie") != "session=alice" {
			t.Fatalf("authenticated fetch replay became anonymous: %v", req.Header)
		}
		return transport.RoundTrip(req)
	})
	ctx := toolssubagent.WithSubagentRole(context.Background(), "verifier")
	if _, err := BuildExchangeReplayTool(first, replayTransport).Func(ctx, json.RawMessage(`{"id":"`+firstItems[1].ID+`"}`)); err != nil {
		t.Fatal(err)
	}
}

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
