package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/cyber"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
)

func TestExchangeReplayRejectsIncompleteSources(t *testing.T) {
	for _, source := range []cyber.Exchange{
		{HeadersTruncated: true}, {RequestTruncated: true}, {Redacted: true},
		{Request: strings.Repeat("a", 4100)}, {ReqHeaders: "Cookie: " + strings.Repeat("a", 2100)},
	} {
		catalog := cyber.NewCatalog(t.TempDir())
		if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
			t.Fatal(err)
		}
		source.URL, source.Method = "https://example.test/submit", "POST"
		ex, err := catalog.RecordExchange(source)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		tool := BuildExchangeReplayTool(catalog, roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			body, _ := io.ReadAll(req.Body)
			if string(body) != "complete" || req.Header.Get("Cookie") != "" {
				t.Fatalf("incomplete request reused: %q, %v", body, req.Header)
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
		}))
		ctx := toolssubagent.WithSubagentRole(context.Background(), "verifier")
		if _, err := tool.Func(ctx, json.RawMessage(`{"id":"`+ex.ID+`"}`)); err == nil || calls != 0 {
			t.Fatalf("incomplete source sent: %+v, calls=%d, %v", ex, calls, err)
		}
		if _, err := tool.Func(ctx, json.RawMessage(`{"id":"`+ex.ID+`","replace_headers":true,"body":"complete"}`)); err != nil || calls != 1 {
			t.Fatalf("complete replacement failed: calls=%d, %v", calls, err)
		}
	}
}

func TestExchangeCompareRejectsNonHeaderCredentials(t *testing.T) {
	for _, source := range []cyber.Exchange{
		{URL: "https://example.test/a?access_token=alice"},
		{URL: "https://example.test/a", Method: "POST", Request: `{"nested":{"password":"alice"}}`, ReqHeaders: "Content-Type: application/json"},
		{URL: "https://example.test/a", Method: "POST", Request: "session_id=alice", ReqHeaders: "Content-Type: application/x-www-form-urlencoded"},
		{URL: "https://example.test/a", ReqHeaders: "X-Custom-Cred: alice"},
	} {
		catalog := cyber.NewCatalog(t.TempDir())
		if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
			t.Fatal(err)
		}
		ex, err := catalog.RecordExchange(source)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		tool := BuildExchangeCompareTool(catalog, roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
		}))
		ctx := toolssubagent.WithSubagentRole(context.Background(), "verifier")
		if _, err := tool.Func(ctx, json.RawMessage(`{"id":"`+ex.ID+`","test":{"identity":"anonymous","unauthenticated":true}}`)); err == nil || calls != 0 {
			t.Fatalf("unsupported identity source sent: %+v, calls=%d, %v", source, calls, err)
		}
	}
}

func TestExchangeReplayRemovesExtendedAuthHeaders(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
		t.Fatal(err)
	}
	source, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/a", ReqHeaders: "X-Access-Token: alice\nX-Session-ID: old\nCookie: session=alice"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	tool := BuildExchangeReplayTool(catalog, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Header.Get("X-Access-Token") != "" || req.Header.Get("X-Session-ID") != "" || req.Header.Get("Cookie") != "" {
			t.Fatalf("extended auth headers retained: %v", req.Header)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
	}))
	ctx := toolssubagent.WithSubagentRole(context.Background(), "verifier")
	if _, err := tool.Func(ctx, json.RawMessage(`{"id":"`+source.ID+`","unauthenticated":true}`)); err != nil || calls != 1 {
		t.Fatalf("extended auth replay failed: calls=%d, %v", calls, err)
	}
}

func TestCSRFRetestRejectsIncompleteAndExpiredSources(t *testing.T) {
	for _, test := range []struct {
		redacted  bool
		truncated bool
		expired   bool
	}{
		{redacted: true}, {truncated: true}, {expired: true},
	} {
		catalog := cyber.NewCatalog(t.TempDir())
		if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
			t.Fatal(err)
		}
		page, _ := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/form"})
		submit, _ := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/submit", Method: "POST", ReqHeaders: "Content-Type: application/x-www-form-urlencoded", Request: "csrf=old", Redacted: test.redacted, RequestTruncated: test.truncated})
		calls := 0
		transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": []string{"csrf=expired; Path=/; Max-Age=0"}}, Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
		})
		args, _ := json.Marshal(map[string]string{"page_id": page.ID, "submit_id": submit.ID, "extract_from": "cookie", "token_name": "csrf", "form_field": "csrf"})
		_, err := BuildCSRFRetestTool(catalog, "verifier", transport).Func(context.Background(), args)
		wantCalls := 0
		if test.expired {
			wantCalls = 1
		}
		if err == nil || calls != wantCalls {
			t.Fatalf("invalid CSRF source submitted: %+v, calls=%d, %v", test, calls, err)
		}
	}
}

