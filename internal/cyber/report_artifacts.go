package cyber

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (c *Catalog) writeReportArtifacts(report ScanReport) error {
	if err := c.writeAtomic(filepath.Join(c.dir, "scan_report.md"), []byte(renderMarkdownReport(report))); err != nil {
		return err
	}
	if err := c.writeCSVReport(filepath.Join(c.dir, "findings.csv"), report.Findings); err != nil {
		return err
	}
	sarif, err := json.MarshalIndent(c.buildSARIF(report), "", "  ")
	if err != nil {
		return err
	}
	return c.writeAtomic(filepath.Join(c.dir, "findings.sarif"), sarif)
}

func renderMarkdownReport(report ScanReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Security Scan Report\n\nGenerated: %s\n\n", report.FinishedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Depth: %s\n- Complete: %t\n- Confirmed: %d\n- Open proof gaps: %d\n- Untested surfaces: %d\n\n", report.Depth, report.Complete, report.Confirmed, report.OpenProofGaps, report.UntestedSurfaces)
	if len(report.CoverageGaps) > 0 {
		b.WriteString("## Coverage Gaps\n\n")
		for _, gap := range report.CoverageGaps {
			fmt.Fprintf(&b, "- %s\n", gap)
		}
		b.WriteString("\n")
	}
	if len(report.ChainCandidates) > 0 {
		b.WriteString("## Chain Candidates (unverified)\n\n")
		for _, candidate := range report.ChainCandidates {
			fmt.Fprintf(&b, "- %s on %s: %s (%s)\n", candidate.Pattern, candidate.Target, candidate.Reason, strings.Join(candidate.FindingIDs, ", "))
		}
		b.WriteString("\n")
	}
	b.WriteString("## Findings\n\n")
	for _, finding := range report.Findings {
		fmt.Fprintf(&b, "### %s [%s]\n\nTarget: %s\n\nStatus: %s\n\n", finding.Title, strings.ToUpper(finding.Severity), finding.Target, finding.Status)
		if finding.DuplicateOf != "" {
			fmt.Fprintf(&b, "Duplicate of: %s\n\n", finding.DuplicateOf)
		}
		if finding.File != "" {
			fmt.Fprintf(&b, "Location: `%s:%d`\n\n", finding.File, finding.Line)
		}
		if finding.RootCause != "" {
			fmt.Fprintf(&b, "Root cause: %s\n\n", finding.RootCause)
		}
		if finding.Evidence != "" {
			fence := markdownFence(finding.Evidence)
			fmt.Fprintf(&b, "Evidence:\n\n%s\n%s\n%s\n\n", fence, finding.Evidence, fence)
		}
		if finding.Remediation != "" {
			fmt.Fprintf(&b, "Remediation: %s\n\n", finding.Remediation)
		}
	}
	return b.String()
}

