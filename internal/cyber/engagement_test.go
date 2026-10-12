package cyber

import (
	"strings"
	"sync"
	"testing"
)

func TestProfileConcurrentRevisionHasOneWinner(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	current, err := catalog.GetEngagement()
	if err != nil {
		t.Fatal(err)
	}
	revision := ProfileRevision(current)
	var wait sync.WaitGroup
	errors := make(chan error, 8)
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := catalog.UpdateProfile(revision, DepthQuick, nil, "", nil)
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	wins := 0
	for err := range errors {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("successful saves=%d", wins)
	}
	if _, err := catalog.UpdateProfile(revision, DepthDeep, nil, "", nil); err == nil {
		t.Fatal("stale revision accepted")
	}
}

func TestFindingRequiresPoCWhenConfirmed(t *testing.T) {
	c := NewCatalog(t.TempDir())
	if _, err := c.UpsertFinding(Finding{
		Title: "sqli", Target: "app.example", Severity: "high", Status: "confirmed",
	}); err == nil {
		t.Fatal("confirmed without poc should fail")
	}
	saved, err := c.UpsertFinding(Finding{
		Title: "sqli", Target: "app.example", Severity: "high", Status: "confirmed", PoC: "sqlmap -u http://app",
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != StatusConfirmed {
		t.Fatalf("status=%s", saved.Status)
	}
	if _, err := c.UpsertFinding(Finding{Title: "invalid evidence", Target: "smtp.example", Status: "verified", ExchangeID: "ex-1"}); err == nil {
		t.Fatal("missing exchange id was accepted as proof")
	}
	ex, err := c.RecordExchange(Exchange{URL: "https://smtp.example/"})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := c.UpsertFinding(Finding{
		Title: "relay", Target: "smtp.example", Severity: "medium", Status: "verified", ExchangeID: ex.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Status != StatusConfirmed {
		t.Fatalf("verified should alias confirmed, got %s", legacy.Status)
	}
}

func TestFindingRejectsUnrelatedOrRedactedProof(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	exchange, err := catalog.RecordExchange(Exchange{URL: "https://app.test/orders/1", Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range []Finding{
		{Title: "wrong host", Target: "other.test", Status: StatusConfirmed, ExchangeID: exchange.ID},
		{Title: "wrong path", Target: "app.test", Endpoint: "/users/1", Status: StatusConfirmed, ExchangeID: exchange.ID},
		{Title: "wrong method path", Target: "app.test", Endpoint: "GET /users/1", Status: StatusConfirmed, ExchangeID: exchange.ID},
		{Title: "wrong endpoint verb", Target: "app.test", Endpoint: "POST /orders/1", Status: StatusConfirmed, ExchangeID: exchange.ID},
		{Title: "wrong method", Target: "app.test", Method: "POST", Status: StatusConfirmed, ExchangeID: exchange.ID},
	} {
		if _, err := catalog.UpsertFinding(finding); err == nil {
			t.Fatalf("unrelated proof accepted: %+v", finding)
		}
	}
	redacted, err := catalog.RecordExchange(Exchange{URL: "https://app.test/orders/1", Redacted: true, BaselineID: exchange.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.UpsertFinding(Finding{Title: "unproven", Target: "app.test", Endpoint: "/orders/1", Status: StatusConfirmed, ExchangeID: redacted.ID}); err == nil {
		t.Fatal("redacted exchange alone was accepted as proof")
	}
}

func TestFindingExchangeTargetDiagnostics(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	exchange, err := catalog.RecordExchange(Exchange{URL: "https://www.app.test:443/api/?token=private-query", Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	finding := Finding{Title: "header observation", Target: "app.test", Status: StatusConfirmed, Endpoint: "/api/", Method: "GET", ExchangeID: exchange.ID}
	if _, err := catalog.UpsertFinding(finding); err == nil {
		t.Fatal("bare domain accepted evidence for www host")
	} else {
		for _, text := range []string{`finding target="app.test"`, `exchange host="www.app.test"`, "exchange_get", "not interchangeable"} {
			if !strings.Contains(err.Error(), text) {
				t.Fatalf("missing %q in diagnostic: %v", text, err)
			}
		}
		if strings.Contains(err.Error(), "private-query") {
			t.Fatalf("diagnostic leaked exchange query: %v", err)
		}
	}
	items, err := catalog.ListFindings()
	if err != nil || len(items) != 0 {
		t.Fatalf("rejected finding changed ledger: %+v, %v", items, err)
	}
	for _, target := range []string{"www.app.test", "HTTPS://WWW.APP.TEST:443/other-path"} {
		finding.Target = target
		if _, err := catalog.UpsertFinding(finding); err != nil {
			t.Fatalf("same canonical host rejected for %q: %v", target, err)
		}
	}
}

func TestWhiteboxFloorAndFinish(t *testing.T) {
	c := NewCatalog(t.TempDir())
	if _, err := c.EnsureRuntime("local", "test-engagement", "repo"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetEngagement(DepthDeep, true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetWhiteboxTarget("."); err != nil {
		t.Fatal(err)
	}
	gaps, err := c.WhiteboxGaps()
	if err != nil || len(gaps) != 3 {
		t.Fatalf("gaps=%v err=%v", gaps, err)
	}
	if _, err := c.FinishScan(); err == nil {
		t.Fatal("deep finish should refuse an unmet floor")
	}
	for _, recipe := range []string{"semgrep", "gitleaks", "trivy"} {
		if err := c.RecordScanRun(recipe, ".", true); err != nil {
			t.Fatal(err)
		}
	}
	gaps, err = c.WhiteboxGaps()
	if err != nil || len(gaps) != 0 {
		t.Fatalf("gaps after floor=%v err=%v", gaps, err)
	}
	if err := c.SetWhiteboxTarget(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if gaps, err := c.WhiteboxGaps(); err != nil || len(gaps) != 3 {
		t.Fatalf("other directory accepted prior scans: gaps=%v err=%v", gaps, err)
	}
	if err := c.SetWhiteboxTarget("."); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpsertCoverage(Coverage{Surface: "auth", Target: "app", Outcome: "untested"}); err != nil {
		t.Fatal(err)
	}
	report, err := c.FinishScan()
	if err != nil {
		t.Fatal(err)
	}
	if report.UntestedSurfaces != 1 || report.Complete {
		t.Fatalf("untested=%d complete=%v", report.UntestedSurfaces, report.Complete)
	}
	runtime, err := c.LoadRuntime()
	if err != nil || runtime.Status != "incomplete" {
		t.Fatalf("runtime status=%+v err=%v", runtime, err)
	}
	loaded, err := c.LoadReport()
	if err != nil || loaded.Depth != DepthDeep {
		t.Fatalf("load=%+v err=%v", loaded, err)
	}
}

func TestCoverageKeepsRiskClassesSeparate(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	for _, class := range []string{"idor", "ssrf"} {
		if _, err := catalog.UpsertCoverage(Coverage{Target: "app.test", Surface: "/api", RiskClass: class, Outcome: "clean"}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := catalog.ListCoverage()
	if err != nil || len(items) != 2 || items[0].ID == items[1].ID {
		t.Fatalf("distinct classes were merged: %+v err=%v", items, err)
	}
	if _, err := catalog.UpsertCoverage(Coverage{Target: "app.test", Surface: "/api", RiskClass: "idor", Outcome: "finding"}); err != nil {
		t.Fatal(err)
	}
	items, err = catalog.ListCoverage()
	if err != nil || len(items) != 2 {
		t.Fatalf("repeated class duplicated: %+v err=%v", items, err)
	}
	legacy := Coverage{ID: slugID("c", "legacy.test", "/long-endpoint"), Target: "legacy.test", Surface: "/long-endpoint", RiskClass: "idor", Outcome: "untested"}
	if _, err := catalog.UpsertCoverage(legacy); err != nil {
		t.Fatal(err)
	}
	updated, err := catalog.UpsertCoverage(Coverage{Target: legacy.Target, Surface: legacy.Surface, RiskClass: legacy.RiskClass, Outcome: "clean"})
	if err != nil || updated.ID != legacy.ID {
		t.Fatalf("legacy row was duplicated: %+v err=%v", updated, err)
	}
}

func TestEmptyOrUnmetStandardScanIsIncomplete(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	if _, err := catalog.SetEngagement(DepthQuick, false); err != nil {
		t.Fatal(err)
	}
	report, err := catalog.FinishScan()
	if err != nil || report.Complete || len(report.CoverageGaps) == 0 {
		t.Fatalf("empty scan was marked complete: %+v err=%v", report, err)
	}
	if _, err := catalog.UpsertAsset(Asset{Kind: "host", Value: "app.test"}); err != nil {
		t.Fatal(err)
	}
	report, err = catalog.FinishScan()
	if err != nil || report.Complete || len(report.CoverageGaps) == 0 {
		t.Fatalf("asset-only scan was marked complete: %+v err=%v", report, err)
	}
	if _, err := catalog.SetEngagement(DepthStandard, true); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SetWhiteboxTarget(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	report, err = catalog.FinishScan()
	if err != nil || report.Complete || len(report.WhiteboxGaps) != len(WhiteboxFloorGroups) {
		t.Fatalf("unmet standard floor marked complete: %+v err=%v", report, err)
	}
}

func TestActiveHostScope(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	if err := catalog.CheckActiveTarget("https://app.test/a", TargetURL); err == nil {
		t.Fatal("network target accepted without allowed_hosts")
	}
	if err := catalog.CheckActiveTarget("/tmp/repo", TargetFile); err != nil {
		t.Fatalf("local scan should not need network scope: %v", err)
	}
	if _, err := catalog.SetAllowedHosts([]string{"app.test"}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"https://other.test/a", "https://app.test.evil/a", "https://user@app.test/a"} {
		if err := catalog.CheckActiveTarget(target, TargetURL); err == nil {
			t.Fatalf("out-of-scope target accepted: %s", target)
		}
	}
	if err := catalog.CheckActiveTarget("https://app.test/a", TargetURL); err != nil {
		t.Fatal(err)
	}
}

func TestExchangeIDsAndOrchestratorGate(t *testing.T) {
	c := NewCatalog(t.TempDir())
	ex, err := c.RecordExchange(Exchange{Method: "GET", URL: "https://app.example/", StatusCode: 200, Response: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if ex.ID != "ex-1" {
		t.Fatalf("id=%s", ex.ID)
	}
	if AllowOrchestratorTool("", "scan") {
		t.Fatal("parent must not scan")
	}
	if !AllowOrchestratorTool("recon", "scan") {
		t.Fatal("recon may scan")
	}
	if !AllowOrchestratorTool("", "finding_upsert") {
		t.Fatal("ledger tools stay on the parent")
	}
	if AllowAgentTool("parent", "recon", "execute_code") {
		t.Fatal("parent agent must not expose execute_code")
	}
	if !AllowAgentTool("recon", "", "execute_code") {
		t.Fatal("fixed recon role should expose execute_code without context propagation")
	}
	if len(RoleSkillNames("recon")) == 0 {
		t.Fatal("recon should bind skills")
	}
}