func TestCSRFRetestCookieJarLifecycle(t *testing.T) {
	for _, test := range []struct {
		name        string
		setCookie   string
		wantSession string
	}{
		{"new session", "new_session=fresh; Path=/", "new_session"},
		{"deleted session", "session=deleted; Path=/; Max-Age=0", ""},
		{"expired session", "session=expired; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := cyber.NewCatalog(t.TempDir())
			if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
				t.Fatal(err)
			}
			page, _ := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/form"})
			submit, _ := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/submit", Method: "POST", ReqHeaders: "Content-Type: application/x-www-form-urlencoded\nCookie: session=old", Request: "csrf=old"})
			calls := 0
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": []string{test.setCookie}}, Body: io.NopCloser(strings.NewReader(`<input name="csrf" value="fresh">`)), Request: req}, nil
				}
				if test.wantSession != "" {
					cookie, err := req.Cookie(test.wantSession)
					if err != nil || cookie.Value != "fresh" {
						t.Fatalf("new session missing: %v, %v", req.Cookies(), err)
					}
				} else if _, err := req.Cookie("session"); err == nil {
					t.Fatalf("deleted session was sent: %v", req.Cookies())
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
			})
			args, _ := json.Marshal(map[string]string{"page_id": page.ID, "submit_id": submit.ID, "extract_from": "body", "token_name": "csrf", "form_field": "csrf"})
			if _, err := BuildCSRFRetestTool(catalog, "verifier", transport).Func(context.Background(), args); err != nil || calls != 2 {
				t.Fatalf("cookie lifecycle failed: calls=%d, %v", calls, err)
			}
		})
	}
}

