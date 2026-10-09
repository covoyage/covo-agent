package cyber

import "testing"

func TestParseDiffRangesAndValidateFinding(t *testing.T) {
	ranges := parseDiffRanges("+++ b/internal/auth.go\n@@ -10,2 +12,3 @@\n+x\n")
	if len(ranges["internal/auth.go"]) != 1 || ranges["internal/auth.go"][0].End != 14 {
		t.Fatalf("ranges = %#v", ranges)
	}
	catalog := NewCatalog(t.TempDir())
	eng := Engagement{Depth: DepthQuick, ScopeMode: "diff", ChangedRanges: ranges}
	if err := catalog.writeJSON(catalog.engagementPath(), eng); err != nil {
		t.Fatal(err)
	}
	finding := Finding{Title: "auth bypass", Target: "example.test", Severity: "high", Status: StatusProofGap, File: "internal/auth.go", Line: 13}
	if _, err := catalog.UpsertFinding(finding); err != nil {
		t.Fatal(err)
	}
	finding.Line = 20
	if _, err := catalog.UpsertFinding(finding); err == nil {
		t.Fatal("expected out-of-scope line to be rejected")
	}
}
