package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/cyber"
	"github.com/covoyage/covonaut/agentcore"
	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

func BuildCSRFRetestTool(catalog *cyber.Catalog, instanceRole string, transport http.RoundTripper) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "csrf_retest",
		Description: "Fetch a fresh CSRF token from a recorded GET and submit a recorded form POST on the same origin. Tokens and HTTP bodies are omitted from results and derivative evidence. Status differences are observations, not a CSRF verdict.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"page_id":      map[string]any{"type": "string", "description": "Recorded GET exchange serving a token."},
			"submit_id":    map[string]any{"type": "string", "description": "Recorded same-origin form POST exchange."},
			"extract_from": map[string]any{"type": "string", "enum": []string{"body", "header", "cookie"}},
			"token_name":   map[string]any{"type": "string", "description": "HTML input/JSON field, response header, or Set-Cookie name."},
			"form_field":   map[string]any{"type": "string", "description": "Existing POST form field to replace with the fresh token."},
		}, "required": []string{"page_id", "submit_id", "extract_from", "token_name", "form_field"}},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if catalog == nil || transport == nil {
				return nil, fmt.Errorf("cyber catalog and safe HTTP transport are required")
			}
			if !cyber.AllowAgentTool(instanceRole, subagentRole(ctx), "csrf_retest") {
				return nil, fmt.Errorf("csrf_retest is reserved for spawned hands-on roles")
			}
			var params struct {
				PageID      string `json:"page_id"`
				SubmitID    string `json:"submit_id"`
				ExtractFrom string `json:"extract_from"`
				TokenName   string `json:"token_name"`
				FormField   string `json:"form_field"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, err
			}
			page, found, err := catalog.GetExchange(params.PageID)
			if err != nil {
				return nil, err
			}
			if !found || page.Method != http.MethodGet {
				return nil, fmt.Errorf("page_id must refer to a recorded GET")
			}
			submit, found, err := catalog.GetExchange(params.SubmitID)
			if err != nil {
				return nil, err
			}
			if !found || submit.Method != http.MethodPost {
				return nil, fmt.Errorf("submit_id must refer to a recorded POST")
			}
			for _, source := range []cyber.Exchange{page, submit} {
				if source.Redacted || source.HeadersTruncated || source.RequestTruncated ||
					strings.HasSuffix(source.ReqHeaders, "…") || strings.HasSuffix(source.Request, "…") {
					return nil, fmt.Errorf("CSRF retest requires complete, non-redacted source requests")
				}
			}
			pageURL, err := url.Parse(page.URL)
			if err != nil {
				return nil, err
			}
			submitURL, err := url.Parse(submit.URL)
			if err != nil || !sameHTTPOrigin(pageURL, submitURL) {
				return nil, fmt.Errorf("CSRF page and form must share an HTTP origin")
			}
			if err := catalog.CheckReplayTarget(page.URL); err != nil {
				return nil, err
			}
			if err := catalog.CheckReplayTarget(submit.URL); err != nil {
				return nil, err
			}
			if strings.TrimSpace(params.TokenName) == "" || strings.TrimSpace(params.FormField) == "" {
				return nil, fmt.Errorf("token_name and form_field are required")
			}
			if params.ExtractFrom != "body" && params.ExtractFrom != "header" && params.ExtractFrom != "cookie" {
				return nil, fmt.Errorf("extract_from must be body, header, or cookie")
			}
			postHeaders := make(http.Header)
			applyRecordedHeaders(postHeaders, submit.ReqHeaders)
			mediaType, _, err := mime.ParseMediaType(postHeaders.Get("Content-Type"))
			if err != nil || mediaType != "application/x-www-form-urlencoded" {
				return nil, fmt.Errorf("submit_id must have an application/x-www-form-urlencoded content type")
			}
			form, err := url.ParseQuery(submit.Request)
			if err != nil || !form.Has(params.FormField) {
				return nil, fmt.Errorf("form_field %q is missing or invalid", params.FormField)
			}
			if ctx == nil {
				ctx = context.Background()
			}
			client := &http.Client{Timeout: 20 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}}
			getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, page.URL, nil)
			if err != nil {
				return nil, err
			}
			applyRecordedHeaders(getReq.Header, page.ReqHeaders)
			if err := catalog.ConsumeReplayBudget(); err != nil {
				return nil, err
			}
			getResp, err := client.Do(getReq)
			if err != nil {
				return nil, err
			}
			getBody, readErr := io.ReadAll(io.LimitReader(getResp.Body, (128<<10)+1))
			_ = getResp.Body.Close()
			if readErr != nil {
				return nil, readErr
			}
			if len(getBody) > 128<<10 || getResp.StatusCode < 200 || getResp.StatusCode >= 300 {
				return nil, fmt.Errorf("token page was truncated or returned a non-success status")
			}
			freshJar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
			if err != nil {
				return nil, err
			}
			freshJar.SetCookies(pageURL, getResp.Cookies())
			token, err := extractCSRFToken(getBody, getResp.Header, params.ExtractFrom, params.TokenName)
			if params.ExtractFrom == "cookie" {
				token = ""
				for _, cookie := range freshJar.Cookies(submitURL) {
					if cookie.Name == params.TokenName {
						token = cookie.Value
						break
					}
				}
			}
			if err != nil || token == "" || len(token) > 4096 {
				return nil, fmt.Errorf("fresh CSRF token not found or exceeds length limit")
			}
			pageEvidence, err := catalog.RecordExchange(cyber.Exchange{
				Method: http.MethodGet, URL: page.URL, StatusCode: getResp.StatusCode,
				Response: redactedCSRFBody(getBody), Identity: page.Identity, BaselineID: page.ID, Redacted: true,
			})
			if err != nil {
				return nil, err
			}
			form.Set(params.FormField, token)
			postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, submit.URL, strings.NewReader(form.Encode()))
			if err != nil {
				return nil, err
			}
			postReq.Header = postHeaders
			postReq.Header.Del("Content-Length")
			cookies := postReq.Cookies()
			jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
			if err != nil {
				return nil, err
			}
			for _, cookie := range cookies {
				cookie.Path = "/"
			}
			jar.SetCookies(submitURL, cookies)
			jar.SetCookies(pageURL, getResp.Cookies())
			postReq.Header.Del("Cookie")
			for _, cookie := range jar.Cookies(submitURL) {
				postReq.AddCookie(cookie)
			}
			if err := catalog.ConsumeReplayBudget(); err != nil {
				return map[string]any{"page_exchange_id": pageEvidence.ID}, err
			}
			postResp, err := client.Do(postReq)
			if err != nil {
				return map[string]any{"page_exchange_id": pageEvidence.ID}, err
			}
			postBody, readErr := io.ReadAll(io.LimitReader(postResp.Body, (128<<10)+1))
			_ = postResp.Body.Close()
			if readErr != nil {
				return map[string]any{"page_exchange_id": pageEvidence.ID}, readErr
			}
			truncated := len(postBody) > 128<<10
			if truncated {
				postBody = postBody[:128<<10]
			}
			postEvidence, err := catalog.RecordExchange(cyber.Exchange{
				Method: http.MethodPost, URL: submit.URL, StatusCode: postResp.StatusCode,
				Request: "[form body redacted]", Response: redactedCSRFBody(postBody),
				Identity: submit.Identity, BaselineID: submit.ID, Redacted: true,
			})
			if err != nil {
				return map[string]any{"page_exchange_id": pageEvidence.ID}, err
			}
			return map[string]any{
				"page_exchange_id": pageEvidence.ID, "submit_exchange_id": postEvidence.ID,
				"page_status": getResp.StatusCode, "baseline_submit_status": submit.StatusCode,
				"submit_status": postResp.StatusCode, "submit_body_truncated": truncated,
				"conclusion": "undetermined; compare with a separately recorded negative control",
			}, nil
		},
	}
}

func csrfCookieApplies(cookie *http.Cookie, target, page *url.URL) bool {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return false
	}
	jar.SetCookies(page, []*http.Cookie{cookie})
	for _, candidate := range jar.Cookies(target) {
		if candidate.Name == cookie.Name && candidate.Value == cookie.Value {
			return true
		}
	}
	return false
}

func redactedCSRFBody(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("[body omitted; bytes=%d; sha256=%x]", len(body), sum)
}

func extractCSRFToken(body []byte, headers http.Header, source, name string) (string, error) {
	switch source {
	case "header":
		return headers.Get(name), nil
	case "cookie":
		for _, cookie := range (&http.Response{Header: headers}).Cookies() {
			if cookie.Name == name {
				return cookie.Value, nil
			}
		}
	case "body":
		var data map[string]json.RawMessage
		if json.Unmarshal(body, &data) == nil {
			var value string
			if json.Unmarshal(data[name], &value) == nil {
				return value, nil
			}
		}
		doc, err := html.Parse(strings.NewReader(string(body)))
		if err != nil {
			return "", err
		}
		var visit func(*html.Node) string
		visit = func(node *html.Node) string {
			if node.Type == html.ElementNode && node.Data == "input" {
				var inputName, value string
				for _, attr := range node.Attr {
					switch attr.Key {
					case "name":
						inputName = attr.Val
					case "value":
						value = attr.Val
					}
				}
				if inputName == name {
					return value
				}
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				if value := visit(child); value != "" {
					return value
				}
			}
			return ""
		}
		return visit(doc), nil
	default:
		return "", fmt.Errorf("extract_from must be body, header, or cookie")
	}
	return "", nil
}
