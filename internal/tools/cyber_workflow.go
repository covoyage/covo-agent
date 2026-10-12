package tools

import (
	"context"
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
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
	"github.com/covoyage/covonaut/agentcore"
	"golang.org/x/net/publicsuffix"
)

type webWorkflowToken struct {
	Slot string `json:"slot"`
	From string `json:"from"`
	Name string `json:"name"`
}

type webWorkflowStep struct {
	ID             string            `json:"id"`
	ExpectedStatus int               `json:"expected_status"`
	Extract        *webWorkflowToken `json:"extract"`
	Bind           *webWorkflowToken `json:"bind"`
}

func BuildWebWorkflowTool(catalog *cyber.Catalog, instanceRole string, transport http.RoundTripper) *agentcore.Tool {
	tokenSchema := map[string]any{"type": "object", "properties": map[string]any{
		"slot": map[string]any{"type": "string"}, "from": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"},
	}, "required": []string{"slot", "from", "name"}}
	return &agentcore.Tool{
		Name:        "web_workflow",
		Description: "Run 2-6 explicitly selected same-origin, same-identity exchanges with a private cookie jar and in-memory token slots. Extract from body/header/cookie; bind into an existing form/JSON scalar or a dedicated CSRF header. Preflights all steps, checks scope/budget before each send, stops on unexpected/non-2xx status, and saves only redacted evidence summaries. Non-GET steps require acknowledge_state_change=true. No scripts, login automation, redirects, identity changes, or automatic intake.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"acknowledge_state_change": map[string]any{"type": "boolean"},
			"steps": map[string]any{"type": "array", "minItems": 2, "maxItems": 6, "items": map[string]any{"type": "object", "properties": map[string]any{
				"id": map[string]any{"type": "string"}, "expected_status": map[string]any{"type": "integer", "minimum": 200, "maximum": 299}, "extract": tokenSchema, "bind": tokenSchema,
			}, "required": []string{"id"}}},
		}, "required": []string{"steps"}},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if catalog == nil || transport == nil || !cyber.AllowAgentTool(instanceRole, subagentRole(ctx), "web_workflow") {
				return nil, fmt.Errorf("web_workflow requires a catalog, safe transport and spawned hands-on role")
			}
			if _, automatic := toolssubagent.IntakeScopeFromContext(ctx); automatic {
				return nil, fmt.Errorf("automatic intake cannot execute workflows")
			}
			var params struct {
				Steps                  []webWorkflowStep `json:"steps"`
				AcknowledgeStateChange bool              `json:"acknowledge_state_change"`
			}
			if err := decodeCyberValidationArgs(args, &params); err != nil {
				return nil, err
			}
			if len(params.Steps) < 2 || len(params.Steps) > 6 {
				return nil, fmt.Errorf("select 2-6 workflow steps")
			}
			sources := make([]cyber.Exchange, 0, len(params.Steps))
			slots := map[string]bool{}
			var origin *url.URL
			var firstHeaders http.Header
			for _, step := range params.Steps {
				ex, found, err := catalog.GetExchange(step.ID)
				if err != nil || !found {
					return nil, fmt.Errorf("workflow source unavailable")
				}
				parsed, err := url.Parse(ex.URL)
				if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || ex.Identity == "" {
					return nil, fmt.Errorf("workflow requires valid HTTP sources with explicit identity labels")
				}
				if ex.Redacted || ex.HeadersTruncated || ex.RequestTruncated || strings.HasSuffix(ex.Request, "\u2026") || strings.HasSuffix(ex.ReqHeaders, "\u2026") {
					return nil, fmt.Errorf("workflow requires complete unredacted requests")
				}
				if ex.Method != "GET" && ex.Method != "POST" && ex.Method != "PUT" && ex.Method != "PATCH" {
					return nil, fmt.Errorf("workflow supports GET/POST/PUT/PATCH only")
				}
				if ex.Method == "GET" && ex.Request != "" || ex.Method != "GET" && !params.AcknowledgeStateChange {
					return nil, fmt.Errorf("GET bodies are unsupported; writes require acknowledge_state_change=true")
				}
				if err := catalog.CheckReplayTarget(ex.URL); err != nil {
					return nil, err
				}
				headers := make(http.Header)
				applyRecordedHeaders(headers, ex.ReqHeaders)
				if origin == nil {
					origin, firstHeaders = parsed, headers
				} else if !sameHTTPOrigin(origin, parsed) || ex.Identity != sources[0].Identity || !workflowSessionHeadersEqual(firstHeaders, headers) {
					return nil, fmt.Errorf("workflow cannot change origin, identity label or non-cookie session headers")
				}
				if step.ExpectedStatus != 0 && (step.ExpectedStatus < 200 || step.ExpectedStatus >= 300) {
					return nil, fmt.Errorf("expected_status must be 2xx")
				}
				if step.Bind != nil {
					if !slots[step.Bind.Slot] {
						return nil, fmt.Errorf("token binding references an unavailable slot")
					}
					if _, err := workflowBind(ex, headers, step.Bind, "covo_token_preflight"); err != nil {
						return nil, err
					}
				}
				if step.Extract != nil {
					binding := step.Extract
					if !workflowTokenName(binding.Slot) || slots[binding.Slot] || binding.Name == "" || len(binding.Name) > 128 || (binding.From != "body" && binding.From != "header" && binding.From != "cookie") {
						return nil, fmt.Errorf("invalid or duplicate extraction slot")
					}
					slots[binding.Slot] = true
				}
				sources = append(sources, ex)
			}
			jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
			if err != nil {
				return nil, err
			}
			initial := (&http.Request{Header: firstHeaders}).Cookies()
			for _, cookie := range initial {
				cookie.Path = "/"
			}
			jar.SetCookies(origin, initial)
			client := &http.Client{Transport: transport, Jar: jar, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			tokens := map[string]string{}
			results := []map[string]any{}
			stopReason := ""
			for position, step := range params.Steps {
				if err := ctx.Err(); err != nil {
					return map[string]any{"steps": results, "complete": false}, err
				}
				ex := sources[position]
				headers := make(http.Header)
				applyRecordedHeaders(headers, ex.ReqHeaders)
				headers.Del("Cookie")
				headers.Del("Content-Length")
				body := ex.Request
				if step.Bind != nil {
					body, err = workflowBind(ex, headers, step.Bind, tokens[step.Bind.Slot])
					if err != nil {
						stopReason = "token_binding_failed"
						break
					}
				}
				request, err := http.NewRequestWithContext(ctx, ex.Method, ex.URL, strings.NewReader(body))
				if err != nil {
					return nil, err
				}
				request.Header = headers
				if err := catalog.CheckReplayTarget(ex.URL); err != nil {
					stopReason = "scope_rejected"
					break
				}
				if err := catalog.ConsumeReplayBudget(); err != nil {
					stopReason = "budget_exhausted"
					break
				}
				response, err := client.Do(request)
				if err != nil {
					stopReason = "request_failed"
					break
				}
				responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, (128<<10)+1))
				response.Body.Close()
				truncated := len(responseBody) > 128<<10 || readErr != nil
				if len(responseBody) > 128<<10 {
					responseBody = responseBody[:128<<10]
				}
				evidence, err := catalog.RecordExchange(cyber.Exchange{Method: ex.Method, URL: ex.URL, Identity: ex.Identity, Role: ex.Role, Tenant: ex.Tenant, Resource: ex.Resource, Owner: ex.Owner, BaselineID: ex.ID,
					Request: "[workflow body omitted]", Response: redactedCSRFBody(responseBody), Redacted: true, ResponseTruncated: truncated, StatusCode: response.StatusCode})
				if err != nil {
					return map[string]any{"steps": results, "complete": false}, err
				}
				results = append(results, map[string]any{"source_exchange_id": ex.ID, "exchange_id": evidence.ID, "status": response.StatusCode, "response_truncated": truncated})
				if truncated || response.StatusCode < 200 || response.StatusCode >= 300 || step.ExpectedStatus != 0 && step.ExpectedStatus != response.StatusCode {
					stopReason = "unexpected_or_incomplete_response"
					break
				}
				if step.Extract != nil {
					binding := step.Extract
					token, tokenErr := extractWorkflowToken(responseBody, response.Header, binding.From, binding.Name)
					if binding.From == "cookie" {
						token = ""
						for _, cookie := range jar.Cookies(request.URL) {
							if cookie.Name == binding.Name {
								token = cookie.Value
							}
						}
					}
					if tokenErr != nil || token == "" || len(token) > 4096 || strings.ContainsAny(token, "\r\n") {
						stopReason = "token_unavailable"
						break
					}
					tokens[binding.Slot] = token
				}
			}
			return map[string]any{"steps": results, "complete": stopReason == "" && len(results) == len(sources), "stop_reason": stopReason, "conclusion": "undetermined; workflow completion is not a vulnerability or permission verdict"}, ctx.Err()
		},
	}
}

