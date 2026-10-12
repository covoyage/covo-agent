package cyber

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssessmentMatrixAndCandidates(t *testing.T) {
	dir := t.TempDir()
	catalog := NewCatalog(dir)
	for _, value := range []string{"one.test", "two.test"} {
		if _, err := catalog.UpsertAsset(Asset{Value: value, Kind: "host"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := catalog.UpsertCoverage(Coverage{Target: "one.test", Surface: "/api/orders", RiskClass: "idor", Outcome: "clean"}); err != nil {
		t.Fatal(err)
	}
	matrix, err := catalog.CoverageMatrix()
	if err != nil || len(matrix) != 2 || matrix[0].Outcome != "clean" || matrix[1].Outcome != "untested" {
		t.Fatalf("matrix=%+v err=%v", matrix, err)
	}
	for _, finding := range []Finding{
		{Title: "object access", Target: "one.test", Severity: "high", Status: StatusConfirmed, PoC: "test A", CWE: "CWE-639"},
		{Title: "data exposure", Target: "one.test", Severity: "medium", Status: StatusConfirmed, PoC: "test B", CWE: "CWE-200"},
	} {
		if _, err := catalog.UpsertFinding(finding); err != nil {
			t.Fatal(err)
		}
	}
	candidates, err := catalog.ChainCandidates()
	if err != nil || len(candidates) != 1 || candidates[0].Status != "candidate" {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	if _, err := catalog.SetEngagement(DepthQuick, false); err != nil {
		t.Fatal(err)
	}
	report, err := catalog.FinishScan()
	if err != nil || len(report.CoverageMatrix) != 2 || len(report.ChainCandidates) != 1 || report.UntestedSurfaces == 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	markdown, err := os.ReadFile(filepath.Join(dir, ".covo", "cyber", "scan_report.md"))
	if err != nil || !strings.Contains(string(markdown), "Chain Candidates (unverified)") {
		t.Fatalf("candidate report=%s err=%v", markdown, err)
	}
}

func TestChainCandidatesIgnoreUnconfirmedAndOtherTargets(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	for _, finding := range []Finding{
		{Title: "access", Target: "one.test", Severity: "high", Status: StatusConfirmed, PoC: "proof", CWE: "CWE-639"},
		{Title: "other host", Target: "two.test", Severity: "low", Status: StatusConfirmed, PoC: "proof", CWE: "CWE-200"},
		{Title: "unproven", Target: "one.test", Severity: "low", Status: StatusOpen, CWE: "CWE-200"},
	} {
		if _, err := catalog.UpsertFinding(finding); err != nil {
			t.Fatal(err)
		}
	}
	candidates, err := catalog.ChainCandidates()
	if err != nil || len(candidates) != 0 {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
}

func TestCoverageMatrixPreservesPartialChecks(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		catalog := NewCatalog(t.TempDir())
		items := []Coverage{
			{Target: "one.test", Surface: "/one", RiskClass: "idor", Outcome: "clean"},
			{Target: "one.test", Surface: "/two", RiskClass: "idor", Outcome: "untested"},
		}
		if reverse {
			items[0], items[1] = items[1], items[0]
		}
		for _, item := range items {
			if _, err := catalog.UpsertCoverage(item); err != nil {
				t.Fatal(err)
			}
		}
		matrix, err := catalog.CoverageMatrix()
		if err != nil || len(matrix) != 1 || matrix[0].Outcome != "untested" || matrix[0].FullyTested || matrix[0].TestedSurfaces != 1 || matrix[0].UntestedSurfaces != 1 {
			t.Fatalf("partial check lost: %+v err=%v", matrix, err)
		}
		for _, outcome := range []string{"blocked", "finding", "clean"} {
			if _, err := catalog.UpsertCoverage(Coverage{Target: "one.test", Surface: "/two", RiskClass: "idor", Outcome: outcome}); err != nil {
				t.Fatal(err)
			}
			matrix, err = catalog.CoverageMatrix()
			if err != nil || matrix[0].Outcome != outcome || matrix[0].FullyTested != (outcome != "blocked") {
				t.Fatalf("outcome %s: %+v err=%v", outcome, matrix, err)
			}
		}
	}
}

func TestCoverageEvidenceAndBlockedReport(t *testing.T) {
	dir := t.TempDir()
	catalog := NewCatalog(dir)
	ex, err := catalog.RecordExchange(Exchange{URL: "https://one.test/check"})
	if err != nil {
		t.Fatal(err)
	}
	item := Coverage{Target: "one.test", Surface: "/check", RiskClass: "idor", Outcome: "blocked", ExchangeIDs: []string{ex.ID, ex.ID}}
	saved, err := catalog.UpsertCoverage(item)
	if err != nil || len(saved.ExchangeIDs) != 1 {
		t.Fatalf("evidence not deduplicated: %+v err=%v", saved, err)
	}
	item.ExchangeIDs = []string{"ex-missing"}
	if _, err := catalog.UpsertCoverage(item); err == nil {
		t.Fatal("missing evidence accepted")
	}
	item.ExchangeIDs = make([]string, 21)
	if _, err := catalog.UpsertCoverage(item); err == nil {
		t.Fatal("unbounded references accepted")
	}
	reloaded, err := NewCatalog(dir).ListCoverage()
	if err != nil || len(reloaded) != 1 || reloaded[0].ExchangeIDs[0] != ex.ID {
		t.Fatalf("evidence lost on reload: %+v err=%v", reloaded, err)
	}
	report, err := catalog.FinishScan()
	if err != nil || report.Complete || len(report.CoverageGaps) == 0 || report.CoverageMatrix[0].LinkedSurfaces != 1 {
		t.Fatalf("blocked report claimed complete: %+v err=%v", report, err)
	}
	markdown := renderMarkdownReport(report)
	if !strings.Contains(markdown, "Check Evidence") || !strings.Contains(markdown, "evidence: "+ex.ID) || !strings.Contains(markdown, "blocked=1") {
		t.Fatal("missing check status or evidence", markdown)
	}
	for _, outcome := range []string{"clean", "finding", "blocked", "untested"} {
		sarif := catalog.buildSARIF(ScanReport{Coverage: []Coverage{{Outcome: outcome, ExchangeIDs: []string{ex.ID}}}})
		run := sarif["runs"].([]any)[0].(map[string]any)
		result := run["results"].([]map[string]any)[0]
		expected := map[string]string{"clean": "pass", "finding": "fail", "blocked": "open", "untested": "open"}[outcome]
		if result["kind"] != expected || result["properties"].(map[string]any)["evidence_linked"] != true {
			t.Fatalf("SARIF outcome %s: %+v", outcome, result)
		}
	}
}
