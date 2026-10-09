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
