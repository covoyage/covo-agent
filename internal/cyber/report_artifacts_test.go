package cyber

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinishScanWritesReportArtifacts(t *testing.T) {
	dir := t.TempDir()
	catalog := NewCatalog(dir)
	if _, err := catalog.SetEngagement(DepthQuick, false); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.UpsertFinding(Finding{
		Title: "=CSV formula", Target: "example.test", Severity: "high", Status: StatusConfirmed,
		PoC: "curl example.test", Evidence: "```break```", CWE: "89", File: "internal/auth.go", Line: 12,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.FinishScan(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"scan_report.md", "findings.csv", "findings.sarif"} {
		info, err := os.Stat(filepath.Join(dir, ".covo", "cyber", name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want 0600", name, info.Mode().Perm())
		}
	}
	md, _ := os.ReadFile(filepath.Join(dir, ".covo", "cyber", "scan_report.md"))
	if !strings.Contains(string(md), "````\n```break```\n````") {
		t.Fatalf("unsafe markdown fence: %s", md)
	}
	csvData, _ := os.ReadFile(filepath.Join(dir, ".covo", "cyber", "findings.csv"))
	if !strings.Contains(string(csvData), "'=CSV formula") {
		t.Fatalf("unsafe CSV cell: %s", csvData)
	}
	sarifData, _ := os.ReadFile(filepath.Join(dir, ".covo", "cyber", "findings.sarif"))
	var sarif map[string]any
	if err := json.Unmarshal(sarifData, &sarif); err != nil || sarif["version"] != "2.1.0" {
		t.Fatalf("invalid SARIF: version=%v err=%v", sarif["version"], err)
	}
}

func TestReportArtifactsRejectStorageLink(t *testing.T) {
	dir := t.TempDir()
	catalog := NewCatalog(dir)
	if err := catalog.EnsurePrivateStorage(); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(external, []byte("unchanged"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(catalog.dir, "scan_report.md")); err != nil {
		t.Fatal(err)
	}
	if err := catalog.writeReportArtifacts(ScanReport{}); err == nil {
		t.Fatal("report accepted symbolic link")
	}
	data, err := os.ReadFile(external)
	if err != nil || string(data) != "unchanged" {
		t.Fatalf("external report modified: %q, %v", data, err)
	}
}

func TestReportRetainsDuplicateEvidenceWithoutDoubleCounting(t *testing.T) {
	dir := t.TempDir()
	catalog := NewCatalog(dir)
	if _, err := catalog.SetEngagement(DepthQuick, false); err != nil {
		t.Fatal(err)
	}
	first, err := catalog.UpsertFinding(Finding{Title: "first", Target: "app.test", Status: StatusConfirmed, PoC: "proof", RootCause: "missing ownership", Endpoint: "/a"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalog.UpsertFinding(Finding{Title: "second", Target: "app.test", Status: StatusConfirmed, PoC: "proof 2", RootCause: "missing ownership", Endpoint: "/a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.TriageFinding(second.ID, "duplicate", first.ID); err != nil {
		t.Fatal(err)
	}
	report, err := catalog.FinishScan()
	if err != nil || report.Confirmed != 1 || len(report.Findings) != 2 {
		t.Fatalf("duplicate count/report: %+v err=%v", report, err)
	}
	markdown, err := os.ReadFile(filepath.Join(dir, ".covo", "cyber", "scan_report.md"))
	if err != nil || !strings.Contains(string(markdown), "Duplicate of: "+first.ID) {
		t.Fatalf("duplicate audit trail missing: %s err=%v", markdown, err)
	}
	sarif := catalog.buildSARIF(report)
	runs := sarif["runs"].([]any)
	results := runs[0].(map[string]any)["results"].([]map[string]any)
	if len(results) != 1 {
		t.Fatalf("duplicate was included in SARIF findings: %+v", results)
	}
}
