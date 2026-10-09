package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/cyber"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
	"github.com/covoyage/covonaut/agentcore"
)

func subagentRole(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	return toolssubagent.SubagentRoleFromContext(ctx)
}

func noteWhiteboxTarget(catalog *cyber.Catalog, target string) {
	if catalog == nil || !cyber.LooksLikeRepo(target) {
		return
	}
	eng, err := catalog.GetEngagement()
	if err != nil {
		return
	}
	if eng.Depth == cyber.DepthQuick {
		return
	}
	if !eng.Whitebox {
		if _, err := catalog.SetEngagement(eng.Depth, true); err != nil {
			return
		}
	}
	_ = catalog.SetWhiteboxTarget(target)
}

func BuildCoverageUpsertTool(catalog *cyber.Catalog) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "coverage_upsert",
		Description: strings.Join([]string{
			"Record which surface was tested and why it stopped.",
			"outcome is clean, finding, blocked, or untested. untested is not clean.",
			"Omit surface to list the ledger.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"surface":    map[string]any{"type": "string", "description": "Endpoint, host, path, or check name."},
				"target":     map[string]any{"type": "string", "description": "Host, URL, or path. Empty means *."},
				"outcome":    map[string]any{"type": "string", "enum": []string{"clean", "finding", "blocked", "untested"}},
				"notes":      map[string]any{"type": "string", "description": "Why it stopped, or what remains untested."},
				"risk_class": map[string]any{"type": "string", "description": "Vulnerability class tested on this surface, e.g. idor or ssrf."},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			_ = ctx
			if catalog == nil {
				return nil, fmt.Errorf("coverage catalog unavailable")
			}
			if len(bytes.TrimSpace(args)) == 0 || string(bytes.TrimSpace(args)) == "{}" {
				items, err := catalog.ListCoverage()
				if err != nil {
					return nil, err
				}
				return map[string]any{"coverage": items, "count": len(items)}, nil
			}
			var item cyber.Coverage
			if err := json.Unmarshal(args, &item); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			saved, err := catalog.UpsertCoverage(item)
			if err != nil {
				return nil, err
			}
			return saved, nil
		},
	}
}

func BuildExchangeListTool(catalog *cyber.Catalog) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "exchange_list",
		Description: "List recorded HTTP exchanges (id, method, url, status). Use exchange_get for the body.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{"type": "integer", "description": "Max rows, newest last. Default 20."},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			_ = ctx
			if catalog == nil {
				return nil, fmt.Errorf("exchange catalog unavailable")
			}
			var params struct {
				Limit int `json:"limit"`
			}
			_ = json.Unmarshal(args, &params)
			items, err := catalog.ListExchanges()
			if err != nil {
				return nil, err
			}
			limit := params.Limit
			if limit <= 0 {
				limit = 20
			}
			if len(items) > limit {
				items = items[len(items)-limit:]
			}
			rows := make([]map[string]any, 0, len(items))
			for _, ex := range items {
				rows = append(rows, map[string]any{
					"id":     ex.ID,
					"method": ex.Method,
					"url":    ex.URL,
					"status": ex.StatusCode,
					"at":     ex.At,
				})
			}
			return map[string]any{"exchanges": rows, "count": len(rows)}, nil
		},
	}
}

func BuildExchangeGetTool(catalog *cyber.Catalog) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "exchange_get",
		Description: "Return one recorded HTTP exchange by id (ex-N), including request and response summaries.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string"},
			},
			"required": []any{"id"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			_ = ctx
			if catalog == nil {
				return nil, fmt.Errorf("exchange catalog unavailable")
			}
			var params struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			ex, ok, err := catalog.GetExchange(params.ID)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("exchange %q not found", strings.TrimSpace(params.ID))
			}
			return ex, nil
		},
	}
}

