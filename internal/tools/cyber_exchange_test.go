package tools

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/covoyage/covo-agent/internal/cyber"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
)

func TestExchangeReplayPreservesWirePathAndDiagnostics(t *testing.T) {
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- request.RequestURI
		writer.WriteHeader(http.StatusFound)
		writer.Write([]byte("moved"))
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{parsed.Hostname()}); err != nil {
		t.Fatal(err)
	}
	path := "/a/../b/%2f?value=%2F&value=+"
	source, err := catalog.RecordExchange(cyber.Exchange{URL: server.URL + path})
	if err != nil {
		t.Fatal(err)
	}
	tool := BuildExchangeReplayTool(catalog, &http.Transport{})
	ctx := toolssubagent.WithSubagentRole(context.Background(), "verifier")
	args, _ := json.Marshal(map[string]any{"id": source.ID, "protocol": "http1"})
	result, err := tool.Func(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if target := <-requests; target != path {
		t.Fatal("request target normalized", target)
	}
	row := result.(map[string]any)
	if row["http_protocol"] != "HTTP/1.1" || row["diagnostics"].(cyber.HTTPDiagnostics).TTFBMS == nil {
		t.Fatal("missing actual protocol/timing", row)
	}
	exchange, _, err := catalog.GetExchange(row["exchange_id"].(string))
	if err != nil || exchange.Diagnostics == nil || exchange.HTTPProtocol != "HTTP/1.1" {
		t.Fatal("diagnostics not persisted", err)
	}
	args, _ = json.Marshal(map[string]any{"id": source.ID, "protocol": "http2"})
	if _, err := tool.Func(ctx, args); err == nil {
		t.Fatal("HTTP/2 cleartext accepted")
	}
	args, _ = json.Marshal(map[string]any{"id": source.ID, "unauthenticated": true, "client_certificate": true})
	if _, err := tool.Func(ctx, args); err == nil {
		t.Fatal("anonymous mTLS allowed")
	}
	if len(requests) != 0 {
		t.Fatal("invalid protocol/identity sent a request")
	}
	args, _ = json.Marshal(map[string]any{"id": source.ID, "proxy": "http://outside.test"})
	if _, err := tool.Func(ctx, args); err == nil {
		t.Fatal("unsupported proxy option accepted")
	}
}

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

func TestWebTestExecuteConcurrency(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
		t.Fatal(err)
	}
	source, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/a?q=old"})
	if err != nil {
		t.Fatal(err)
	}
	var calls, active, peak atomic.Int32
	bothStarted := make(chan struct{})
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		call := calls.Add(1)
		if call > 2 {
			current := active.Add(1)
			defer active.Add(-1)
			for previous := peak.Load(); current > previous && !peak.CompareAndSwap(previous, current); previous = peak.Load() {
			}
			if current == 2 {
				close(bothStarted)
			}
			select {
			case <-bothStarted:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("same")), Request: req}, nil
	})
	result, err := BuildWebTestExecuteTool(catalog, "verifier", transport).Func(context.Background(), json.RawMessage(`{"id":"`+source.ID+`","kind":"concurrency","concurrency":2,"acknowledge_state_change":true}`))
	if err != nil || calls.Load() != 4 || peak.Load() != 2 || result.(map[string]any)["complete"] != true {
		t.Fatalf("bounded overlap failed: %+v calls=%d peak=%d err=%v", result, calls.Load(), peak.Load(), err)
	}
	exchanges, _ := catalog.ListExchanges()
	if len(exchanges) != 5 {
		t.Fatal("concurrency evidence missing")
	}
	for _, ex := range exchanges[1:] {
		if ex.BaselineID != source.ID {
			t.Fatal("missing baseline link")
		}
	}
}

func TestWebTestExecuteUpload(t *testing.T) {
	var body strings.Builder
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("csrf", "original-token"); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("document", "original.txt")
	if err != nil {
		t.Fatal(err)
	}
	file.Write([]byte("original contents"))
	writer.Close()
	catalog := cyber.NewCatalog(t.TempDir())
	catalog.SetAllowedHosts([]string{"example.test"})
	source, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/upload", Method: "POST", ReqHeaders: "Content-Type: " + writer.FormDataContentType(), Request: body.String()})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if err := req.ParseMultipartForm(4000); err != nil {
			t.Fatal(err)
		}
		defer req.MultipartForm.RemoveAll()
		if req.FormValue("csrf") != "original-token" {
			t.Fatal("unselected form field changed")
		}
		file, header, err := req.FormFile("document")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		content, _ := io.ReadAll(file)
		if header.Filename != "covo-probe.txt" || header.Header.Get("Content-Type") != "text/plain" || !strings.HasPrefix(string(content), "covo_upload_") {
			t.Fatalf("unexpected upload content: %s %+v", content, header)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(content))), Request: req}, nil
	})
	result, err := BuildWebTestExecuteTool(catalog, "verifier", transport).Func(context.Background(), json.RawMessage(`{"id":"`+source.ID+`","kind":"upload","file_field":"document","acknowledge_state_change":true}`))
	if err != nil || calls != 1 || result.(map[string]any)["marker_reflected"] != true {
		t.Fatalf("upload failed: %+v err=%v", result, err)
	}
}

