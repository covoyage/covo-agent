package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/cyber"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
)

func TestOASTToolPrivateCookiesBoundedProviderAndRole(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	calls := 0
	domain := ""
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "provider.test" {
			t.Fatal("provider changed")
		}
		header := http.Header{}
		body := "callback.test"
		if strings.HasSuffix(request.URL.Path, "getdomain.php") {
			header.Add("Set-Cookie", "session=secret; Path=/; Secure; HttpOnly")
		} else {
			cookie, err := request.Cookie("session")
			if err != nil || cookie.Value != "secret" {
				t.Fatal("missing isolated cookie")
			}
			body = `[["` + domain + `","192.0.2.1","time"]]`
		}
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	tool := BuildOASTTool(catalog, transport, "https://provider.test", func() string { return "source" })
	if _, err := tool.Func(context.Background(), []byte(`{"action":"allocate"}`)); err == nil || calls != 0 {
		t.Fatal("parent allocation allowed")
	}
	ctx := toolssubagent.WithSubagentRole(context.Background(), "verifier")
	result, err := tool.Func(ctx, []byte(`{"action":"allocate"}`))
	if err != nil {
		t.Fatal(err)
	}
	session := result.(cyber.OASTSession)
	domain = session.Domain
	args, _ := json.Marshal(map[string]any{"action": "poll", "id": session.ID})
	result, err = tool.Func(ctx, args)
	if err != nil || len(result.(cyber.OASTSession).Observations) != 1 {
		t.Fatal(result, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "secret") {
		t.Fatal("cookie leaked")
	}
	bad := BuildOASTTool(catalog, transport, "http://provider.test", func() string { return "source" })
	if _, err := bad.Func(ctx, []byte(`{"action":"allocate"}`)); err == nil || calls != 2 {
		t.Fatal("plaintext provider allowed")
	}
}

func TestOASTProviderFailuresAndCookieRotation(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	calls := 0
	cookieValue := "first"
	body := "[]"
	code := 200
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		headers := http.Header{}
		if strings.HasSuffix(request.URL.Path, "getdomain.php") {
			headers.Add("Set-Cookie", "session=first; Path=/api; Max-Age=60; Secure; HttpOnly")
			return &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(strings.NewReader("callback.test")), Request: request}, nil
		}
		cookie, err := request.Cookie("session")
		if err != nil || cookie.Value != cookieValue {
			t.Fatal("rotation or scope lost", cookie, err)
		}
		headers.Add("Set-Cookie", "session=rotated; Path=/api; Secure; HttpOnly")
		headers.Set("Location", "https://outside.test")
		return &http.Response{StatusCode: code, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	tool := BuildOASTTool(catalog, transport, "https://provider.test/api", func() string { return "source" })
	ctx := toolssubagent.WithSubagentRole(context.Background(), "verifier")
	result, err := tool.Func(ctx, []byte(`{"action":"allocate"}`))
	if err != nil {
		t.Fatal(err)
	}
	session := result.(cyber.OASTSession)
	if remaining := time.Until(session.ExpiresAt); remaining > time.Minute || remaining <= 0 {
		t.Fatal("cookie expiry not honored", remaining)
	}
	args, _ := json.Marshal(map[string]any{"action": "poll", "id": session.ID})
	if _, err := tool.Func(ctx, args); err != nil {
		t.Fatal(err)
	}
	cookieValue = "rotated"
	for _, failure := range []struct {
		body string
		code int
	}{{"[]", 302}, {"null", 200}, {"broken", 200}, {strings.Repeat("x", 32769), 200}, {`[["incomplete"]]`, 200}} {
		body, code = failure.body, failure.code
		before := calls
		if _, err := tool.Func(ctx, args); err == nil || calls != before+1 {
			t.Fatal("provider failure not isolated", failure.code, err, calls)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	before := calls
	if _, err := tool.Func(cancelled, []byte(`{"action":"allocate"}`)); err == nil || calls != before {
		t.Fatal("pre-cancelled allocation sent")
	}
}
