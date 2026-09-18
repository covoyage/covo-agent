package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/covoyage/covo-agent/internal/useragent"
	"github.com/covoyage/covonaut/agentcore"
	"golang.org/x/net/html"
)

const (
	webFetchMaxBodyBytes    = 2 * 1024 * 1024
	webFetchDefaultChars    = 20000
	webFetchMinChars        = 4000
	webFetchMaxChars        = 100000
	webFetchCacheTTL        = 15 * time.Minute
	webFetchCacheMaxEntries = 64
)

type webFetchCacheEntry struct {
	result    map[string]any
	expiresAt time.Time
}

type webFetchCache struct {
	mu      sync.Mutex
	entries map[string]*webFetchCacheEntry
	order   []string
}

func newWebFetchCache() *webFetchCache {
	return &webFetchCache{entries: make(map[string]*webFetchCacheEntry)}
}

func (c *webFetchCache) get(key string) (map[string]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ent, ok := c.entries[key]
	if !ok || time.Now().After(ent.expiresAt) {
		if ok {
			delete(c.entries, key)
		}
		return nil, false
	}
	return cloneFetchResult(ent.result), true
}

func (c *webFetchCache) put(key string, result map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists {
		if len(c.order) >= webFetchCacheMaxEntries {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
		c.order = append(c.order, key)
	}
	c.entries[key] = &webFetchCacheEntry{
		result:    cloneFetchResult(result),
		expiresAt: time.Now().Add(webFetchCacheTTL),
	}
}

func cloneFetchResult(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

var defaultWebFetchCache = newWebFetchCache()

// BuildWebFetchTool builds the web_fetch tool. When transport is non-nil it is
// used for every request (and each redirect hop), enabling SSRF protection via
// URL validation and DNS pinning; nil keeps the plain HTTP client.
func BuildWebFetchTool(transport http.RoundTripper) *agentcore.Tool {
	return buildWebFetchTool(transport, defaultWebFetchCache)
}

func buildWebFetchTool(transport http.RoundTripper, cache *webFetchCache) *agentcore.Tool {
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
			"Fetch content from a URL with HTTP GET and return readable text.",
			"HTML is converted to markdown by default. Does not POST or PUT.",
			"Use this to read documentation, API GET responses, or any web page.",
			"Does not execute JavaScript; use browser tools for JS-heavy pages.",
			"Does not open a visible window; use open_url when the user only wants to see the site.",
			"If the result has blocked=true (anti-bot, captcha, Cloudflare, 401/403/429),",
			"do not treat text as page content — switch to browser navigate, or stop.",
			"",
			"Limits: 2MB max response, 30s timeout, 5 redirect max.",
			"Untrusted-content wrapping is applied by the agent after this tool returns.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The URL to fetch (must start with http:// or https://).",
				},
				"extract": map[string]any{
					"type":        "string",
					"description": "How to extract the body: 'markdown' (default, HTML→markdown), 'text' (stripped tags), or 'raw' (verbatim).",
					"enum":        []string{"markdown", "text", "raw"},
				},
				"max_chars": map[string]any{
					"type":        "integer",
					"description": "Maximum characters of extracted text to return (default 20000, clamped 4000–100000).",
				},
				"headers": map[string]any{
					"type":        "object",
					"description": "Optional HTTP headers as key-value pairs. Requests with custom headers are not cached.",
				},
			},
			"required": []string{"url"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				URL      string            `json:"url"`
				Extract  string            `json:"extract"`
				MaxChars int               `json:"max_chars"`
				Headers  map[string]string `json:"headers"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			rawURL := strings.TrimSpace(params.URL)
			if rawURL == "" {
				return nil, fmt.Errorf("url is required")
			}
			if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
				return nil, fmt.Errorf("url must start with http:// or https://")
			}
			parsed, err := url.Parse(rawURL)
			if err != nil || parsed.Host == "" {
				return nil, fmt.Errorf("invalid url")
			}
			parsed.Fragment = ""
			canonical := parsed.String()

			extract := strings.ToLower(strings.TrimSpace(params.Extract))
			if extract == "" {
				extract = "markdown"
			}
			switch extract {
			case "markdown", "text", "raw":
			default:
				return nil, fmt.Errorf("extract must be markdown, text, or raw")
			}

			maxChars := params.MaxChars
			if maxChars <= 0 {
				maxChars = webFetchDefaultChars
			}
			if maxChars < webFetchMinChars {
				maxChars = webFetchMinChars
			}
			if maxChars > webFetchMaxChars {
				maxChars = webFetchMaxChars
			}

			cacheable := len(params.Headers) == 0
			cacheKey := "GET|" + canonical + "|" + extract
			if cacheable && cache != nil {
				if hit, ok := cache.get(cacheKey); ok {
					hit["cached"] = true
					applyFetchMaxChars(hit, maxChars)
					return hit, nil
				}
			}

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, canonical, nil)
			if err != nil {
				return nil, fmt.Errorf("create request: %w", err)
			}
			req.Header.Set("User-Agent", useragent.UserAgent("covo-agent"))
			req.Header.Set("Accept", "text/html,text/plain,application/json;q=0.9,*/*;q=0.8")
			for k, v := range params.Headers {
				req.Header.Set(k, v)
			}

			resp, err := client.Do(req)
			if err != nil {
				return nil, fmt.Errorf("fetch: %w", err)
			}
			defer resp.Body.Close()

			limited := io.LimitReader(resp.Body, webFetchMaxBodyBytes)
			body, err := io.ReadAll(limited)
			if err != nil {
				return nil, fmt.Errorf("read body: %w", err)
			}

			contentType := resp.Header.Get("Content-Type")
			finalURL := canonical
			if resp.Request != nil && resp.Request.URL != nil {
				finalURL = resp.Request.URL.String()
			}

			rawText := string(body)
			title := ""
			text := rawText
			trimmed := strings.TrimSpace(rawText)
			isHTML := strings.Contains(contentType, "text/html") ||
				strings.HasPrefix(trimmed, "<!DOCTYPE") ||
				strings.HasPrefix(strings.ToLower(trimmed), "<html")

			switch {
			case extract == "raw":
				text = rawText
			case isHTML && extract == "markdown":
				title, text = htmlToMarkdown(rawText)
			case isHTML:
				title, text = htmlToText(rawText)
			default:
				text = collapseBlankLines(rawText)
			}

			truncated := false
			text, truncated = truncateRunesTo(text, maxChars)

			blocked, reason := classifyFetchBlock(resp.StatusCode, title, text)
			out := map[string]any{
				"url":          rawURL,
				"final_url":    finalURL,
				"status":       resp.StatusCode,
				"content_type": contentType,
				"title":        title,
				"text":         text,
				"extract":      extract,
				"truncated":    truncated,
				"cached":       false,
				"blocked":      blocked,
			}
			if blocked {
				snippet, _ := truncateRunesTo(strings.TrimSpace(text), 400)
				out["text"] = snippet
				out["truncated"] = true
				out["error"] = "BLOCKED: " + reason + "; use browser navigate or stop. Do not treat this as page content."
				out["reason"] = reason
			} else if resp.StatusCode >= 400 {
				snippet, _ := truncateRunesTo(strings.TrimSpace(text), 400)
				out["text"] = snippet
				out["ok"] = false
			}

			if cacheable && cache != nil && !blocked && resp.StatusCode >= 200 && resp.StatusCode < 400 {
				cache.put(cacheKey, out)
			}
			return out, nil
		},
	}
}

func classifyFetchBlock(status int, title, text string) (bool, string) {
	switch status {
	case 429:
		return true, "rate_limited"
	case 401, 403, 407, 451:
		return true, "forbidden"
	}
	if status >= 400 {
		return false, ""
	}
	blob := strings.ToLower(title + "\n" + text)
	strong := []string{
		"cf-browser-verification",
		"checking your browser before",
		"verify you are human",
		"just a moment...",
		"attention required",
		"enable javascript and cookies",
		"验证码",
	}
	for _, n := range strong {
		if strings.Contains(blob, n) {
			return true, "challenge_page"
		}
	}
	runes := utf8.RuneCountInString(strings.TrimSpace(text))
	if runes < 400 {
		weak := []string{"captcha", "access denied", "verify you", "cloudflare"}
		for _, n := range weak {
			if strings.Contains(blob, n) {
				return true, "challenge_page"
			}
		}
	}
	return false, ""
}

func applyFetchMaxChars(result map[string]any, maxChars int) {
	text, _ := result["text"].(string)
	trimmed, truncated := truncateRunesTo(text, maxChars)
	result["text"] = trimmed
	if truncated {
		result["truncated"] = true
	}
}

func truncateRunesTo(s string, max int) (string, bool) {
	if max <= 0 {
		return "", s != ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s, false
	}
	r := []rune(s)
	return string(r[:max]) + "\n... [truncated]", true
}

func htmlToText(src string) (title, text string) {
	title, md := htmlToMarkdown(src)
	return title, stripMarkdownLight(md)
}

func stripMarkdownLight(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	s = strings.ReplaceAll(s, "`", "")
	return collapseBlankLines(s)
}

func htmlToMarkdown(src string) (title, markdown string) {
	node, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return "", stripHTML(src)
	}
	var b strings.Builder
	title = extractHTMLTitle(node)
	renderHTMLNode(&b, node, 0)
	return title, collapseBlankLines(b.String())
}

func extractHTMLTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "title" {
		return strings.TrimSpace(textContent(n))
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if t := extractHTMLTitle(c); t != "" {
			return t
		}
	}
	return ""
}

func skipHTMLTag(name string) bool {
	switch name {
	case "script", "style", "noscript", "template", "svg", "iframe":
		return true
	}
	return false
}

func renderHTMLNode(b *strings.Builder, n *html.Node, listDepth int) {
	if n.Type == html.CommentNode {
		return
	}
	if n.Type == html.TextNode {
		t := collapseWS(n.Data)
		if t != "" {
			b.WriteString(t)
		}
		return
	}
	if n.Type != html.ElementNode {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			renderHTMLNode(b, c, listDepth)
		}
		return
	}
	if skipHTMLTag(n.Data) {
		return
	}

	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(n.Data[1] - '0')
		b.WriteString("\n")
		b.WriteString(strings.Repeat("#", level))
		b.WriteByte(' ')
		writeInlineChildren(b, n)
		b.WriteString("\n")
	case "p":
		b.WriteString("\n")
		writeInlineChildren(b, n)
		b.WriteString("\n")
	case "br":
		b.WriteByte('\n')
	case "hr":
		b.WriteString("\n---\n")
	case "li":
		b.WriteString("\n")
		b.WriteString(strings.Repeat("  ", listDepth))
		b.WriteString("- ")
		writeInlineChildren(b, n)
	case "ul", "ol":
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			renderHTMLNode(b, c, listDepth+1)
		}
		b.WriteByte('\n')
	case "pre":
		b.WriteString("\n```\n")
		b.WriteString(strings.TrimRight(textContent(n), "\n"))
		b.WriteString("\n```\n")
	case "code":
		if n.Parent != nil && n.Parent.Type == html.ElementNode && n.Parent.Data == "pre" {
			return
		}
		b.WriteByte('`')
		b.WriteString(strings.TrimSpace(textContent(n)))
		b.WriteByte('`')
	case "a":
		href := attr(n, "href")
		var inner strings.Builder
		writeInlineChildren(&inner, n)
		label := strings.TrimSpace(inner.String())
		if label == "" {
			label = href
		}
		if href != "" {
			fmt.Fprintf(b, "[%s](%s)", label, href)
		} else {
			b.WriteString(label)
		}
	case "img":
		alt := attr(n, "alt")
		src := attr(n, "src")
		if src != "" {
			fmt.Fprintf(b, "![%s](%s)", alt, src)
		}
	case "blockquote":
		var inner strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			renderHTMLNode(&inner, c, listDepth)
		}
		for _, line := range strings.Split(strings.TrimSpace(inner.String()), "\n") {
			b.WriteString("\n> ")
			b.WriteString(line)
		}
		b.WriteByte('\n')
	case "strong", "b":
		b.WriteString("**")
		writeInlineChildren(b, n)
		b.WriteString("**")
	case "em", "i":
		b.WriteByte('*')
		writeInlineChildren(b, n)
		b.WriteByte('*')
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			renderHTMLNode(b, c, listDepth)
		}
	}
}

func writeInlineChildren(b *strings.Builder, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		renderHTMLNode(b, c, 0)
	}
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	prevBlank := false
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			if prevBlank || len(out) == 0 {
				continue
			}
			prevBlank = true
			out = append(out, "")
			continue
		}
		prevBlank = false
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func stripHTML(htmlSrc string) string {
	inTag := false
	var buf strings.Builder
	buf.Grow(len(htmlSrc) / 2)

	for _, ch := range htmlSrc {
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
	return collapseBlankLines(buf.String())
}
