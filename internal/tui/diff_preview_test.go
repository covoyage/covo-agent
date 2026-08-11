package tui

import (
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/diff"
)

func TestFormatDiffPreviews(t *testing.T) {
	formatted := FormatDiffPreviews([]diff.FileDiff{
		{Path: "src/sample.go", Unified: "--- a/sample.go\n+++ b/sample.go\n@@ -1 +1 @@\n-old\n+new"},
	})
	for _, want := range []string{"sample.go", "-old", "+new", "@@ -1 +1 @@"} {
		if !strings.Contains(formatted, want) {
			t.Errorf("formatted diff missing %q: %q", want, formatted)
		}
	}
}

func TestFormatDiffPreviewsSkipsEmptyEntries(t *testing.T) {
	formatted := FormatDiffPreviews([]diff.FileDiff{{Path: "empty.go"}})
	if formatted != "" {
		t.Fatalf("empty preview formatted as %q", formatted)
	}
}