func BuildExchangeLabelTool(catalog *cyber.Catalog) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "exchange_label",
		Description: "Label a recorded exchange with a test identity, role, tenant, resource owner, and optional baseline ex-N. Do not include credentials.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":          map[string]any{"type": "string"},
				"identity":    map[string]any{"type": "string"},
				"role":        map[string]any{"type": "string"},
				"tenant":      map[string]any{"type": "string"},
				"resource":    map[string]any{"type": "string"},
				"owner":       map[string]any{"type": "string"},
				"baseline_id": map[string]any{"type": "string"},
			},
			"required": []string{"id"},
		},
		Func: func(_ context.Context, args json.RawMessage) (any, error) {
			if catalog == nil {
				return nil, fmt.Errorf("exchange catalog unavailable")
			}
			var params struct {
				ID string `json:"id"`
				cyber.Exchange
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, err
			}
			return catalog.LabelExchange(params.ID, params.Exchange)
		},
	}
}

func BuildExchangeSearchTool(catalog *cyber.Catalog) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "exchange_search",
		Description: "Search recorded HTTP exchanges by method, host, path, parameter name, status, or text.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"method":      map[string]any{"type": "string"},
				"host":        map[string]any{"type": "string"},
				"path":        map[string]any{"type": "string"},
				"parameter":   map[string]any{"type": "string", "description": "Query or request-body parameter name."},
				"identity":    map[string]any{"type": "string"},
				"tenant":      map[string]any{"type": "string"},
				"resource":    map[string]any{"type": "string"},
				"status_code": map[string]any{"type": "integer"},
				"text":        map[string]any{"type": "string", "description": "Case-insensitive text in URL, request, or response."},
				"limit":       map[string]any{"type": "integer", "description": "Maximum results, default 50 and maximum 200."},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			_ = ctx
			if catalog == nil {
				return nil, fmt.Errorf("exchange catalog unavailable")
			}
			var filter cyber.ExchangeFilter
			if err := json.Unmarshal(args, &filter); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			items, err := catalog.SearchExchanges(filter)
			if err != nil {
				return nil, err
			}
			return map[string]any{"exchanges": items, "count": len(items)}, nil
		},
	}
}