func (c *Catalog) writeCSVReport(path string, findings []Finding) error {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"id", "title", "severity", "status", "duplicate_of", "target", "endpoint", "parameter", "cwe", "cve", "file", "line"})
	for _, finding := range findings {
		_ = w.Write([]string{
			csvSafe(finding.ID), csvSafe(finding.Title), csvSafe(finding.Severity), csvSafe(finding.Status), csvSafe(finding.DuplicateOf),
			csvSafe(finding.Target), csvSafe(finding.Endpoint), csvSafe(finding.Parameter), csvSafe(finding.CWE),
			csvSafe(finding.CVE), csvSafe(finding.File), strconv.Itoa(finding.Line),
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	return c.writeAtomic(path, []byte(b.String()))
}

func csvSafe(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}

func markdownFence(content string) string {
	longest := 2
	for i := 0; i < len(content); {
		if content[i] != '`' {
			i++
			continue
		}
		j := i
		for j < len(content) && content[j] == '`' {
			j++
		}
		if j-i > longest {
			longest = j - i
		}
		i = j
	}
	return strings.Repeat("`", longest+1)
}

func (c *Catalog) buildSARIF(report ScanReport) map[string]any {
	rules := make([]map[string]any, 0, len(report.Findings))
	results := make([]map[string]any, 0, len(report.Findings)+len(report.Coverage))
	seenRules := map[string]bool{}
	for _, finding := range report.Findings {
		if finding.DuplicateOf != "" {
			continue
		}
		ruleID := sarifRuleID(finding)
		if !seenRules[ruleID] {
			seenRules[ruleID] = true
			rules = append(rules, map[string]any{
				"id": ruleID, "name": finding.Title,
				"shortDescription": map[string]any{"text": finding.Title},
				"properties":       map[string]any{"security-severity": sarifSecurityScore(finding)},
			})
		}
		result := map[string]any{
			"ruleId": ruleID, "level": sarifLevel(finding.Severity), "kind": "fail",
			"message":             map[string]any{"text": finding.Title + ": " + finding.Evidence},
			"partialFingerprints": map[string]any{"primaryLocationLineHash": findingFingerprint(finding)},
			"properties":          map[string]any{"severity": finding.Severity, "status": finding.Status, "target": finding.Target, "exchange_id": finding.ExchangeID},
		}
		if location := sarifLocation(finding); location != nil {
			result["locations"] = []any{location}
		}
		results = append(results, result)
	}
	for _, coverage := range report.Coverage {
		kind := "pass"
		if coverage.Outcome == "untested" || coverage.Outcome == "blocked" {
			kind = "open"
		}
		results = append(results, map[string]any{
			"ruleId": "coverage", "kind": kind, "level": "none",
			"message":    map[string]any{"text": coverage.Surface + ": " + coverage.Outcome + " " + coverage.Notes},
			"properties": map[string]any{"coverage": true, "target": coverage.Target},
		})
	}
	run := map[string]any{
		"tool":        map[string]any{"driver": map[string]any{"name": "covo-agent", "rules": rules}},
		"results":     results,
		"invocations": []any{map[string]any{"executionSuccessful": report.Complete, "properties": map[string]any{"coverage_gaps": report.CoverageGaps}}},
	}
	if provenance := c.gitProvenance(); provenance != nil {
		run["versionControlProvenance"] = []any{provenance}
	}
	return map[string]any{"version": "2.1.0", "$schema": "https://json.schemastore.org/sarif-2.1.0.json", "runs": []any{run}}
}

func sarifRuleID(f Finding) string {
	if strings.TrimSpace(f.CWE) != "" {
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, f.CWE)
		if digits != "" {
			return "CWE-" + digits
		}
	}
	if strings.TrimSpace(f.CVE) != "" {
		return strings.ToUpper(strings.TrimSpace(f.CVE))
	}
	return f.ID
}

func sarifLevel(severity string) string {
	switch strings.ToLower(severity) {
	case "critical", "high":
		return "error"
	case "medium":
		return "warning"
	default:
		return "note"
	}
}

func sarifSecurityScore(f Finding) string {
	if f.CVSS > 0 {
		return strconv.FormatFloat(f.CVSS, 'f', 1, 64)
	}
	switch strings.ToLower(f.Severity) {
	case "critical":
		return "9.5"
	case "high":
		return "8.0"
	case "medium":
		return "5.5"
	case "low":
		return "3.0"
	default:
		return "1.0"
	}
}

func sarifLocation(f Finding) map[string]any {
	file := filepath.ToSlash(filepath.Clean(f.File))
	if f.Line > 0 && file != "" && file != "." && !filepath.IsAbs(f.File) && !strings.HasPrefix(file, "../") {
		return map[string]any{"physicalLocation": map[string]any{"artifactLocation": map[string]any{"uri": file}, "region": map[string]any{"startLine": f.Line}}}
	}
	logical := f.Endpoint
	if logical == "" {
		logical = f.Target
	}
	if logical == "" {
		return nil
	}
	return map[string]any{"logicalLocations": []any{map[string]any{"name": logical}}}
}

func findingFingerprint(f Finding) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{CanonicalTarget(f.Target), normalizeIdentity(f.Endpoint), normalizeIdentity(f.Parameter), normalizeIdentity(f.File), strconv.Itoa(f.Line), normalizeIdentity(f.RootCause), normalizeIdentity(f.CVE)}, "|")))
	return hex.EncodeToString(sum[:16])
}

func (c *Catalog) gitProvenance() map[string]any {
	workDir := filepath.Dir(filepath.Dir(c.dir))
	run := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", workDir}, args...)...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	commit, remote := run("rev-parse", "HEAD"), run("config", "--get", "remote.origin.url")
	if commit == "" || remote == "" {
		return nil
	}
	return map[string]any{"repositoryUri": remote, "revisionId": commit, "branch": run("rev-parse", "--abbrev-ref", "HEAD")}
}