func TestWebTestExecuteRejectsUnsafePlans(t *testing.T) {
	for _, option := range []string{`"kind":"concurrency","concurrency":5`, `"kind":"concurrency","acknowledge_state_change":false`, `"kind":"upload","file_field":"absent"`, `"kind":"concurrency","payload":"unexpected"`, `"kind":"script"`} {
		catalog := cyber.NewCatalog(t.TempDir())
		catalog.SetAllowedHosts([]string{"example.test"})
		source, _ := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/a"})
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("unsafe execution reached transport")
			return nil, nil
		})
		ack := `"acknowledge_state_change":true,`
		if strings.Contains(option, "acknowledge_state_change") {
			ack = ""
		}
		args := json.RawMessage(`{"id":"` + source.ID + `",` + ack + option + `}`)
		if _, err := BuildWebTestExecuteTool(catalog, "verifier", transport).Func(context.Background(), args); err == nil {
			t.Fatal("unsafe executor plan accepted", option)
		}
	}
	for _, mode := range []string{"parent", "intake", "redacted", "unscoped", "budget"} {
		catalog := cyber.NewCatalog(t.TempDir())
		if mode != "unscoped" {
			catalog.SetAllowedHosts([]string{"example.test"})
		}
		source, _ := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/a", Redacted: mode == "redacted"})
		if mode == "budget" {
			for count := 0; count < 200; count++ {
				if err := catalog.ConsumeReplayBudget(); err != nil {
					t.Fatal(err)
				}
			}
		}
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("blocked executor reached transport")
			return nil, nil
		})
		ctx := context.Background()
		role := "verifier"
		if mode == "parent" {
			role = "parent"
		}
		if mode == "intake" {
			ctx = toolssubagent.WithIntakeScope(ctx, toolssubagent.IntakeScope{ExchangeID: source.ID})
		}
		if _, err := BuildWebTestExecuteTool(catalog, role, transport).Func(ctx, json.RawMessage(`{"id":"`+source.ID+`","kind":"concurrency","acknowledge_state_change":true}`)); err == nil {
			t.Fatal("blocked executor accepted", mode)
		}
	}
}

func TestWebTestExecutePartialBaseline(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
		t.Fatal(err)
	}
	source, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/a"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("second baseline failed")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("same")), Request: req}, nil
	})
	result, err := BuildWebTestExecuteTool(catalog, "verifier", transport).Func(context.Background(), json.RawMessage(`{"id":"`+source.ID+`","kind":"concurrency","acknowledge_state_change":true}`))
	if err == nil || calls != 2 || result == nil || result.(map[string]any)["baseline"].(map[string]any)["complete"] != false {
		t.Fatalf("partial baseline treated complete: %+v calls=%d err=%v", result, calls, err)
	}
}

func TestWebTestExecutePartialBudget(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	catalog.SetAllowedHosts([]string{"example.test"})
	source, _ := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/a"})
	for used := 0; used < 197; used++ {
		if err := catalog.ConsumeReplayBudget(); err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("same")), Request: req}, nil
	})
	result, err := BuildWebTestExecuteTool(catalog, "verifier", transport).Func(context.Background(), json.RawMessage(`{"id":"`+source.ID+`","kind":"concurrency","concurrency":4,"acknowledge_state_change":true}`))
	if result == nil || result.(map[string]any)["complete"] != false || calls.Load() > 3 {
		t.Fatalf("partial budget failed open: %+v calls=%d err=%v", result, calls.Load(), err)
	}
	if err := catalog.ConsumeReplayBudget(); err == nil {
		t.Fatal("concurrent execution bypassed daily budget")
	}
}

