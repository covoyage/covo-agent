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
	plain := stripANSIPre(formatted)
	for _, want := range []string{"sample.go", "-old", "+new", "@@ -1 +1 @@"} {
		if !strings.Contains(plain, want) {
			t.Errorf("formatted diff missing %q: %q", want, plain)
		}
	}
	// Diff-semantic coloring must be present (green + line, red - line).
	if !strings.Contains(formatted, "\x1b[32m") || !strings.Contains(formatted, "\x1b[31m") {
		t.Errorf("expected diff-semantic ANSI colors, got %q", formatted)
	}
}

// stripANSIPre removes ANSI escape sequences for content assertions.
func stripANSIPre(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		if r == '\x1b' {
			inEscape = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestFormatDiffPreviewsSkipsEmptyEntries(t *testing.T) {
	formatted := FormatDiffPreviews([]diff.FileDiff{{Path: "empty.go"}})
	if formatted != "" {
		t.Fatalf("empty preview formatted as %q", formatted)
	}
}
