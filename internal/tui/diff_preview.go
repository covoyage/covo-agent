package tui

import (
	"path/filepath"
	"strings"

	"github.com/covoyage/covonaut/tui/theme"

	"github.com/covoyage/covo-agent/internal/diff"
	"github.com/covoyage/covo-agent/internal/i18n"
)

// FormatDiffPreviews renders structured file diffs for the chat history.
func FormatDiffPreviews(previews []diff.FileDiff) string {
	palette := theme.CurrentPalette()
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
		for _, line := range strings.Split(preview.Unified, "\n") {
			builder.WriteString("\n")
			switch {
			case strings.HasPrefix(line, "+"):
				builder.WriteString(palette.Success.Render("  " + line))
			case strings.HasPrefix(line, "-"):
				builder.WriteString(palette.Error.Render("  " + line))
			default:
				builder.WriteString(palette.Dim.Render("  " + line))
			}
		}
	}
	return builder.String()
}