func BuildExchangeReplayTool(catalog *cyber.Catalog, transport http.RoundTripper) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "exchange_replay",
		Description: strings.Join([]string{
			"Replay a recorded HTTP exchange with optional method, URL, header, and body overrides.",
			"Replay requires an explicitly allowed host (scan_profile or enabled request_intake), stays on the captured origin, does not follow redirects, and has a daily budget of 200. The response is saved as new evidence.",
			"Incomplete or redacted requests require complete header/body replacements. Identity replacement rejects URL/body credentials and unclassified recorded headers.",
			"Only spawned hands-on cyber roles may call this tool.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":              map[string]any{"type": "string", "description": "Recorded exchange id (ex-N)."},
				"method":          map[string]any{"type": "string", "description": "Optional method override."},
				"url":             map[string]any{"type": "string", "description": "Optional URL override."},
				"headers":         map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				"body":            map[string]any{"type": "string", "description": "Optional request-body override."},
				"identity":        map[string]any{"type": "string", "description": "Optional test identity label (not a credential)."},
				"role":            map[string]any{"type": "string"},
				"tenant":          map[string]any{"type": "string"},
				"resource":        map[string]any{"type": "string"},
				"owner":           map[string]any{"type": "string"},
				"unauthenticated": map[string]any{"type": "boolean", "description": "Remove recorded authentication headers before replay."},
				"replace_auth":    map[string]any{"type": "boolean", "description": "Remove recorded authentication headers before applying replacement headers."},
				"replace_headers": map[string]any{"type": "boolean", "description": "Use only explicitly supplied headers, replacing all recorded headers."},
			},
			"required": []any{"id"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if catalog == nil || transport == nil {
				return nil, fmt.Errorf("cyber catalog and safe HTTP transport are required")
			}
			if !cyber.AllowOrchestratorTool(subagentRole(ctx), "exchange_replay") {
				return nil, fmt.Errorf("exchange_replay is reserved for spawned hands-on roles; the parent orchestrates")
			}
			var params struct {
				ID              string            `json:"id"`
				Method          string            `json:"method"`
				URL             string            `json:"url"`
				Headers         map[string]string `json:"headers"`
				Body            *string           `json:"body"`
				Identity        string            `json:"identity"`
				Role            string            `json:"role"`
				Tenant          string            `json:"tenant"`
				Resource        string            `json:"resource"`
				Owner           string            `json:"owner"`
				Unauthenticated bool              `json:"unauthenticated"`
				ReplaceAuth     bool              `json:"replace_auth"`
				ReplaceHeaders  bool              `json:"replace_headers"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			source, ok, err := catalog.GetExchange(params.ID)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("exchange %q not found", strings.TrimSpace(params.ID))
			}
			if (source.HeadersTruncated || strings.HasSuffix(source.ReqHeaders, "…")) && !params.ReplaceHeaders {
				return nil, fmt.Errorf("recorded headers are incomplete; replace_headers is required")
			}
			if (source.RequestTruncated || strings.HasSuffix(source.Request, "…")) && params.Body == nil {
				return nil, fmt.Errorf("recorded body is incomplete; a complete body override is required")
			}
			if source.Redacted && (!params.ReplaceHeaders || params.Body == nil) {
				return nil, fmt.Errorf("redacted request requires complete header and body replacement")
			}
			method := strings.ToUpper(strings.TrimSpace(params.Method))
			if method == "" {
				method = source.Method
			}
			targetURL := strings.TrimSpace(params.URL)
			if targetURL == "" {
				targetURL = source.URL
			}
			origin, err := url.Parse(source.URL)
			if err != nil || origin.Hostname() == "" || (origin.Scheme != "http" && origin.Scheme != "https") {
				return nil, fmt.Errorf("recorded exchange has no valid HTTP origin")
			}
			client := &http.Client{
				Timeout:   30 * time.Second,
				Transport: transport,
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}
			body := ""
			if params.Body != nil {
				body = *params.Body
			} else if method != http.MethodGet && method != http.MethodHead && !strings.HasPrefix(strings.TrimSpace(source.Request), "curl ") {
				body = source.Request
			}
			if ctx == nil {
				ctx = context.Background()
			}
			req, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewBufferString(body))
			if err != nil {
				return nil, fmt.Errorf("create replay request: %w", err)
			}
			if !sameHTTPOrigin(origin, req.URL) {
				return nil, fmt.Errorf("replay target must stay on the recorded HTTP origin")
			}
			if err := catalog.CheckReplayTarget(req.URL.String()); err != nil {
				return nil, err
			}
			if !params.ReplaceHeaders {
				applyRecordedHeaders(req.Header, source.ReqHeaders)
			}
			if params.Unauthenticated || params.ReplaceAuth {
				if err := validateReplayIdentitySource(req.Header, req.URL, body); err != nil {
					return nil, err
				}
				stripReplayAuth(req.Header)
			}
			for name, value := range params.Headers {
				if params.Unauthenticated && isReplayAuthName(name) && strings.TrimSpace(value) != "" {
					return nil, fmt.Errorf("unauthenticated replay cannot add authentication headers")
				}
				req.Header.Set(name, value)
			}
			if params.Unauthenticated {
				if err := validateReplayIdentitySource(req.Header, req.URL, body); err != nil {
					return nil, err
				}
			}
			if err := catalog.ConsumeReplayBudget(); err != nil {
				return nil, err
			}
			started := time.Now()
			resp, err := client.Do(req)
			if err != nil {
				return nil, fmt.Errorf("replay exchange: %w", err)
			}
			defer resp.Body.Close()
			responseBody, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
			if err != nil {
				return nil, fmt.Errorf("read replay response: %w", err)
			}
			bodyTruncated := len(responseBody) > 2<<20
			if bodyTruncated {
				responseBody = responseBody[:2<<20]
			}
			bodyHash := sha256.Sum256(responseBody)
			saved, err := catalog.RecordExchange(cyber.Exchange{
				Method: method, URL: targetURL, StatusCode: resp.StatusCode,
				ReqHeaders: formatHeaders(req.Header), Request: body, Response: string(responseBody),
				Identity: chooseLabel(params.Identity, source.Identity), Role: chooseLabel(params.Role, source.Role),
				Tenant: chooseLabel(params.Tenant, source.Tenant), Resource: chooseLabel(params.Resource, source.Resource),
				Owner: chooseLabel(params.Owner, source.Owner), BaselineID: source.ID,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"exchange_id": saved.ID, "replay_of": source.ID, "method": method,
				"url": targetURL, "status": resp.StatusCode, "response": saved.Response,
				"response_length": len(responseBody), "body_sha256": fmt.Sprintf("%x", bodyHash),
				"body_truncated": bodyTruncated, "evidence_clipped": len(responseBody) > 8000,
				"duration_ms": time.Since(started).Milliseconds(),
			}, nil
		},
	}
}

func sameHTTPOrigin(origin, target *url.URL) bool {
	return (target.Scheme == "http" || target.Scheme == "https") &&
		strings.EqualFold(origin.Scheme, target.Scheme) && strings.EqualFold(origin.Hostname(), target.Hostname()) &&
		origin.Port() == target.Port() && target.User == nil
}

func stripReplayAuth(header http.Header) {
	for name := range header {
		if isReplayAuthName(name) {
			header.Del(name)
		}
	}
}

func isReplayAuthName(name string) bool {
	normalized := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(name))
	return strings.Contains(normalized, "auth") || strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "apikey") || strings.Contains(normalized, "session") ||
		strings.Contains(normalized, "credential") || normalized == "cookie" || normalized == "key" ||
		normalized == "jwt" || normalized == "password" || normalized == "passwd"
}

func validateReplayIdentitySource(headers http.Header, target *url.URL, body string) error {
	query, err := url.ParseQuery(target.RawQuery)
	if err != nil {
		return fmt.Errorf("identity replacement requires a valid query string")
	}
	for name := range query {
		if isReplayAuthName(name) {
			return fmt.Errorf("identity replacement cannot reuse URL authentication parameter %q", name)
		}
	}
	for name := range headers {
		if isReplayAuthName(name) {
			continue
		}
		switch http.CanonicalHeaderKey(name) {
		case "Accept", "Accept-Encoding", "Accept-Language", "Content-Type", "Content-Length", "User-Agent", "Origin", "Referer", "Cache-Control", "Pragma", "Connection", "X-Requested-With":
		default:
			return fmt.Errorf("identity replacement cannot classify recorded header %q; use a sanitized exchange", name)
		}
	}
	if strings.TrimSpace(body) == "" {
		return nil
	}
	var value any
	if json.Unmarshal([]byte(body), &value) == nil {
		var hasCredentials func(any) bool
		hasCredentials = func(value any) bool {
			switch item := value.(type) {
			case map[string]any:
				for name, nested := range item {
					if isReplayAuthName(name) || hasCredentials(nested) {
						return true
					}
				}
			case []any:
				for _, nested := range item {
					if hasCredentials(nested) {
						return true
					}
				}
			}
			return false
		}
		if hasCredentials(value) {
			return fmt.Errorf("identity replacement cannot reuse JSON body credentials")
		}
		return nil
	}
	if strings.HasPrefix(strings.ToLower(headers.Get("Content-Type")), "application/x-www-form-urlencoded") {
		form, err := url.ParseQuery(body)
		if err != nil {
			return fmt.Errorf("identity replacement requires a complete form body")
		}
		for name := range form {
			if isReplayAuthName(name) {
				return fmt.Errorf("identity replacement cannot reuse form authentication parameter %q", name)
			}
		}
		return nil
	}
	return fmt.Errorf("identity replacement cannot classify the request body; use a sanitized exchange")
}

type replayCompareSide struct {
	Identity        string            `json:"identity"`
	Headers         map[string]string `json:"headers"`
	Unauthenticated bool              `json:"unauthenticated"`
	Body            *string           `json:"body"`
}

func BuildExchangeCompareTool(catalog *cyber.Catalog, transport http.RoundTripper) *agentcore.Tool {
	replay := BuildExchangeReplayTool(catalog, transport)
	return &agentcore.Tool{
		Name:        "exchange_compare",
		Description: "Replay a recorded HTTP request twice with separate identities; return evidence IDs and response differences, not an authorization verdict. Test-side auth must be replaced or explicitly removed. Both sides are preflighted; URL/body credentials and unclassified recorded headers require a sanitized source.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"id": map[string]any{"type": "string"},
			"baseline": map[string]any{"type": "object", "properties": map[string]any{
				"identity": map[string]any{"type": "string"}, "headers": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				"unauthenticated": map[string]any{"type": "boolean"}, "body": map[string]any{"type": "string"},
			}},
			"test": map[string]any{"type": "object", "properties": map[string]any{
				"identity": map[string]any{"type": "string"}, "headers": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				"unauthenticated": map[string]any{"type": "boolean"}, "body": map[string]any{"type": "string"},
			}, "required": []string{"identity"}},
		}, "required": []string{"id", "test"}},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if catalog == nil || !cyber.AllowOrchestratorTool(subagentRole(ctx), "exchange_replay") {
				return nil, fmt.Errorf("exchange_compare is reserved for spawned hands-on roles")
			}
			var params struct {
				ID       string            `json:"id"`
				Baseline replayCompareSide `json:"baseline"`
				Test     replayCompareSide `json:"test"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, err
			}
			if strings.TrimSpace(params.Test.Identity) == "" {
				return nil, fmt.Errorf("test identity is required")
			}
			source, exists, err := catalog.GetExchange(params.ID)
			if err != nil {
				return nil, err
			}
			if !exists {
				return nil, fmt.Errorf("exchange %q not found", params.ID)
			}
			if params.Baseline.Identity != "" && params.Baseline.Identity != source.Identity && !params.Baseline.Unauthenticated && len(params.Baseline.Headers) == 0 {
				return nil, fmt.Errorf("baseline identity differs from the recorded identity without replacement authentication")
			}
			if err := validateReplayCompareSide(params.Test); err != nil {
				return nil, err
			}
			if params.Baseline.Unauthenticated || len(params.Baseline.Headers) > 0 {
				if err := validateReplayCompareSide(params.Baseline); err != nil {
					return nil, err
				}
			}
			origin, err := url.Parse(source.URL)
			if err != nil {
				return nil, err
			}
			for index, side := range []replayCompareSide{params.Baseline, params.Test} {
				if index == 0 && !side.Unauthenticated && len(side.Headers) == 0 {
					continue
				}
				headers := make(http.Header)
				applyRecordedHeaders(headers, source.ReqHeaders)
				body := ""
				if source.Method != http.MethodGet && source.Method != http.MethodHead {
					body = source.Request
				}
				if side.Body != nil {
					body = *side.Body
				}
				if err := validateReplayIdentitySource(headers, origin, body); err != nil {
					return nil, err
				}
			}
			send := func(side replayCompareSide, replace bool) (map[string]any, error) {
				input := map[string]any{"id": params.ID, "identity": side.Identity, "headers": side.Headers,
					"unauthenticated": side.Unauthenticated, "replace_auth": replace}
				if side.Body != nil {
					input["body"] = *side.Body
				}
				encoded, err := json.Marshal(input)
				if err != nil {
					return nil, err
				}
				result, err := replay.Func(ctx, encoded)
				if err != nil {
					return nil, err
				}
				return result.(map[string]any), nil
			}
			baseline, err := send(params.Baseline, len(params.Baseline.Headers) > 0)
			if err != nil {
				return nil, err
			}
			test, err := send(params.Test, !params.Test.Unauthenticated)
			if err != nil {
				return map[string]any{"baseline_exchange_id": baseline["exchange_id"]}, err
			}
			comparable := baseline["body_truncated"] != true && test["body_truncated"] != true
			comparison := map[string]any{
				"baseline_exchange_id": baseline["exchange_id"], "test_exchange_id": test["exchange_id"],
				"baseline_status": baseline["status"], "test_status": test["status"],
				"baseline_length": baseline["response_length"], "test_length": test["response_length"],
				"comparable":           comparable,
				"baseline_duration_ms": baseline["duration_ms"], "test_duration_ms": test["duration_ms"],
			}
			if comparable {
				comparison["same_body"] = baseline["body_sha256"] == test["body_sha256"]
			}
			return comparison, nil
		},
	}
}

