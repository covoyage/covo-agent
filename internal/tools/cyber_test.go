package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/cyber"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
)

func TestScanListRecipe(t *testing.T) {
	tool := BuildScanTool(t.TempDir(), nil, nil)
	out, err := tool.Func(context.Background(), []byte(`{"recipe":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("list=%#v", out)
	}
	count, _ := m["count"].(int)
	if count < 50 {
		t.Fatalf("count=%v", m["count"])
	}
}

func TestScanToolPassesOptions(t *testing.T) {
	tool := BuildScanTool(t.TempDir(), nil, nil)
	args, _ := json.Marshal(map[string]any{
		"recipe":  "hydra",
		"target":  "10.0.0.8",
		"options": map[string]any{"username": "root", "password": "toor"},
	})
	_, err := tool.Func(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "service") {
		t.Fatalf("expected missing-service error, got %v", err)
	}
}

func TestScanPlanUsesRecipe(t *testing.T) {
	plan, err := cyber.PlanScan(cyber.ScanRequest{Recipe: "curl", Target: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Binary != "curl" || len(plan.Command) < 2 {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestScanRequiresAllowedHost(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	tool := BuildScanTool(t.TempDir(), catalog, nil)
	ctx := toolssubagent.WithSubagentRole(context.Background(), "recon")
	if _, err := tool.Func(ctx, []byte(`{"recipe":"nmap","target":"example.test"}`)); err == nil || !strings.Contains(err.Error(), "allowed_hosts") {
		t.Fatalf("unscoped network scan: %v", err)
	}
	if _, err := catalog.SetAllowedHosts([]string{"example.test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Func(ctx, []byte(`{"recipe":"nmap","target":"other.test"}`)); err == nil || !strings.Contains(err.Error(), "outside allowed_hosts") {
		t.Fatalf("out-of-scope network scan: %v", err)
	}
}

func TestScanResultsRedactCredentialOptions(t *testing.T) {
	options := map[string]string{"password": "pw", "hash": "pw-long-hash", "cookie": "session=secret", "api_key": "key-value", "SMBPass": "hidden-pass", "RHOSTS": "example.test"}
	output := redactScanSecrets("set RHOSTS example.test; -p pw -H pw-long-hash Cookie: session=secret key-value hidden-pass extra-secret auth-value", options,
		"--password=extra-secret", "-H", "Authorization: auth-value")
	for _, secret := range []string{"pw-long-hash", "session=secret", "key-value", "hidden-pass", "extra-secret", "auth-value"} {
		if strings.Contains(output, secret) {
			t.Fatalf("credential leaked in scan result: %s", output)
		}
	}
	if !strings.Contains(output, "example.test") {
		t.Fatalf("scan target was removed: %s", output)
	}
}

func TestScanResultsHideSensitiveTargets(t *testing.T) {
	visible, secrets := safeScanTarget("https://app.test/a?token=secret-value", cyber.RecipeCurl, nil)
	if strings.Contains(visible, "secret-value") || strings.Contains(redactScanSecrets("GET https://app.test/a?token=secret-value", secrets), "secret-value") {
		t.Fatalf("URL query leaked: target=%q", visible)
	}
	visible, secrets = safeScanTarget("header.body.signature", "jwt-analyzer", nil)
	if visible != "[redacted]" || strings.Contains(redactScanSecrets("jwt_tool header.body.signature", secrets), "header.body.signature") {
		t.Fatalf("JWT target leaked: target=%q", visible)
	}
}

func TestNativeHTTPFingerprintRedactsEchoedQuery(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{"app.test"}); err != nil {
		t.Fatal(err)
	}
	target := "https://app.test/a?token=secret-value"
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"/next?token=secret-value"}}, Body: io.NopCloser(strings.NewReader("secret-value")), Request: req}, nil
	})
	result, err := nativeHTTPFingerprint(context.Background(), target, catalog, transport)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "secret-value") {
		t.Fatalf("native HTTP tool result leaked query token: %s", encoded)
	}
}

func TestScanCurlUsesNativeFallbackWithoutBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{"app.test"}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://outside.test/"}}, Body: io.NopCloser(strings.NewReader("moved")), Request: req}, nil
	})
	tool := BuildScanToolWithTransport(t.TempDir(), catalog, nil, transport)
	ctx := toolssubagent.WithSubagentRole(context.Background(), "recon")
	result, err := tool.Func(ctx, []byte(`{"recipe":"curl","target":"https://app.test/a"}`))
	if err != nil {
		t.Fatal(err)
	}
	row := result.(map[string]any)
	if row["backend"] != "native-http" || row["status"] != http.StatusFound || calls != 1 || row["exchange_id"] == nil {
		t.Fatalf("fallback was not bounded evidence: %+v calls=%d", row, calls)
	}
	if _, err := tool.Func(ctx, []byte(`{"recipe":"curl","target":"https://app.test/a","extra":["--head"]}`)); err == nil || calls != 1 {
		t.Fatalf("unsupported flags were silently dropped: calls=%d err=%v", calls, err)
	}
}

func TestFindingTriageTool(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	first, err := catalog.UpsertFinding(cyber.Finding{Title: "first", Target: "app.test", RootCause: "missing ownership", Endpoint: "/a"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalog.UpsertFinding(cyber.Finding{Title: "second", Target: "app.test", RootCause: "missing ownership", Endpoint: "/a"})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"id": second.ID, "decision": "duplicate", "duplicate_of": first.ID})
	result, err := BuildFindingTriageTool(catalog).Func(context.Background(), args)
	if err != nil || result.(cyber.Finding).DuplicateOf != first.ID {
		t.Fatalf("triage result=%+v err=%v", result, err)
	}
}

func TestScanProfileRejectsBadScopeWithoutChangingDepth(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	tool := BuildScanProfileTool(catalog)
	if _, err := tool.Func(context.Background(), []byte(`{"depth":"quick","allowed_hosts":["example.test/path"]}`)); err == nil {
		t.Fatal("invalid scope was accepted")
	}
	eng, err := catalog.GetEngagement()
	if err != nil || eng.Depth != cyber.DepthDeep {
		t.Fatalf("invalid scope changed profile: %+v err=%v", eng, err)
	}
	if _, err := tool.Func(context.Background(), []byte(`{"depth":"standard","allowed_hosts":["example.test"]}`)); err != nil {
		t.Fatal(err)
	}
	eng, err = catalog.GetEngagement()
	if err != nil || eng.Depth != cyber.DepthStandard || len(eng.AllowedHosts) != 1 {
		t.Fatalf("profile did not save scope: %+v err=%v", eng, err)
	}
}

func TestCoverageUpsertPersistsRiskClass(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	tool := BuildCoverageUpsertTool(catalog)
	if _, err := tool.Func(context.Background(), []byte(`{"target":"app.test","surface":"/orders","risk_class":"idor","outcome":"clean"}`)); err != nil {
		t.Fatal(err)
	}
	items, err := catalog.ListCoverage()
	if err != nil || len(items) != 1 || items[0].RiskClass != "idor" {
		t.Fatalf("tool did not persist coverage: %+v err=%v", items, err)
	}
}

func TestFindingUpsertTool(t *testing.T) {
	dir := t.TempDir()
	cat := cyber.NewCatalog(dir)
	tool := BuildFindingUpsertTool(cat)
	args, _ := json.Marshal(map[string]any{
		"title":    "Open admin",
		"target":   "app.example.com",
		"severity": "high",
	})
	out, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := out.(cyber.Finding)
	if !ok || f.ID == "" {
		t.Fatalf("unexpected result: %#v", out)
	}
}

func TestFindingUpsertListsWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	cat := cyber.NewCatalog(dir)
	tool := BuildFindingUpsertTool(cat)
	if _, err := tool.Func(context.Background(), []byte(`{"title":"Open admin","target":"app.example.com"}`)); err != nil {
		t.Fatal(err)
	}
	out, err := tool.Func(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("list result=%#v", out)
	}
	if m["count"] != 1 {
		t.Fatalf("count=%v", m["count"])
	}
}

func TestAssetUpsertTool(t *testing.T) {
	dir := t.TempDir()
	cat := cyber.NewCatalog(dir)
	tool := BuildAssetUpsertTool(cat)
	args, _ := json.Marshal(map[string]any{
		"kind":  "host",
		"value": "app.example.com",
	})
	out, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := out.(cyber.Asset)
	if !ok || a.ID == "" {
		t.Fatalf("unexpected result: %#v", out)
	}
}