func TestWebWorkflowTokenCookiesAndEvidence(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
		t.Fatal(err)
	}
	page, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/page", Identity: "alice", ReqHeaders: "Cookie: session=original"})
	if err != nil {
		t.Fatal(err)
	}
	submit, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/save", Method: "POST", Identity: "alice", ReqHeaders: "Cookie: session=original\nContent-Type: application/x-www-form-urlencoded", Request: "csrf=old&keep=%2f+%20"})
	if err != nil {
		t.Fatal(err)
	}
	verify, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/check", Identity: "alice", ReqHeaders: "Cookie: session=original"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		headers, body := make(http.Header), "verified"
		switch calls {
		case 1:
			if req.Header.Get("Cookie") != "session=original" {
				t.Fatal("initial session missing")
			}
			headers.Add("Set-Cookie", "session=rotated; Path=/")
			body = `<input name="csrf" value="private-fresh-token">`
		case 2:
			wire, _ := io.ReadAll(req.Body)
			if req.Header.Get("Cookie") != "session=rotated" || string(wire) != "csrf=private-fresh-token&keep=%2f+%20" {
				t.Fatalf("token/session or unchanged field lost: %s %+v", wire, req.Header)
			}
			headers.Add("Set-Cookie", "session=deleted; Max-Age=0; Path=/")
		case 3:
			if req.Header.Get("Cookie") != "" {
				t.Fatal("deleted cookie restored from source")
			}
		}
		return &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	args := json.RawMessage(`{"acknowledge_state_change":true,"steps":[{"id":"` + page.ID + `","extract":{"slot":"csrf","from":"body","name":"csrf"}},{"id":"` + submit.ID + `","bind":{"slot":"csrf","from":"form","name":"csrf"}},{"id":"` + verify.ID + `"}]}`)
	result, err := BuildWebWorkflowTool(catalog, "verifier", transport).Func(context.Background(), args)
	if err != nil || calls != 3 || result.(map[string]any)["complete"] != true {
		t.Fatalf("workflow failed: %+v calls=%d err=%v", result, calls, err)
	}
	exchanges, _ := catalog.ListExchanges()
	if len(exchanges) != 6 {
		t.Fatal("step evidence missing")
	}
	for _, ex := range exchanges[3:] {
		encoded, _ := json.Marshal(ex)
		if !ex.Redacted || ex.BaselineID == "" || strings.Contains(string(encoded), "private-fresh-token") || strings.Contains(string(encoded), "rotated") {
			t.Fatal("workflow persisted token/session", ex)
		}
	}
}

func TestWebWorkflowPreflightAndStop(t *testing.T) {
	for _, mode := range []string{"origin", "identity", "auth_header", "no_ack", "missing_slot", "redacted", "parent", "intake", "status", "token_missing", "scope", "budget", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			catalog := cyber.NewCatalog(t.TempDir())
			if _, err := catalog.SetAllowedHosts([]string{"example.test", "outside.test"}); err != nil {
				t.Fatal(err)
			}
			page, _ := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/page", Identity: "alice", ReqHeaders: "Authorization: first"})
			source := cyber.Exchange{URL: "https://example.test/save", Method: "POST", Identity: "alice", ReqHeaders: "Authorization: first\nContent-Type: application/x-www-form-urlencoded", Request: "csrf=old"}
			switch mode {
			case "origin":
				source.URL = "https://outside.test/save"
			case "identity":
				source.Identity = "bob"
			case "auth_header":
				source.ReqHeaders = "Authorization: other\nContent-Type: application/x-www-form-urlencoded"
			case "redacted":
				source.Redacted = true
			}
			submit, _ := catalog.RecordExchange(source)
			if mode == "budget" {
				for count := 0; count < 199; count++ {
					if err := catalog.ConsumeReplayBudget(); err != nil {
						t.Fatal(err)
					}
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "intake" {
				ctx = toolssubagent.WithIntakeScope(ctx, toolssubagent.IntakeScope{ExchangeID: page.ID})
			}
			calls := 0
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				status, body := 200, `<input name="csrf" value="fresh">`
				switch mode {
				case "status":
					status = 403
				case "token_missing":
					body = "no token"
				case "scope":
					catalog.SetAllowedHosts(nil)
				case "cancel":
					cancel()
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			})
			ack, slot := true, "csrf"
			if mode == "no_ack" {
				ack = false
			}
			if mode == "missing_slot" {
				slot = "missing"
			}
			args, _ := json.Marshal(map[string]any{"acknowledge_state_change": ack, "steps": []webWorkflowStep{{ID: page.ID, Extract: &webWorkflowToken{Slot: "csrf", From: "body", Name: "csrf"}}, {ID: submit.ID, Bind: &webWorkflowToken{Slot: slot, From: "form", Name: "csrf"}}}})
			role := "verifier"
			if mode == "parent" {
				role = "parent"
			}
			result, err := BuildWebWorkflowTool(catalog, role, transport).Func(ctx, args)
			runtimeFailure := mode == "status" || mode == "token_missing" || mode == "scope" || mode == "budget" || mode == "cancel"
			if !runtimeFailure {
				if err == nil || calls != 0 {
					t.Fatalf("invalid plan reached network: mode=%s calls=%d err=%v", mode, calls, err)
				}
				return
			}
			if calls != 1 {
				t.Fatalf("failed workflow continued: mode=%s calls=%d", mode, calls)
			}
			if mode == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancel not propagated", err)
				}
				return
			}
			if err != nil || result.(map[string]any)["complete"] != false {
				t.Fatalf("failure treated complete: %+v err=%v", result, err)
			}
		})
	}
}