func workflowSessionHeadersEqual(first, second http.Header) bool {
	copyHeaders := func(headers http.Header) string {
		copy := headers.Clone()
		for _, name := range []string{"Content-Type", "Content-Length"} {
			copy.Del(name)
		}
		return formatHeaders(copy)
	}
	return copyHeaders(first) == copyHeaders(second)
}

func extractWorkflowToken(body []byte, headers http.Header, source, name string) (string, error) {
	if source == "body" && json.Valid(body) {
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		nodes := 0
		if err := validateProbeJSON(decoder, 0, &nodes); err != nil {
			return "", err
		}
	}
	return extractCSRFToken(body, headers, source, name)
}

func workflowTokenName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, char := range name {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-') {
			return false
		}
	}
	return true
}

func workflowBind(ex cyber.Exchange, headers http.Header, binding *webWorkflowToken, token string) (string, error) {
	if !workflowTokenName(binding.Slot) || binding.Name == "" || len(binding.Name) > 512 || token == "" || len(token) > 4096 || strings.ContainsAny(token, "\r\n") {
		return "", fmt.Errorf("invalid token binding")
	}
	if binding.From == "header" {
		name := http.CanonicalHeaderKey(binding.Name)
		if name != "X-Csrf-Token" && name != "X-Xsrf-Token" && name != "Csrf-Token" {
			return "", fmt.Errorf("header binding is limited to dedicated CSRF headers")
		}
		headers.Set(name, token)
		return ex.Request, nil
	}
	if ex.Method == "GET" {
		return "", fmt.Errorf("body token binding requires POST/PUT/PATCH")
	}
	mediaType, _, err := mime.ParseMediaType(headers.Get("Content-Type"))
	if err != nil {
		return "", err
	}
	var body string
	switch {
	case binding.From == "form" && mediaType == "application/x-www-form-urlencoded":
		body, err = probeValues(ex.Request, binding.Name, 0, token)
	case binding.From == "json" && (mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")):
		body, err = probeJSON(ex.Request, binding.Name, token)
	default:
		err = fmt.Errorf("token binding requires matching form/JSON content type")
	}
	if len(body) > 4000 {
		return "", fmt.Errorf("bound body exceeds 4000 bytes")
	}
	return body, err
}
