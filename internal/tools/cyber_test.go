package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/cyber"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
	"github.com/covoyage/covonaut/agentcore"
)

func TestFindingRetestUsesVerifierForkWithoutMutatingFinding(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	finding, err := catalog.UpsertFinding(cyber.Finding{Title: "candidate", Target: "example.test", Evidence: "original"})
	if err != nil {
		t.Fatal(err)
	}
	fork := &agentcore.Tool{Func: func(ctx context.Context, args json.RawMessage) (any, error) {
		cyber.RecordRetestSession(ctx, "actual-child-session")
		mutation, _ := json.Marshal(map[string]any{"id": finding.ID, "title": finding.Title, "target": finding.Target, "evidence": "overwrite"})
		if _, err := BuildFindingUpsertTool(catalog).Func(ctx, mutation); err == nil {
			t.Fatal("retest mutated shared finding")
		}
		if _, err := BuildFindingTriageTool(catalog).Func(ctx, []byte(`{"id":"x","decision":"approve"}`)); err == nil {
			t.Fatal("retest triaged shared finding")
		}
		var params map[string]any
		if err := json.Unmarshal(args, &params); err != nil {
			t.Fatal(err)
		}
		if params["role"] != "verifier" || !strings.Contains(params["task"].(string), finding.ID) {
			t.Fatal("missing verifier reference")
		}
		return map[string]any{"output": "new observations", "subagent_id": "child-1"}, nil
	}}
	tool := BuildFindingRetestTool(catalog, fork, func() string { return "source" })
	args, _ := json.Marshal(map[string]any{"finding_id": finding.ID, "task": "verify fix"})
	if _, err := tool.Func(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	records, err := catalog.ListRetests()
	if err != nil || len(records) != 1 || records[0].BranchID != "child-1" || records[0].ChildSessionID != "actual-child-session" || records[0].Results[0].Notes != "new observations" {
		t.Fatal(records, err)
	}
	findings, _ := catalog.ListFindings()
	if findings[0].Evidence != "original" {
		t.Fatal("original evidence changed")
	}
}

func TestCyberStorageApprovalBoundaryAndRetestCancellation(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	storage := BuildCyberStorageTool(catalog, "verifier")
	if _, err := storage.Func(context.Background(), []byte(`{"apply":true}`)); err == nil {
		t.Fatal("child cleanup permitted")
	}
	for _, args := range []string{`{} {}`, `{"unknown":true}`} {
		if _, err := storage.Func(context.Background(), []byte(args)); err == nil {
			t.Fatal("invalid cleanup arguments accepted", args)
		}
	}
	finding, err := catalog.UpsertFinding(cyber.Finding{Title: "candidate", Target: "example.test", Evidence: "original"})
	if err != nil {
		t.Fatal(err)
	}
	fork := &agentcore.Tool{Func: func(ctx context.Context, _ json.RawMessage) (any, error) {
		cyber.RecordRetestSession(ctx, "cancelled-child")
		return map[string]any{"subagent_id": "cancelled-branch", "output": "partial"}, context.Canceled
	}}
	tool := BuildFindingRetestTool(catalog, fork, func() string { return "source" })
	args, _ := json.Marshal(map[string]any{"finding_id": finding.ID, "task": "verify"})
	if _, err := tool.Func(context.Background(), args); err == nil {
		t.Fatal("cancellation swallowed")
	}
	records, err := catalog.ListRetests()
	if err != nil || len(records) != 1 || records[0].Status != "cancelled" || records[0].ChildSessionID != "cancelled-child" || records[0].BranchID != "cancelled-branch" || !strings.Contains(records[0].Results[0].Notes, "partial") {
		t.Fatal(records, err)
	}
}

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
	current, _ := catalog.GetEngagement()
	revision := cyber.ProfileRevision(current)
	invalid, _ := json.Marshal(map[string]any{"revision": revision, "depth": "quick", "allowed_hosts": []string{"example.test/path"}})
	if _, err := tool.Func(context.Background(), invalid); err == nil {
		t.Fatal("invalid scope was accepted")
	}
	eng, err := catalog.GetEngagement()
	if err != nil || eng.Depth != cyber.DepthDeep {
		t.Fatalf("invalid scope changed profile: %+v err=%v", eng, err)
	}
	valid, _ := json.Marshal(map[string]any{"revision": revision, "depth": "standard", "allowed_hosts": []string{"example.test"}})
	if _, err := tool.Func(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Func(context.Background(), valid); err == nil {
		t.Fatal("stale profile accepted")
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

func TestCoverageUpsertEvidenceScope(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	source, err := catalog.RecordExchange(cyber.Exchange{URL: "https://app.test/orders/1"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := catalog.RecordExchange(cyber.Exchange{URL: "https://app.test/orders/2"})
	if err != nil {
		t.Fatal(err)
	}
	tool := BuildCoverageUpsertTool(catalog)
	ctx := toolssubagent.WithIntakeScope(context.Background(), toolssubagent.IntakeScope{ExchangeID: source.ID})
	args, _ := json.Marshal(map[string]any{"target": "app.test", "surface": "/orders/1", "outcome": "clean", "exchange_ids": []string{other.ID}})
	if _, err := tool.Func(ctx, args); err == nil {
		t.Fatal("automatic intake linked other evidence")
	}
	args, _ = json.Marshal(map[string]any{"target": "app.test", "surface": "/orders/1", "outcome": "clean", "exchange_ids": []string{source.ID}})
	if _, err := tool.Func(ctx, args); err != nil {
		t.Fatal(err)
	}
	result, err := BuildWebContextTool(catalog).Func(context.Background(), json.RawMessage(`{"id":"`+source.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	coverage := result.(map[string]any)["coverage"].([]map[string]any)
	if len(coverage) != 1 || coverage[0]["evidence_linked"] != true || coverage[0]["exchange_ids"].([]string)[0] != source.ID {
		t.Fatalf("context lost evidence: %+v", coverage)
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

func TestFindingUpsertEvidenceRecovery(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	tool := BuildFindingUpsertTool(catalog)
	upsert := func(finding cyber.Finding) (cyber.Finding, error) {
		args, err := json.Marshal(finding)
		if err != nil {
			t.Fatal(err)
		}
		result, err := tool.Func(context.Background(), args)
		if err != nil {
			return cyber.Finding{}, err
		}
		return result.(cyber.Finding), nil
	}
	finding := cyber.Finding{Title: "header check", Target: "www.app.test", Evidence: "observed response", Status: cyber.StatusConfirmed, Endpoint: "/api/", Method: "GET"}
	args, err := json.Marshal(finding)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	recovery := result.(map[string]any)
	if recovery["status"] != "proof_required" || recovery["saved"] != false || recovery["requested_status"] != cyber.StatusConfirmed {
		t.Fatalf("missing explicit unsaved recovery: %+v", recovery)
	}
	items, err := catalog.ListFindings()
	if err != nil || len(items) != 0 {
		t.Fatalf("unproven finding was automatically saved: %+v, %v", items, err)
	}
	retry := recovery["suggested_retry"].(map[string]any)
	if retry["status"] != cyber.StatusProofGap || retry["target"] != finding.Target || retry["evidence"] != finding.Evidence || retry["endpoint"] != finding.Endpoint || retry["method"] != finding.Method {
		t.Fatalf("retry changed finding identity or evidence: %+v", retry)
	}
	args, err = json.Marshal(retry)
	if err != nil {
		t.Fatal(err)
	}
	result, err = tool.Func(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	saved := result.(cyber.Finding)
	if saved.Status != cyber.StatusProofGap {
		t.Fatalf("explicit retry was not recorded as proof gap: %+v", saved)
	}
	finding.ID, finding.Status = saved.ID, cyber.StatusConfirmed
	wrong, err := catalog.RecordExchange(cyber.Exchange{URL: "https://app.test/api/", Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	finding.ExchangeID = wrong.ID
	if _, err := upsert(finding); err == nil || !strings.Contains(err.Error(), `exchange host="app.test"`) {
		t.Fatalf("mismatched proof did not identify its host: %v", err)
	}
	items, err = catalog.ListFindings()
	if err != nil || len(items) != 1 || items[0].Status != cyber.StatusProofGap || items[0].ExchangeID != "" || items[0].Evidence != finding.Evidence {
		t.Fatalf("rejected confirmation altered pending evidence: %+v, %v", items, err)
	}
	matching, err := catalog.RecordExchange(cyber.Exchange{URL: "https://www.app.test/api/", Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	finding.ExchangeID = matching.ID
	confirmed, err := upsert(finding)
	if err != nil || confirmed.ID != saved.ID || confirmed.Status != cyber.StatusConfirmed || confirmed.ExchangeID != matching.ID {
		t.Fatalf("matching evidence could not confirm finding: %+v, %v", confirmed, err)
	}
	updated, err := upsert(cyber.Finding{ID: confirmed.ID, Title: confirmed.Title, Target: confirmed.Target, Status: cyber.StatusConfirmed})
	if err != nil || updated.ExchangeID != matching.ID || updated.Status != cyber.StatusConfirmed {
		t.Fatalf("inherited proof incorrectly required resubmission: %+v, %v", updated, err)
	}
}

func TestFindingUpsertRepeatedProofRequiredThroughInvokeTool(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	core := agentcore.New(agentcore.Config{})
	t.Cleanup(core.Close)
	core.RegisterTools(BuildFindingUpsertTool(catalog))
	for _, status := range []string{"confirmed", "verified", "accepted", "ruled_out"} {
		for attempt := 0; attempt < 5; attempt++ {
			args, err := json.Marshal(cyber.Finding{Title: "unproven observation", Target: "app.test", Status: status, PoC: " ", ExchangeID: " "})
			if err != nil {
				t.Fatal(err)
			}
			text, err := core.InvokeTool(context.Background(), "finding_upsert", args)
			if err != nil {
				t.Fatalf("%s attempt %d was a hard execution failure: %v", status, attempt, err)
			}
			var result struct {
				Status          string        `json:"status"`
				Saved           *bool         `json:"saved"`
				RequestedStatus string        `json:"requested_status"`
				NextAction      string        `json:"next_action"`
				SuggestedRetry  cyber.Finding `json:"suggested_retry"`
			}
			if err := json.Unmarshal([]byte(text), &result); err != nil {
				t.Fatalf("model received invalid JSON: %v: %s", err, text)
			}
			expected := cyber.StatusConfirmed
			if status == "ruled_out" {
				expected = cyber.StatusRuledOut
			}
			if result.Status != "proof_required" || result.Saved == nil || *result.Saved || result.RequestedStatus != expected || result.SuggestedRetry.Status != cyber.StatusProofGap || result.SuggestedRetry.Target != "app.test" || !strings.Contains(result.NextAction, "Do not repeat unchanged") {
				t.Fatalf("%s attempt %d missing unsaved recovery: %+v", status, attempt, result)
			}
		}
	}
	items, err := catalog.ListFindings()
	if err != nil || len(items) != 0 {
		t.Fatalf("repeated unproven requests wrote findings: %+v, %v", items, err)
	}
}

func TestFindingUpsertProofRecoveryPreservesHardErrors(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	redacted, err := catalog.RecordExchange(cyber.Exchange{URL: "https://app.test/", Redacted: true})
	if err != nil {
		t.Fatal(err)
	}
	tool := BuildFindingUpsertTool(catalog)
	for _, args := range []string{
		`{`,
		`{"target":"app.test","status":"confirmed"}`,
		`{"title":"missing","target":"app.test","status":"confirmed","exchange_id":"nonexistent"}`,
		`{"title":"redacted","target":"app.test","status":"confirmed","exchange_id":"` + redacted.ID + `"}`,
	} {
		if result, err := tool.Func(context.Background(), json.RawMessage(args)); err == nil {
			t.Fatalf("hard validation failure converted to recovery: %+v", result)
		}
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".covo"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	blocked := BuildFindingUpsertTool(cyber.NewCatalog(home))
	if result, err := blocked.Func(context.Background(), json.RawMessage(`{"title":"storage","target":"app.test","status":"confirmed"}`)); err == nil {
		t.Fatalf("storage failure converted to proof recovery: %+v", result)
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