func TestWebWorkflowJSONBindingAndRealHTTP(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		calls++
		if req.URL.Path == "/page" {
			writer.Header().Set("Content-Type", "application/json")
			writer.Write([]byte(`{"csrf":"private-json-token"}`))
			return
		}
		if req.URL.Path == "/submit" {
			body, _ := io.ReadAll(req.Body)
			if string(body) != `{"csrf":"private-json-token","id":9007199254740993}` {
				t.Errorf("JSON token binding changed other fields: %s", body)
			}
		}
		writer.Write([]byte("ok"))
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	catalog := cyber.NewCatalog(t.TempDir())
	catalog.SetAllowedHosts([]string{parsed.Hostname()})
	page, _ := catalog.RecordExchange(cyber.Exchange{URL: server.URL + "/page", Identity: "alice"})
	submit, _ := catalog.RecordExchange(cyber.Exchange{URL: server.URL + "/submit", Method: "POST", Identity: "alice", ReqHeaders: "Content-Type: application/json", Request: `{"csrf":"old","id":9007199254740993}`})
	check, _ := catalog.RecordExchange(cyber.Exchange{URL: server.URL + "/check", Identity: "alice"})
	args, _ := json.Marshal(map[string]any{"acknowledge_state_change": true, "steps": []webWorkflowStep{{ID: page.ID, Extract: &webWorkflowToken{Slot: "csrf", From: "body", Name: "csrf"}}, {ID: submit.ID, Bind: &webWorkflowToken{Slot: "csrf", From: "json", Name: "/csrf"}}, {ID: check.ID, ExpectedStatus: 200}}})
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	result, err := BuildWebWorkflowTool(catalog, "verifier", transport).Func(context.Background(), args)
	if err != nil || calls != 3 || result.(map[string]any)["complete"] != true {
		t.Fatalf("real HTTP workflow failed: %+v calls=%d err=%v", result, calls, err)
	}
	if _, err := extractWorkflowToken([]byte(`{"csrf":"first","csrf":"second"}`), nil, "body", "csrf"); err == nil {
		t.Fatal("ambiguous JSON token accepted")
	}
	if workflowSessionHeadersEqual(http.Header{"Cookie": []string{"session=first"}}, http.Header{"Cookie": []string{"session=second"}}) {
		t.Fatal("different captured sessions were mixed")
	}
}

func TestWebAccessMatrixRejectsAmbiguousPlans(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	source, _ := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/a", Identity: "alice", Response: `{"id":1}`, ResponseTruncated: true})
	tool := BuildWebAccessMatrixTool(catalog)
	args := json.RawMessage(`{"exchange_ids":["` + source.ID + `"]}`)
	result, err := tool.Func(context.Background(), args)
	if err != nil || result.(map[string]any)["complete"] != false {
		t.Fatal("truncated evidence reported complete", result, err)
	}
	ctx := toolssubagent.WithIntakeScope(context.Background(), toolssubagent.IntakeScope{ExchangeID: source.ID})
	if _, err := tool.Func(ctx, args); err == nil {
		t.Fatal("intake built cross identity plan")
	}
	for _, bad := range []string{
		`{"exchange_ids":["` + source.ID + `","` + source.ID + `"]}`,
		`{"exchange_ids":["missing"]}`,
		`{"exchange_ids":["` + source.ID + `"],"rules":[{"identity":"alice","exchange_id":"missing","expected":"allow"}]}`,
		`{"exchange_ids":["` + source.ID + `"],"rules":[{"identity":"alice","exchange_id":"` + source.ID + `","expected":"allow"},{"identity":"alice","exchange_id":"` + source.ID + `","expected":"deny"}]}`,
	} {
		if _, err := tool.Func(context.Background(), json.RawMessage(bad)); err == nil {
			t.Fatal("invalid matrix accepted", bad)
		}
	}
}

