package lsp

import (
	"fmt"
	"strings"
)

var severityNames = map[int]string{
	1: "ERROR",
	2: "WARN",
	3: "INFO",
	4: "HINT",
}

const (
	defaultSeverity = 1
	maxPerFile      = 20
	maxTotalChars   = 4000
)

func FormatDiagnostic(d Diagnostic) string {
	sev := severityNames[d.Severity]
	if sev == "" {
		sev = "ERROR"
	}
	line := d.Range.Start.Line + 1
	col := d.Range.Start.Character + 1
	msg := strings.TrimSpace(d.Message)
	codePart := ""
	if d.Code != "" {
		codePart = fmt.Sprintf(" [%s]", d.Code)
	}
	sourcePart := ""
	if d.Source != "" {
		sourcePart = fmt.Sprintf(" (%s)", d.Source)
	}
	return fmt.Sprintf("%s [%d:%d] %s%s%s", sev, line, col, msg, codePart, sourcePart)
}

func ReportForFile(filePath string, diagnostics []Diagnostic, severities []int) string {
	if len(diagnostics) == 0 {
		return ""
	}
	sevSet := make(map[int]bool, len(severities))
	if len(severities) == 0 {
		sevSet[defaultSeverity] = true
	}
	for _, s := range severities {
		sevSet[s] = true
	}
	var filtered []Diagnostic
	for _, d := range diagnostics {
		sev := d.Severity
		if sev == 0 {
			sev = defaultSeverity
		}
		if sevSet[sev] {
			filtered = append(filtered, d)
		}
	}
	if len(filtered) == 0 {
		return ""
	}
	limited := filtered
	extra := 0
	if len(limited) > maxPerFile {
		extra = len(limited) - maxPerFile
		limited = limited[:maxPerFile]
	}
	var lines []string
	for _, d := range limited {
		lines = append(lines, FormatDiagnostic(d))
	}
	body := strings.Join(lines, "\n")
	if extra > 0 {
		body += fmt.Sprintf("\n... and %d more", extra)
	}
	result := fmt.Sprintf("<diagnostics file=\"%s\">\n%s\n</diagnostics>", filePath, body)
	if len(result) > maxTotalChars {
		marker := "\n…[truncated]"
		return result[:maxTotalChars-len(marker)] + marker
	}
	return result
}

func Truncate(s string, limit int) string {
	if limit <= 0 {
		limit = maxTotalChars
	}
	if len(s) <= limit {
		return s
	}
	marker := "\n…[truncated]"
	if limit < len(marker) {
		return s[:limit]
	}
	return s[:limit-len(marker)] + marker
}
