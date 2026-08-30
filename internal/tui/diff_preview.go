package tui

import (
	"path/filepath"
	"strings"

	"github.com/covoyage/covo-agent/internal/diff"
	"github.com/covoyage/covo-agent/internal/diffrender"
	"github.com/covoyage/covo-agent/internal/i18n"
)

// FormatDiffPreviews renders structured file diffs for the chat history.
// Each unified diff is colorized via diffrender (diff-semantic colors plus
// token-level syntax highlighting when COVO_SYNTAX_HIGHLIGHT is on).
func FormatDiffPreviews(previews []diff.FileDiff) string {
	var builder strings.Builder
	for _, preview := range previews {
		if preview.Unified == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		label := preview.Path
		if label == "" {
			label = "file"
		}
		builder.WriteString(i18n.T("system.diff_prefix", "file", filepath.Base(label)))
		for _, line := range strings.Split(diffrender.Colorize(preview.Unified, diffrender.SyntaxEnabled()), "\n") {
			builder.WriteString("\n  ")
			builder.WriteString(line)
		}
	}
	return builder.String()
}