func TestWebAccessMatrixExplicitExpectations(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	first, err := catalog.RecordExchange(cyber.Exchange{Method: "GET", URL: "https://example.test/orders/1", Identity: "alice", Role: "member", Resource: "order-1", Owner: "alice", Response: `{"id":1,"price":10,"password":"never-output"}`})
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalog.RecordExchange(cyber.Exchange{Method: "GET", URL: "https://example.test/orders/2", Identity: "bob", Role: "admin", Resource: "order-2", Owner: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"exchange_ids": []string{first.ID, second.ID}, "rules": []map[string]string{{"identity": "alice", "exchange_id": second.ID, "expected": "deny"}}})
	result, err := BuildWebAccessMatrixTool(catalog).Func(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	row := result.(map[string]any)
	cases := row["cases"].([]map[string]any)
	if len(cases) != 4 || row["complete"] != true {
		t.Fatalf("matrix missing cases: %+v", row)
	}
	for _, testCase := range cases {
		want := "unknown"
		if testCase["identity"] == "alice" && testCase["source_exchange_id"] == second.ID {
			want = "deny"
		}
		if testCase["expected"] != want {
			t.Fatal("permission inferred from observations", testCase)
		}
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "never-output") || strings.Contains(string(encoded), `"credentials"`) {
		t.Fatal("raw secrets or credentials entered the plan", string(encoded))
	}
	exchanges, _ := catalog.ListExchanges()
	if len(exchanges) != 2 {
		t.Fatal("planning mutated evidence or sent requests")
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

func TestWebParamProbeHTMLProfile(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
		t.Fatal(err)
	}
	source, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/a?q=old&q=keep&view=%2f+%20"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		values := req.URL.Query()["q"]
		if len(values) != 2 || values[1] != "keep" || !strings.HasSuffix(req.URL.RawQuery, "&view=%2f+%20") {
			t.Fatalf("unselected wire values changed: %s", req.URL)
		}
		if calls <= 3 && values[0] != "old" {
			t.Fatal("baseline mutated")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(html.EscapeString(values[0]))), Request: req}, nil
	})
	tool := BuildWebParamProbeTool(catalog, "verifier", transport)
	result, err := tool.Func(context.Background(), json.RawMessage(`{"id":"`+source.ID+`","parameter":"q","profile":"html"}`))
	if err != nil {
		t.Fatal(err)
	}
	row := result.(map[string]any)
	observations := row["observations"].([]map[string]any)
	if calls != 5 || row["replay_attempts"] != 5 || row["complete"] != true || len(observations) != 2 || observations[1]["observation"] != "html_encoded" {
		t.Fatalf("incorrect bounded HTML observations: %+v calls=%d", row, calls)
	}
	exchanges, err := catalog.ListExchanges()
	if err != nil || len(exchanges) != 6 {
		t.Fatalf("missing baseline or probe evidence: %+v err=%v", exchanges, err)
	}
	for _, exchange := range exchanges[1:] {
		if exchange.BaselineID != source.ID {
			t.Fatal("evidence lost original source", exchange)
		}
	}
}

func TestWebParamProbeProfilesRejectAutomaticIntake(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	source, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/a?q=old"})
	if err != nil {
		t.Fatal(err)
	}
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("automatic intake reached classified or repeated probes")
		return nil, nil
	})
	ctx := toolssubagent.WithIntakeScope(context.Background(), toolssubagent.IntakeScope{ExchangeID: source.ID, Endpoint: "GET https://example.test/a"})
	for _, options := range []string{`"profile":"html"`, `"profile":"template"`, `"baseline_samples":3`, `"max_requests":8`} {
		args := json.RawMessage(`{"id":"` + source.ID + `","parameter":"q",` + options + `}`)
		if _, err := BuildWebParamProbeTool(catalog, "verifier", transport).Func(ctx, args); err == nil {
			t.Fatal("automatic intake accepted expanded probe options", options)
		}
	}
}

func TestWebParamProbeTemplateControls(t *testing.T) {
	for _, mode := range []string{"literal", "evaluated", "control_evaluated", "bare_product"} {
		t.Run(mode, func(t *testing.T) {
			catalog := cyber.NewCatalog(t.TempDir())
			if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
				t.Fatal(err)
			}
			source, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/a?q=old"})
			if err != nil {
				t.Fatal(err)
			}
			pattern := regexp.MustCompile(`\{\{([0-9]+)\*([0-9]+)\}\}`)
			if mode == "control_evaluated" {
				pattern = regexp.MustCompile(`([0-9]+)\*([0-9]+)`)
			}
			calls := 0
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				body := req.URL.Query().Get("q")
				if match := pattern.FindStringSubmatch(body); len(match) == 3 && mode != "literal" {
					left, _ := strconv.Atoi(match[1])
					right, _ := strconv.Atoi(match[2])
					product := strconv.Itoa(left * right)
					body = strings.ReplaceAll(body, match[0], product)
					if mode == "bare_product" {
						body = product
					}
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			})
			result, err := BuildWebParamProbeTool(catalog, "verifier", transport).Func(context.Background(), json.RawMessage(`{"id":"`+source.ID+`","parameter":"q","profile":"template"}`))
			if err != nil {
				t.Fatal(err)
			}
			row := result.(map[string]any)
			observations := row["observations"].([]map[string]any)
			if mode == "control_evaluated" {
				if calls != 4 || row["complete"] != false || row["stop_reason"] != "inconclusive_control" {
					t.Fatalf("ambiguous control accepted: %+v calls=%d", row, calls)
				}
				return
			}
			if calls != 7 || row["complete"] != true || len(observations) != 4 {
				t.Fatalf("incomplete template battery: %+v calls=%d", row, calls)
			}
			for _, observation := range observations {
				positive := observation["observation"] == "arithmetic_evaluation_observed"
				if positive != (mode == "evaluated" && observation["probe"] == "double_brace") {
					t.Fatalf("unsupported template conclusion: mode=%s observation=%+v", mode, observation)
				}
			}
			if _, exists := row["vulnerable"]; exists {
				t.Fatal("probe returned a vulnerability verdict")
			}
		})
	}
}

