package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/useragent"
	"github.com/covoyage/covonaut/agentcore"
)

// BuildWebFetchTool builds the web_fetch tool. When transport is non-nil it is
// used for every request (and each redirect hop), enabling SSRF protection via
// URL validation and DNS pinning; nil keeps the plain HTTP client.
func BuildWebFetchTool(transport http.RoundTripper) *agentcore.Tool {
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	if transport != nil {
		client.Transport = transport
	}

	return &agentcore.Tool{
		Name: "web_fetch",
		Description: strings.Join([]string{
			"Fetch content from a URL and return the text content.",
			"Use this to read documentation, API responses, or any web page.",
			"Returns the raw text content (stripped of HTML tags where possible).",
			"",
			"Limits: 2MB max response, 30s timeout, 5 redirect max.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The URL to fetch (must start with http:// or https://).",
				},
				"headers": map[string]any{
					"type":        "object",
					"description": "Optional HTTP headers as key-value pairs.",
				},
			},
			"required": []string{"url"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				URL     string            `json:"url"`
				Headers map[string]string `json:"headers"`
			}
			json.Unmarshal(args, &params)

			url := strings.TrimSpace(params.URL)
			if url == "" {
				return nil, fmt.Errorf("url is required")
			}
			if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
				return nil, fmt.Errorf("url must start with http:// or https://")
			}

			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				return nil, fmt.Errorf("create request: %w", err)
			}
			req.Header.Set("User-Agent", useragent.UserAgent("covo-agent"))
			req.Header.Set("Accept", "text/html,text/plain,application/json;q=0.9")

			for k, v := range params.Headers {
				req.Header.Set(k, v)
			}

			resp, err := client.Do(req)
			if err != nil {
				return nil, fmt.Errorf("fetch: %w", err)
			}
			defer resp.Body.Close()

			// Limit to 2MB
			limited := io.LimitReader(resp.Body, 2*1024*1024)
			body, err := io.ReadAll(limited)
			if err != nil {
				return nil, fmt.Errorf("read body: %w", err)
			}

			contentType := resp.Header.Get("Content-Type")
			text := string(body)

			// Strip HTML tags for text/html content
			if strings.Contains(contentType, "text/html") || strings.HasPrefix(text, "<!DOCTYPE") || strings.HasPrefix(text, "<html") {
				text = stripHTML(text)
			}

			// Truncate for display
			truncated := false
			if len(text) > 50000 {
				text = text[:50000] + "\n... [truncated]"
				truncated = true
			}

			return map[string]any{
				"url":            url,
				"status":         resp.StatusCode,
				"content_type":   contentType,
				"content_length": len(body),
				"text":           text,
				"truncated":      truncated,
			}, nil
		},
	}
}

func stripHTML(html string) string {
	// Simple HTML tag stripping
	inTag := false
	var buf strings.Builder
	buf.Grow(len(html) / 2)

	for _, ch := range html {
		if ch == '<' {
			inTag = true
			continue
		}
		if ch == '>' {
			inTag = false
			continue
		}
		if !inTag {
			buf.WriteRune(ch)
		}
	}

	// Clean up whitespace
	result := buf.String()
	lines := strings.Split(result, "\n")
	var clean []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			clean = append(clean, line)
		}
	}

	return strings.Join(clean, "\n")
}
