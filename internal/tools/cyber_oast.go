package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/cyber"
	"github.com/covoyage/covo-agent/internal/tools/capability"
	"github.com/covoyage/covonaut/agentcore"
	"golang.org/x/net/publicsuffix"
)

func BuildOASTTool(catalog *cyber.Catalog, transport http.RoundTripper, provider string, source func() string) *agentcore.Tool {
	tool := &agentcore.Tool{Name: "oast", Description: "Allocate or poll isolated DNS interaction evidence through the operator-configured HTTPS DNSLog-compatible provider. Actions: list, allocate, poll. Callbacks are observations, not vulnerability confirmation. Probe delivery uses separately authorized tools.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string", "enum": []string{"list", "allocate", "poll"}}, "id": map[string]any{"type": "string"}, "finding_id": map[string]any{"type": "string"}, "exchange_id": map[string]any{"type": "string"}}, "additionalProperties": false}}
	tool.Func = func(ctx context.Context, args json.RawMessage) (any, error) {
		var params struct {
			Action     string `json:"action"`
			ID         string `json:"id"`
			FindingID  string `json:"finding_id"`
			ExchangeID string `json:"exchange_id"`
		}
		decoder := json.NewDecoder(bytes.NewReader(args))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&params); err != nil {
			return nil, err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("OAST expects one JSON object")
		}
		if catalog == nil {
			return nil, fmt.Errorf("OAST catalog unavailable")
		}
		if params.Action == "" || params.Action == "list" {
			return catalog.ListOAST()
		}
		if params.Action != "allocate" && params.Action != "poll" {
			return nil, fmt.Errorf("unknown OAST action")
		}
		if !cyber.AllowOrchestratorTool(subagentRole(ctx), "oast") {
			return nil, fmt.Errorf("OAST network operations require a spawned hands-on role")
		}
		base, err := url.Parse(provider)
		if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Opaque != "" {
			return nil, fmt.Errorf("configure COVO_OAST_PROVIDER with a trusted HTTPS DNSLog-compatible base URL")
		}
		if transport == nil || source == nil {
			return nil, fmt.Errorf("OAST requires safe transport and current source session")
		}
		sourceID := source()
		if sourceID == "" {
			return nil, fmt.Errorf("OAST requires a current source session")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
		client := &http.Client{Transport: transport, Jar: jar, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		path := "getdomain.php"
		var session cyber.OASTSession
		var existingCookies []*http.Cookie
		if params.Action == "poll" {
			session, existingCookies, err = catalog.OASTCredentials(params.ID, provider)
			if err != nil {
				return nil, err
			}
			path = "getrecords.php"
		}
		endpoint := *base
		endpoint.Path = strings.TrimRight(base.Path, "/") + "/" + path
		endpoint.RawPath = ""
		if params.Action == "poll" {
			jar.SetCookies(&endpoint, existingCookies)
			if len(jar.Cookies(&endpoint)) == 0 {
				return nil, fmt.Errorf("OAST provider session cookies expired or are not usable")
			}
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, err
		}
		if err := catalog.ConsumeReplayBudget(); err != nil {
			return nil, err
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("OAST provider request failed")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("OAST provider returned HTTP %d; redirects are not followed", response.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 32769))
		if err != nil || len(data) > 32768 {
			return nil, fmt.Errorf("OAST provider response exceeded budget or failed")
		}
		cookies, err := oastProviderCookies(existingCookies, response.Cookies(), jar, &endpoint)
		if err != nil {
			return nil, err
		}
		if params.Action == "allocate" {
			return catalog.BeginOAST(provider, string(data), sourceID, params.FindingID, params.ExchangeID, cookies)
		}
		var rows [][]string
		if err := json.Unmarshal(data, &rows); err != nil || rows == nil || len(rows) > 100 {
			return nil, fmt.Errorf("invalid or oversized OAST provider records")
		}
		observations := make([]cyber.OASTObservation, 0, len(rows))
		for _, row := range rows {
			if len(row) != 3 {
				return nil, fmt.Errorf("provider records must contain name, remote IP and timestamp")
			}
			observations = append(observations, cyber.OASTObservation{Name: row[0], RemoteAddress: row[1], ProviderTime: row[2]})
		}
		return catalog.RecordOAST(session.ID, observations, cookies)
	}
	capability.Declare(tool, capability.Metadata{Kind: capability.Execute})
	return tool
}

func oastProviderCookies(existing, updated []*http.Cookie, jar *cookiejar.Jar, endpoint *url.URL) ([]*http.Cookie, error) {
	byName := map[string]*http.Cookie{}
	for _, cookie := range existing {
		byName[cookie.Name] = cookie
	}
	for _, cookie := range updated {
		copyCookie := *cookie
		if copyCookie.Path == "" {
			copyCookie.Path = path.Dir(endpoint.Path)
			if copyCookie.Path == "." {
				copyCookie.Path = "/"
			}
		}
		if copyCookie.MaxAge > 0 {
			copyCookie.Expires = time.Now().Add(time.Duration(copyCookie.MaxAge) * time.Second)
		}
		byName[cookie.Name] = &copyCookie
	}
	cookies := make([]*http.Cookie, 0, len(byName))
	for _, selected := range jar.Cookies(endpoint) {
		cookie := byName[selected.Name]
		if cookie == nil || cookie.Value != selected.Value || len(cookie.Name)+len(cookie.Value) > 4096 {
			return nil, fmt.Errorf("invalid provider cookie selection")
		}
		cookies = append(cookies, cookie)
	}
	if len(cookies) == 0 || len(cookies) > 16 {
		return nil, fmt.Errorf("provider must return isolated usable session cookies")
	}
	return cookies, nil
}