func TestWebParamProbeBaselineNoise(t *testing.T) {
	rows := []map[string]any{
		{"exchange_id": "a", "status": 200, "response_length": 100, "duration_ms": int64(10), "body_sha256": "first"},
		{"exchange_id": "b", "status": 200, "response_length": 110, "duration_ms": int64(20), "body_sha256": "second"},
		{"exchange_id": "c", "status": 200, "response_length": 105, "duration_ms": int64(15), "body_sha256": "third"},
	}
	baseline := summarizeProbeBaseline(rows)
	if baseline["status_stable"] != true || baseline["body_stable"] != false || baseline["response_length_min"] != 100 || baseline["response_length_max"] != 110 || baseline["duration_ms_min"] != int64(10) || baseline["duration_ms_max"] != int64(20) {
		t.Fatal("incorrect baseline noise", baseline)
	}
	comparison := probeSampleComparison(baseline, rows[2])
	if comparison["comparable"] != true || comparison["length_outside_baseline"] != false || comparison["duration_above_baseline"] != false || comparison["body_changed"] != nil {
		t.Fatal("baseline jitter treated as a meaningful difference", comparison)
	}
	rows[2]["response_length"], rows[2]["duration_ms"] = 120, int64(30)
	comparison = probeSampleComparison(baseline, rows[2])
	if comparison["length_outside_baseline"] != true || comparison["duration_above_baseline"] != true {
		t.Fatal("outside-sample observations missing", comparison)
	}
	rows[2]["evidence_clipped"] = true
	if comparison := probeSampleComparison(baseline, rows[2]); comparison["comparable"] != false || comparison["length_outside_baseline"] != nil {
		t.Fatal("clipped response compared", comparison)
	}
}

func TestWebParamProbeClassifiedBoundsAndFailures(t *testing.T) {
	for _, mode := range []string{"request_limit", "daily_budget", "baseline_truncated", "baseline_status", "status_changes", "probe_truncated", "probe_status", "scope_revoked", "cancelled", "transport_failure"} {
		t.Run(mode, func(t *testing.T) {
			catalog := cyber.NewCatalog(t.TempDir())
			if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
				t.Fatal(err)
			}
			source, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/a?q=old"})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "daily_budget" {
				for used := 0; used < 199; used++ {
					if err := catalog.ConsumeReplayBudget(); err != nil {
						t.Fatal(err)
					}
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				body, status := req.URL.Query().Get("q"), 200
				switch {
				case mode == "baseline_truncated", mode == "probe_truncated" && calls == 4:
					body = strings.Repeat("x", 8100)
				case mode == "baseline_status", mode == "probe_status" && calls == 4:
					status = 403
				case mode == "status_changes" && calls == 2:
					status = 201
				case mode == "scope_revoked":
					if _, err := catalog.SetAllowedHosts(nil); err != nil {
						t.Fatal(err)
					}
				case mode == "cancelled":
					cancel()
				case mode == "transport_failure":
					return nil, errors.New("transport unavailable")
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			})
			args := map[string]any{"id": source.ID, "parameter": "q", "profile": "html"}
			if mode == "request_limit" {
				args["max_requests"] = 4
			}
			payload, _ := json.Marshal(args)
			result, err := BuildWebParamProbeTool(catalog, "verifier", transport).Func(ctx, payload)
			if mode == "cancelled" {
				if !errors.Is(err, context.Canceled) || calls != 1 {
					t.Fatalf("cancellation ignored: calls=%d err=%v", calls, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			row := result.(map[string]any)
			expectedCalls, expectedReason := 1, "replay_failed"
			switch mode {
			case "request_limit":
				expectedCalls, expectedReason = 4, "request_limit"
			case "baseline_truncated":
				expectedReason = "inconclusive_baseline_truncated"
			case "baseline_status":
				expectedReason = "inconclusive_baseline_status"
			case "status_changes":
				expectedCalls, expectedReason = 2, "inconclusive_baseline_status_changes"
			case "probe_truncated":
				expectedCalls, expectedReason = 4, "inconclusive_truncated"
			case "probe_status":
				expectedCalls, expectedReason = 4, "inconclusive_status"
			}
			if calls != expectedCalls || row["complete"] != false || row["stop_reason"] != expectedReason {
				t.Fatalf("unsafe or ambiguous run reported complete: %+v calls=%d", row, calls)
			}
			if mode == "daily_budget" || mode == "scope_revoked" {
				if row["baseline"].(map[string]any)["complete"] != false {
					t.Fatal("partial baseline reported complete", row)
				}
			}
		})
	}
}

func TestWebParamProbeClassifiedPreflight(t *testing.T) {
	for _, test := range []struct {
		ex   cyber.Exchange
		args string
	}{
		{cyber.Exchange{}, `"profile":"unknown"`},
		{cyber.Exchange{}, `"profile":"html","baseline_samples":2`},
		{cyber.Exchange{}, `"profile":"html","baseline_samples":6`},
		{cyber.Exchange{}, `"profile":"html","max_requests":13`},
		{cyber.Exchange{}, `"profile":"html","baseline_samples":5,"max_requests":5`},
		{cyber.Exchange{}, `"profile":"html","parameter":"absent"`},
		{cyber.Exchange{Redacted: true}, `"profile":"html"`},
		{cyber.Exchange{HeadersTruncated: true}, `"profile":"html"`},
		{cyber.Exchange{RequestTruncated: true}, `"profile":"html"`},
		{cyber.Exchange{Request: `{"q":"original"}`}, `"profile":"html"`},
		{cyber.Exchange{Method: "POST", ReqHeaders: "Content-Type: application/x-www-form-urlencoded", Request: "q=old"}, `"profile":"html","location":"form"`},
	} {
		catalog := cyber.NewCatalog(t.TempDir())
		if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
			t.Fatal(err)
		}
		test.ex.URL = "https://example.test/a?q=old"
		source, err := catalog.RecordExchange(test.ex)
		if err != nil {
			t.Fatal(err)
		}
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid classified probe reached transport")
			return nil, nil
		})
		parameter := `"parameter":"q",`
		if strings.Contains(test.args, `"parameter"`) {
			parameter = ""
		}
		args := json.RawMessage(`{"id":"` + source.ID + `",` + parameter + test.args + `}`)
		if _, err := BuildWebParamProbeTool(catalog, "verifier", transport).Func(context.Background(), args); err == nil {
			t.Fatal("invalid classified probe accepted", test.args)
		}
	}
}