func validateReplayCompareSide(side replayCompareSide) error {
	if side.Unauthenticated {
		if len(side.Headers) != 0 {
			return fmt.Errorf("unauthenticated comparison cannot add headers")
		}
		return nil
	}
	for name, value := range side.Headers {
		switch http.CanonicalHeaderKey(name) {
		case "Authorization", "Cookie", "X-Api-Key", "X-Auth-Token":
			if strings.TrimSpace(value) != "" {
				return nil
			}
		}
	}
	return fmt.Errorf("comparison side requires authentication headers or unauthenticated=true")
}

func chooseLabel(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func applyRecordedHeaders(header http.Header, raw string) {
	for _, line := range strings.Split(raw, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(name) != "" {
			header.Set(strings.TrimSpace(name), strings.TrimSpace(value))
		}
	}
}

func formatHeaders(header http.Header) string {
	var b strings.Builder
	for name, values := range header {
		for _, value := range values {
			fmt.Fprintf(&b, "%s: %s\n", name, value)
		}
	}
	return strings.TrimSpace(b.String())
}

func BuildScanProfileTool(catalog *cyber.Catalog) *agentcore.Tool {
	return BuildScanProfileToolForRole(catalog, "parent")
}

func BuildScanProfileToolForRole(catalog *cyber.Catalog, instanceRole string) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "scan_profile",
		Description: strings.Join([]string{
			"Set scan depth (quick, standard, deep) and whether the target is a local repository.",
			"Only the parent may set allowed_hosts to operator-authorized exact hostnames before network scans.",
			"Default depth is deep. A local directory or git repo enables the whitebox floor unless depth is quick.",
			"Omit arguments to read the current profile.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"depth":         map[string]any{"type": "string", "enum": []string{"quick", "standard", "deep"}},
				"whitebox":      map[string]any{"type": "boolean", "description": "Force the local-repository floor on or off."},
				"allowed_hosts": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Exact hosts for active network scans; required before network recipes run."},
				"target":        map[string]any{"type": "string", "description": "If this path is a local repo, whitebox is enabled unless depth is quick."},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			_ = ctx
			if catalog == nil {
				return nil, fmt.Errorf("engagement catalog unavailable")
			}
			if len(bytes.TrimSpace(args)) == 0 || string(bytes.TrimSpace(args)) == "{}" {
				eng, err := catalog.GetEngagement()
				if err != nil {
					return nil, err
				}
				gaps, _ := catalog.WhiteboxGaps()
				runtime, _ := catalog.LoadRuntime()
				return map[string]any{
					"depth": eng.Depth, "whitebox": eng.Whitebox, "whitebox_target": eng.WhiteboxTarget, "whitebox_gaps": gaps,
					"scope_mode": eng.ScopeMode, "allowed_hosts": eng.AllowedHosts, "base_ref": eng.BaseRef, "head_ref": eng.HeadRef, "runtime": runtime,
				}, nil
			}
			if instanceRole != "parent" {
				return nil, fmt.Errorf("only the parent may change scan profile or authorized hosts")
			}
			var params struct {
				Depth        string    `json:"depth"`
				Whitebox     *bool     `json:"whitebox"`
				Target       string    `json:"target"`
				AllowedHosts *[]string `json:"allowed_hosts"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			eng, err := catalog.GetEngagement()
			if err != nil {
				return nil, err
			}
			depth := params.Depth
			if depth == "" {
				depth = eng.Depth
			}
			whitebox := eng.Whitebox
			if params.Whitebox != nil {
				whitebox = *params.Whitebox
			}
			if cyber.LooksLikeRepo(params.Target) && !strings.EqualFold(strings.TrimSpace(depth), cyber.DepthQuick) {
				whitebox = true
			}
			if params.AllowedHosts != nil {
				if _, err := catalog.SetAllowedHosts(*params.AllowedHosts); err != nil {
					return nil, err
				}
			}
			saved, err := catalog.SetEngagement(depth, whitebox)
			if err != nil {
				return nil, err
			}
			if whitebox && cyber.LooksLikeRepo(params.Target) {
				if err := catalog.SetWhiteboxTarget(params.Target); err != nil {
					return nil, err
				}
				saved, err = catalog.GetEngagement()
				if err != nil {
					return nil, err
				}
			}
			gaps, _ := catalog.WhiteboxGaps()
			runtime, _ := catalog.LoadRuntime()
			return map[string]any{
				"depth": saved.Depth, "whitebox": saved.Whitebox, "whitebox_target": saved.WhiteboxTarget, "whitebox_gaps": gaps,
				"scope_mode": saved.ScopeMode, "allowed_hosts": saved.AllowedHosts, "base_ref": saved.BaseRef, "head_ref": saved.HeadRef, "runtime": runtime,
			}, nil
		},
	}
}

func BuildFinishScanTool(catalog *cyber.Catalog) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "finish_scan",
		Description: strings.Join([]string{
			"Close the recon pass. Writes a report of findings, coverage, and untested surfaces.",
			"depth deep refuses to finish while the local-repository floor (semgrep, gitleaks or trufflehog, trivy fs) has not actually run.",
			"Untested coverage is not clean. Confirmed findings still need poc or exchange_id.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{"type": "string", "description": "Optional local path. A repo enables the whitebox floor unless depth is quick."},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			_ = ctx
			if catalog == nil {
				return nil, fmt.Errorf("engagement catalog unavailable")
			}
			var params struct {
				Target string `json:"target"`
			}
			_ = json.Unmarshal(args, &params)
			noteWhiteboxTarget(catalog, params.Target)
			report, err := catalog.FinishScan()
			if err != nil {
				gaps, _ := catalog.WhiteboxGaps()
				return map[string]any{
					"finished":      false,
					"error":         err.Error(),
					"whitebox_gaps": gaps,
				}, err
			}
			return map[string]any{
				"finished":          true,
				"complete":          report.Complete,
				"depth":             report.Depth,
				"whitebox":          report.Whitebox,
				"whitebox_gaps":     report.WhiteboxGaps,
				"confirmed":         report.Confirmed,
				"open_proof_gaps":   report.OpenProofGaps,
				"untested_surfaces": report.UntestedSurfaces,
				"findings":          len(report.Findings),
				"coverage":          len(report.Coverage),
				"coverage_gaps":     report.CoverageGaps,
				"artifacts":         []string{".covo/cyber/scan_report.json", ".covo/cyber/scan_report.md", ".covo/cyber/findings.csv", ".covo/cyber/findings.sarif"},
			}, nil
		},
	}
}