func TestExchangeReplayRecordsNewEvidence(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
		t.Fatal(err)
	}
	source, err := catalog.RecordExchange(cyber.Exchange{
		Method: "POST", URL: "https://example.test/users", Request: `{"role":"user"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		if string(body) != `{"role":"admin"}` {
			t.Fatalf("unexpected replay body: %s", body)
		}
		return &http.Response{
			StatusCode: 201,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request:    req,
		}, nil
	})
	tool := BuildExchangeReplayTool(catalog, rt)
	ctx := toolssubagent.WithSubagentRole(context.Background(), "verifier")
	result, err := tool.Func(ctx, json.RawMessage(`{"id":"`+source.ID+`","body":"{\"role\":\"admin\"}"}`))
	if err != nil {
		t.Fatal(err)
	}
	row := result.(map[string]any)
	if row["exchange_id"] == source.ID || row["status"] != 201 {
		t.Fatalf("unexpected replay result: %#v", row)
	}
}

func TestWebContextIsBoundedAndOmitsSecrets(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	for _, rawURL := range []string{"https://example.test/orders?id=secret-value", "https://other.test/orders?id=other"} {
		if _, err := catalog.RecordExchange(cyber.Exchange{URL: rawURL, ReqHeaders: "Cookie: private-cookie", Response: "secret-body"}); err != nil {
			t.Fatal(err)
		}
	}
	for count := 0; count < 8; count++ {
		if _, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/orders?id=secret-value"}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := BuildWebContextTool(catalog).Func(context.Background(), json.RawMessage(`{"id":"ex-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil || strings.Contains(string(encoded), "secret-value") || strings.Contains(string(encoded), "private-cookie") || strings.Contains(string(encoded), "secret-body") {
		t.Fatalf("context leaked raw evidence: %s err=%v", encoded, err)
	}
	if len(result.(map[string]any)["recent"].([]map[string]any)) != 5 {
		t.Fatalf("context crossed origins: %+v", result)
	}
}

func TestCSRFRetestRedactsFreshToken(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{"example.test", "other.test"}); err != nil {
		t.Fatal(err)
	}
	page, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/form", ReqHeaders: "Cookie: session=alice"})
	if err != nil {
		t.Fatal(err)
	}
	submit, err := catalog.RecordExchange(cyber.Exchange{
		Method: "POST", URL: "https://example.test/submit", ReqHeaders: "Content-Type: application/x-www-form-urlencoded\nCookie: session=alice",
		Request: "csrf=old&action=save", StatusCode: 403,
	})
	if err != nil {
		t.Fatal(err)
	}
	const fresh = "fresh-secret-123"
	calls := 0
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Header.Get("Cookie") != "session=alice" {
			t.Fatalf("session auth was lost: %v", req.Header)
		}
		if calls == 1 && req.Method == http.MethodGet {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<form><input name="csrf" value="` + fresh + `"></form>`)), Request: req}, nil
		}
		body, err := io.ReadAll(req.Body)
		if err != nil || req.Method != http.MethodPost || req.URL.Path != "/submit" || !strings.Contains(string(body), "csrf="+fresh) || !strings.Contains(string(body), "action=save") {
			t.Fatalf("unexpected submission: %s err=%v", body, err)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ack " + fresh)), Request: req}, nil
	})
	args := json.RawMessage(`{"page_id":"` + page.ID + `","submit_id":"` + submit.ID + `","extract_from":"body","token_name":"csrf","form_field":"csrf"}`)
	ctx := toolssubagent.WithSubagentRole(context.Background(), "verifier")
	if _, err := BuildCSRFRetestTool(catalog, "parent", transport).Func(ctx, args); err == nil || calls != 0 {
		t.Fatalf("parent bypassed CSRF gate: calls=%d err=%v", calls, err)
	}
	tool := BuildCSRFRetestTool(catalog, "verifier", transport)
	result, err := tool.Func(ctx, args)
	if err != nil || calls != 2 {
		t.Fatalf("CSRF retest failed: result=%+v calls=%d err=%v", result, calls, err)
	}
	output, _ := json.Marshal(result)
	items, err := catalog.ListExchanges()
	stored, _ := json.Marshal(items)
	if err != nil || strings.Contains(string(output), fresh) || strings.Contains(string(stored), fresh) || result.(map[string]any)["submit_exchange_id"] == "" {
		t.Fatalf("fresh token leaked: output=%s stored=%s err=%v", output, stored, err)
	}
}

func TestCSRFRetestRejectsCrossOriginBeforeSending(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{"example.test", "other.test"}); err != nil {
		t.Fatal(err)
	}
	page, _ := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/form"})
	submit, _ := catalog.RecordExchange(cyber.Exchange{Method: "POST", URL: "https://other.test/submit", ReqHeaders: "Content-Type: application/x-www-form-urlencoded", Request: "csrf=old"})
	tool := BuildCSRFRetestTool(catalog, "verifier", roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("cross-origin retest reached transport")
		return nil, nil
	}))
	args := json.RawMessage(`{"page_id":"` + page.ID + `","submit_id":"` + submit.ID + `","extract_from":"body","token_name":"csrf","form_field":"csrf"}`)
	if _, err := tool.Func(context.Background(), args); err == nil {
		t.Fatal("cross-origin form was accepted")
	}
}

func TestCSRFRetestRequiresExplicitHostScope(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	page, _ := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/form"})
	submit, _ := catalog.RecordExchange(cyber.Exchange{Method: "POST", URL: "https://example.test/submit", ReqHeaders: "Content-Type: application/x-www-form-urlencoded", Request: "csrf=old"})
	tool := BuildCSRFRetestTool(catalog, "verifier", roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unscoped retest reached transport")
		return nil, nil
	}))
	args := json.RawMessage(`{"page_id":"` + page.ID + `","submit_id":"` + submit.ID + `","extract_from":"body","token_name":"csrf","form_field":"csrf"}`)
	if _, err := tool.Func(context.Background(), args); err == nil {
		t.Fatal("passive evidence authorized active POST")
	}
}

func TestCSRFRetestHeaderAndCookieSources(t *testing.T) {
	for _, source := range []string{"header", "cookie"} {
		t.Run(source, func(t *testing.T) {
			catalog := cyber.NewCatalog(t.TempDir())
			if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
				t.Fatal(err)
			}
			page, _ := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/form", ReqHeaders: "Cookie: session=page"})
			submit, _ := catalog.RecordExchange(cyber.Exchange{
				Method: "POST", URL: "https://example.test/submit", ReqHeaders: "Content-Type: application/x-www-form-urlencoded\nCookie: session=submit; csrf=old",
				Request: "csrf=old", StatusCode: 403,
			})
			calls := 0
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					header := make(http.Header)
					header.Add("Set-Cookie", "session=rotated; Path=/")
					if source == "header" {
						header.Set("X-CSRF", "refreshed")
					} else {
						header.Add("Set-Cookie", "csrf=refreshed; Path=/")
					}
					return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
				}
				body, _ := io.ReadAll(req.Body)
				if !strings.HasPrefix(req.Header.Get("Cookie"), "session=rotated") {
					t.Fatalf("POST kept the stale session: %q", req.Header.Get("Cookie"))
				}
				if req.Header.Get("Cookie") != "session=rotated; csrf=refreshed" && source == "cookie" {
					t.Fatalf("POST session replaced by GET session: %q", req.Header.Get("Cookie"))
				}
				if !strings.Contains(string(body), "csrf=refreshed") {
					t.Fatalf("new token missing from form body: %s", body)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("done")), Request: req}, nil
			})
			tool := BuildCSRFRetestTool(catalog, "verifier", transport)
			name := "X-CSRF"
			if source == "cookie" {
				name = "csrf"
			}
			args, _ := json.Marshal(map[string]string{"page_id": page.ID, "submit_id": submit.ID, "extract_from": source, "token_name": name, "form_field": "csrf"})
			if _, err := tool.Func(context.Background(), args); err != nil || calls != 2 {
				t.Fatalf("retest calls=%d err=%v", calls, err)
			}
			if _, err := tool.Func(context.Background(), json.RawMessage(`{"page_id":"`+page.ID+`","submit_id":"`+submit.ID+`","extract_from":"bad","token_name":"csrf","form_field":"csrf"}`)); err == nil || calls != 2 {
				t.Fatalf("invalid extraction sent a request: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestCSRFCookieAppliesOnlyToSubmissionScope(t *testing.T) {
	target, err := url.Parse("https://app.example.test/submit")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := url.Parse("https://app.example.test/form")
	for _, cookie := range []*http.Cookie{
		{Name: "csrf", Value: "x", Domain: "outside.test", Path: "/"},
		{Name: "csrf", Value: "x", Domain: "test", Path: "/"},
		{Name: "csrf", Value: "x", Path: "/form"},
	} {
		if csrfCookieApplies(cookie, target, page) {
			t.Fatalf("out-of-scope cookie accepted: %+v", cookie)
		}
	}
	if !csrfCookieApplies(&http.Cookie{Name: "csrf", Value: "x", Domain: "example.test", Path: "/"}, target, page) {
		t.Fatal("valid same-site cookie rejected")
	}
	accountPage, _ := url.Parse("https://app.example.test/account/form")
	if csrfCookieApplies(&http.Cookie{Name: "csrf", Value: "x"}, target, accountPage) {
		t.Fatal("cookie with implicit /account path crossed into /submit")
	}
}

func TestExchangeReplayBlocksParent(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	source, err := catalog.RecordExchange(cyber.Exchange{Method: "GET", URL: "https://example.test/"})
	if err != nil {
		t.Fatal(err)
	}
	tool := BuildExchangeReplayTool(catalog, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("parent replay reached transport")
		return nil, nil
	}))
	if _, err := tool.Func(context.Background(), json.RawMessage(`{"id":"`+source.ID+`"}`)); err == nil {
		t.Fatal("expected parent replay to be blocked")
	}
}

func TestExchangeReplayRejectsOtherOrigin(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	source, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/a"})
	if err != nil {
		t.Fatal(err)
	}
	tool := BuildExchangeReplayTool(catalog, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("out-of-origin replay reached transport")
		return nil, nil
	}))
	ctx := toolssubagent.WithSubagentRole(context.Background(), "verifier")
	if _, err := tool.Func(ctx, json.RawMessage(`{"id":"`+source.ID+`","url":"https://outside.test/a"}`)); err == nil {
		t.Fatal("expected cross-origin override to fail")
	}
}

func TestExchangeReplayRequiresExplicitActiveScope(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	source, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/a"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	tool := BuildExchangeReplayTool(catalog, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
	}))
	ctx := toolssubagent.WithSubagentRole(context.Background(), "verifier")
	args := json.RawMessage(`{"id":"` + source.ID + `"}`)
	if _, err := tool.Func(ctx, args); err == nil || calls != 0 {
		t.Fatalf("passive evidence authorized active replay: calls=%d err=%v", calls, err)
	}
	if _, err := catalog.SetIntake(cyber.IntakeConfig{Enabled: true, Hosts: []string{"example.test"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Func(ctx, args); err != nil || calls != 1 {
		t.Fatalf("enabled intake did not authorize replay: calls=%d err=%v", calls, err)
	}
}

func TestExchangeCompareIsolatesIdentityEvidence(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
		t.Fatal(err)
	}
	source, err := catalog.RecordExchange(cyber.Exchange{
		URL: "https://example.test/orders/1", Identity: "alice", ReqHeaders: "Cookie: session=alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	tool := BuildExchangeCompareTool(catalog, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 && req.Header.Get("Cookie") != "session=alice" {
			t.Fatalf("baseline lost its cookie: %v", req.Header)
		}
		if calls == 2 && (req.Header.Get("Cookie") != "" || req.Header.Get("Authorization") != "Bearer bob") {
			t.Fatalf("test request reused baseline auth: %v", req.Header)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"owner":"alice"}`)), Request: req}, nil
	}))
	ctx := toolssubagent.WithSubagentRole(context.Background(), "verifier")
	args := json.RawMessage(`{"id":"` + source.ID + `","test":{"identity":"bob","headers":{"Authorization":"Bearer bob"}}}`)
	if _, err := tool.Func(context.Background(), args); err == nil {
		t.Fatal("parent should not compare")
	}
	if _, err := tool.Func(ctx, json.RawMessage(`{"id":"`+source.ID+`","test":{"identity":"bob"}}`)); err == nil || calls != 0 {
		t.Fatalf("test identity without auth should not send: calls=%d err=%v", calls, err)
	}
	if _, err := tool.Func(ctx, json.RawMessage(`{"id":"`+source.ID+`","test":{"identity":"bob","headers":{"Authorization":" "}}}`)); err == nil || calls != 0 {
		t.Fatalf("empty authentication should not send: calls=%d err=%v", calls, err)
	}
	result, err := tool.Func(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	row := result.(map[string]any)
	if calls != 2 || row["same_body"] != true || row["baseline_exchange_id"] == row["test_exchange_id"] {
		t.Fatalf("comparison did not save two independent facts: %+v calls=%d", row, calls)
	}
}