func TestWebParamProbeClassifiedRealHTTP(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		calls++
		if req.Method != "GET" || req.Header.Get("X-Test-Session") != "chosen" || req.URL.Query().Get("keep") != "full" {
			t.Errorf("request semantics changed: %s %s %+v", req.Method, req.URL, req.Header)
		}
		writer.Header().Set("Content-Type", "text/html")
		writer.Write([]byte("<p>" + req.URL.Query().Get("q") + "</p>"))
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	catalog := cyber.NewCatalog(t.TempDir())
	source, err := catalog.RecordExchange(cyber.Exchange{URL: server.URL + "/page?q=old&keep=full", ReqHeaders: "X-Test-Session: chosen"})
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	tool := BuildWebParamProbeTool(catalog, "verifier", transport)
	args := json.RawMessage(`{"id":"` + source.ID + `","parameter":"q","profile":"html"}`)
	if _, err := tool.Func(context.Background(), args); err == nil || calls != 0 {
		t.Fatalf("classified profile bypassed host permission: calls=%d err=%v", calls, err)
	}
	if _, err := catalog.SetAllowedHosts([]string{parsed.Hostname()}); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildWebParamProbeTool(catalog, "parent", transport).Func(context.Background(), args); err == nil || calls != 0 {
		t.Fatalf("parent performed classified probes: calls=%d err=%v", calls, err)
	}
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	row := result.(map[string]any)
	if calls != 5 || row["complete"] != true || row["observations"].([]map[string]any)[1]["observation"] != "raw_html_observed" {
		t.Fatalf("actual HTTP probe failed: %+v calls=%d", row, calls)
	}
	if row["baseline"].(map[string]any)["body_stable"] != true {
		t.Fatal("unchanged HTTP baselines were not stable", row)
	}
}

func TestWebParamProbeFieldMutation(t *testing.T) {
	query, err := probeValues("id=1&id=2&view=full", "id", 1, "marker")
	if err != nil {
		t.Fatal(err)
	}
	values, _ := url.ParseQuery(query)
	if values["id"][0] != "1" || values["id"][1] != "marker" || values.Get("view") != "full" {
		t.Fatal("unselected values changed", values)
	}
	if _, err := probeValues(query, "id", 2, "marker"); err == nil {
		t.Fatal("missing occurrence accepted")
	}
	wire, err := probeValues("%69d=1&id=2&view=%2f+%20&&flag", "id", 1, "marker")
	if err != nil || wire != "%69d=1&id=marker&view=%2f+%20&&flag" {
		t.Fatalf("unselected wire fields changed: %s err=%v", wire, err)
	}
	body := `{"items":[{"a/b~c":"old"}],"id":9007199254740993,"other":true}`
	mutated, err := probeJSON(body, "/items/0/a~1b~0c", "marker")
	if err != nil || !strings.Contains(mutated, `"a/b~c":"marker"`) || !strings.Contains(mutated, `9007199254740993`) || !strings.Contains(mutated, `"other":true`) {
		t.Fatalf("mutation=%s err=%v", mutated, err)
	}
	for _, pointer := range []string{"", "/absent", "/items/00", "/items/-", "/items", "/items/0/a~2b", "/id/nested"} {
		if _, err := probeJSON(body, pointer, "marker"); err == nil {
			t.Fatalf("invalid pointer %q accepted", pointer)
		}
	}
	for _, ambiguous := range []string{`{"id":1,"id":2}`, `{"id":1,"other":{"x":1,"x":2}}`, `{"id":1,"\u0069d":2}`} {
		if _, err := probeJSON(ambiguous, "/id", "marker"); err == nil {
			t.Fatal("duplicate JSON keys accepted", ambiguous)
		}
	}
}

func TestWebParamProbeStructuredBodies(t *testing.T) {
	for _, location := range []string{"json", "form"} {
		t.Run(location, func(t *testing.T) {
			catalog := cyber.NewCatalog(t.TempDir())
			if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
				t.Fatal(err)
			}
			body, parameter, contentType := `{"variables":{"id":"old"},"keep":9007199254740993}`, "/variables/id", "application/json"
			if location == "form" {
				body, parameter, contentType = "id=old&id=keep&other=full", "id", "application/x-www-form-urlencoded"
			}
			source, err := catalog.RecordExchange(cyber.Exchange{Method: "POST", URL: "https://example.test/a?view=full", ReqHeaders: "Content-Type: " + contentType + "\n", Request: body})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				wire, _ := io.ReadAll(req.Body)
				if req.Method != "POST" || req.URL.RequestURI() != "/a?view=full" || req.Header.Get("Content-Type") != contentType || !strings.Contains(string(wire), "covo_probe_") {
					t.Fatalf("wrong request: %s %s %s", req.Method, req.URL, wire)
				}
				if location == "json" && !strings.Contains(string(wire), "9007199254740993") || location == "form" && !strings.Contains(string(wire), "id=keep") {
					t.Fatalf("unselected value changed: %s", wire)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(wire))), Request: req}, nil
			})
			tool := BuildWebParamProbeTool(catalog, "verifier", transport)
			args, _ := json.Marshal(map[string]any{"id": source.ID, "parameter": parameter, "location": location})
			intakeCtx := toolssubagent.WithIntakeScope(context.Background(), toolssubagent.IntakeScope{ExchangeID: source.ID, Endpoint: "POST https://example.test/a"})
			if _, err := tool.Func(intakeCtx, args); err == nil || calls != 0 {
				t.Fatalf("automatic body probe sent: calls=%d err=%v", calls, err)
			}
			result, err := tool.Func(context.Background(), args)
			if err != nil || calls != 1 || result.(map[string]any)["observation"] != "marker_observed" {
				t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
			}
			exchanges, _ := catalog.ListExchanges()
			if len(exchanges) != 2 || exchanges[1].BaselineID != source.ID {
				t.Fatalf("probe evidence missing: %+v", exchanges)
			}
		})
	}
}

func TestWebParamProbeRejectsInvalidBodyEvidence(t *testing.T) {
	for _, test := range []struct {
		name string
		ex   cyber.Exchange
		args string
	}{
		{"redacted", cyber.Exchange{Redacted: true, ReqHeaders: "Content-Type: application/json", Request: `{"id":1}`}, `"location":"json","parameter":"/id"`},
		{"headers truncated", cyber.Exchange{HeadersTruncated: true, ReqHeaders: "Content-Type: application/json", Request: `{"id":1}`}, `"location":"json","parameter":"/id"`},
		{"body truncated", cyber.Exchange{RequestTruncated: true, ReqHeaders: "Content-Type: application/json", Request: `{"id":1}`}, `"location":"json","parameter":"/id"`},
		{"wrong type", cyber.Exchange{ReqHeaders: "Content-Type: text/plain", Request: `{"id":1}`}, `"location":"json","parameter":"/id"`},
		{"invalid JSON", cyber.Exchange{ReqHeaders: "Content-Type: application/json", Request: `{"id":`}, `"location":"json","parameter":"/id"`},
		{"missing JSON field", cyber.Exchange{ReqHeaders: "Content-Type: application/json", Request: `{"id":1}`}, `"location":"json","parameter":"/absent"`},
		{"duplicate JSON", cyber.Exchange{ReqHeaders: "Content-Type: application/json", Request: `{"id":1,"id":2}`}, `"location":"json","parameter":"/id"`},
		{"JSON index", cyber.Exchange{ReqHeaders: "Content-Type: application/json", Request: `{"id":1}`}, `"location":"json","parameter":"/id","index":1`},
		{"missing form value", cyber.Exchange{ReqHeaders: "Content-Type: application/x-www-form-urlencoded", Request: "id=1"}, `"location":"form","parameter":"id","index":1`},
		{"unknown input", cyber.Exchange{}, `"parameter":"id","payload":"unexpected"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := cyber.NewCatalog(t.TempDir())
			if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
				t.Fatal(err)
			}
			test.ex.Method, test.ex.URL = "POST", "https://example.test/a"
			source, err := catalog.RecordExchange(test.ex)
			if err != nil {
				t.Fatal(err)
			}
			transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid probe reached transport")
				return nil, nil
			})
			args := json.RawMessage(`{"id":"` + source.ID + `",` + test.args + `}`)
			if _, err := BuildWebParamProbeTool(catalog, "verifier", transport).Func(context.Background(), args); err == nil {
				t.Fatal("invalid probe accepted")
			}
		})
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