func TestExchangeReplayDoesNotFollowRedirect(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
		t.Fatal(err)
	}
	source, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/start"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	tool := BuildExchangeReplayTool(catalog, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://outside.test/"}}, Body: io.NopCloser(strings.NewReader("redirect")), Request: req}, nil
	}))
	ctx := toolssubagent.WithSubagentRole(context.Background(), "verifier")
	result, err := tool.Func(ctx, json.RawMessage(`{"id":"`+source.ID+`"}`))
	if err != nil || result.(map[string]any)["status"] != http.StatusFound || calls != 1 {
		t.Fatalf("redirect was followed: result=%+v calls=%d err=%v", result, calls, err)
	}
}

func TestExchangeCompareTruncatedBodyIsInconclusive(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
		t.Fatal(err)
	}
	source, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/start"})
	if err != nil {
		t.Fatal(err)
	}
	tool := BuildExchangeCompareTool(catalog, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("a", (2<<20)+1))), Request: req}, nil
	}))
	ctx := toolssubagent.WithSubagentRole(context.Background(), "verifier")
	result, err := tool.Func(ctx, json.RawMessage(`{"id":"`+source.ID+`","test":{"identity":"anonymous","unauthenticated":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	row := result.(map[string]any)
	if row["comparable"] != false || row["same_body"] != nil {
		t.Fatalf("truncated comparison claimed certainty: %+v", row)
	}
}

func TestWebParamProbeRecordsScopedObservation(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	source, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/orders?id=1&view=full"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Query().Get("view") != "full" || !strings.HasPrefix(req.URL.Query().Get("id"), "covo_probe_") {
			t.Fatalf("unexpected probe mutation: %s", req.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(req.URL.Query().Get("id"))), Request: req}, nil
	})
	tool := BuildWebParamProbeTool(catalog, "verifier", transport)
	args := json.RawMessage(`{"id":"` + source.ID + `","parameter":"id"}`)
	if _, err := tool.Func(context.Background(), args); err == nil || calls != 0 {
		t.Fatalf("unscoped probe sent a request: calls=%d err=%v", calls, err)
	}
	if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
		t.Fatal(err)
	}
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	row := result.(map[string]any)
	if calls != 1 || row["observation"] != "marker_observed" || row["exchange_id"] == source.ID {
		t.Fatalf("probe evidence=%+v calls=%d", row, calls)
	}
	if _, err := tool.Func(context.Background(), json.RawMessage(`{"id":"`+source.ID+`","parameter":"absent"}`)); err == nil || calls != 1 {
		t.Fatalf("missing parameter sent a request: calls=%d err=%v", calls, err)
	}
}

func TestWebParamProbeTruncatedResponseIsInconclusive(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
		t.Fatal(err)
	}
	source, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/a?q=old"})
	if err != nil {
		t.Fatal(err)
	}
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 8100))), Request: req}, nil
	})
	tool := BuildWebParamProbeTool(catalog, "verifier", transport)
	result, err := tool.Func(context.Background(), json.RawMessage(`{"id":"`+source.ID+`","parameter":"q"}`))
	if err != nil || result.(map[string]any)["observation"] != "inconclusive_truncated" {
		t.Fatalf("truncated probe=%+v err=%v", result, err)
	}
}

func TestWebParamProbeRejectsParentAndNonGET(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
		t.Fatal(err)
	}
	source, err := catalog.RecordExchange(cyber.Exchange{Method: "POST", URL: "https://example.test/a?q=old"})
	if err != nil {
		t.Fatal(err)
	}
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unauthorized or non-GET probe reached transport")
		return nil, nil
	})
	ctx := toolssubagent.WithSubagentRole(context.Background(), "verifier")
	args := json.RawMessage(`{"id":"` + source.ID + `","parameter":"q"}`)
	if _, err := BuildWebParamProbeTool(catalog, "parent", transport).Func(ctx, args); err == nil {
		t.Fatal("fixed parent role bypassed")
	}
	if _, err := BuildWebParamProbeTool(catalog, "verifier", transport).Func(ctx, args); err == nil {
		t.Fatal("POST exchange was probed as GET")
	}
}

func TestExchangeSearchDecodesStatusFilter(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.RecordExchange(cyber.Exchange{Method: "GET", URL: "https://example.test/private?id=7", StatusCode: 403}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.RecordExchange(cyber.Exchange{Method: "GET", URL: "https://example.test/public?id=7", StatusCode: 200}); err != nil {
		t.Fatal(err)
	}
	result, err := BuildExchangeSearchTool(catalog).Func(context.Background(), json.RawMessage(`{"parameter":"id","status_code":403}`))
	if err != nil {
		t.Fatal(err)
	}
	rows := result.(map[string]any)
	if rows["count"] != 1 {
		t.Fatalf("unexpected search result: %#v", rows)
	}
}

func TestExchangeLabelTool(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	baseline, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/orders/1"})
	if err != nil {
		t.Fatal(err)
	}
	testExchange, err := catalog.RecordExchange(cyber.Exchange{URL: baseline.URL})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{
		"id": testExchange.ID, "identity": "bob", "role": "customer", "owner": "alice", "resource": "order:1", "baseline_id": baseline.ID,
	})
	if _, err := BuildExchangeLabelTool(catalog).Func(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	got, _, err := catalog.GetExchange(testExchange.ID)
	if err != nil || got.Identity != "bob" || got.BaselineID != baseline.ID {
		t.Fatalf("identity label failed: %+v err=%v", got, err)
	}
}
