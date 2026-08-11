// Package evolution provides OSV (Open Source Vulnerabilities) integration.
// It queries the OSV.dev API to check whether skill dependencies have known
// vulnerabilities, supporting PyPI, npm, Go, Maven, crates.io, and RubyGems.
package evolution

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/safego"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// OsvVulnerability represents a single vulnerability from OSV.
type OsvVulnerability struct {
	ID         string   `json:"id"`
	Summary    string   `json:"summary"`
	Severity   string   `json:"severity"` // CRITICAL, HIGH, MEDIUM, LOW
	FixedIn    string   `json:"fixed_in,omitempty"`
	References []string `json:"references,omitempty"`
}

// OsvPackageQuery describes a package to check for vulnerabilities.
type OsvPackageQuery struct {
	Ecosystem string
	Name      string
	Version   string
}

// ---------------------------------------------------------------------------
// HTTP client (shared across all calls)
// ---------------------------------------------------------------------------

// osvClient is initialised once with a 10 s timeout.
var osvClient = &http.Client{
	Timeout: 10 * time.Second,
}

const osvBatchURL = "https://api.osv.dev/v1/querybatch"

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// OsvCheck queries the OSV.dev API for vulnerabilities affecting a single
// package version.
//
//	ecosystem  – one of "PyPI", "npm", "Go", "Maven", "crates.io", "RubyGems"
//	packageName – package identifier (e.g. "requests", "lodash")
//	version     – version string (e.g. "2.28.1", "1.2.3")
func OsvCheck(ecosystem, packageName, version string) ([]OsvVulnerability, error) {
	results, err := OsvCheckBatch([]OsvPackageQuery{
		{Ecosystem: ecosystem, Name: packageName, Version: version},
	})
	if err != nil {
		return nil, err
	}
	key := pkgKey(ecosystem, packageName)
	return results[key], nil
}

// OsvCheckBatch queries multiple packages in parallel (max 10 concurrent) and
// returns a map from "ecosystem:name" keys to their vulnerability lists.
func OsvCheckBatch(packages []OsvPackageQuery) (map[string][]OsvVulnerability, error) {
	if len(packages) == 0 {
		return map[string][]OsvVulnerability{}, nil
	}

	// Build the JSON request body.
	type pkgObj struct {
		Name      string `json:"name"`
		Ecosystem string `json:"ecosystem"`
	}
	type queryObj struct {
		Package pkgObj `json:"package"`
		Version string `json:"version"`
	}
	type batchReq struct {
		Queries []queryObj `json:"queries"`
	}

	req := batchReq{
		Queries: make([]queryObj, 0, len(packages)),
	}
	for _, p := range packages {
		if p.Name == "" {
			continue
		}
		req.Queries = append(req.Queries, queryObj{
			Package: pkgObj{Name: p.Name, Ecosystem: p.Ecosystem},
			Version: p.Version,
		})
	}
	if len(req.Queries) == 0 {
		return map[string][]OsvVulnerability{}, nil
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("osv: marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, osvBatchURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("osv: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := osvClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("osv: api call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("osv: api returned %d: %s", resp.StatusCode, string(msg))
	}

	// The batch response is {"results": [ {...per-query...} ]}
	type batchResp struct {
		Results []json.RawMessage `json:"results"`
	}
	var apiResp batchResp
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("osv: decode response: %w", err)
	}

	// Parse each per-query result.
	out := make(map[string][]OsvVulnerability, len(packages))
	for i, raw := range apiResp.Results {
		if i >= len(req.Queries) {
			break
		}
		q := req.Queries[i]
		vulns := parseSingleResult(raw)
		if len(vulns) == 0 {
			continue
		}
		key := pkgKey(q.Package.Ecosystem, q.Package.Name)
		out[key] = vulns
	}

	return out, nil
}

// OsvCheckBatchParallel queries multiple packages using a bounded parallelism
// of 10 concurrent single-package calls. This is the fallback when you prefer
// individual calls over the batch endpoint.
func OsvCheckBatchParallel(packages []OsvPackageQuery) (map[string][]OsvVulnerability, error) {
	if len(packages) == 0 {
		return map[string][]OsvVulnerability{}, nil
	}

	sem := make(chan struct{}, 10)
	var mu sync.Mutex
	out := make(map[string][]OsvVulnerability, len(packages))
	var wg sync.WaitGroup
	var firstErr error

	for _, p := range packages {
		if p.Name == "" {
			continue
		}
		wg.Add(1)
		q := p
		safego.SafeGo(func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			vulns, err := OsvCheck(q.Ecosystem, q.Name, q.Version)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
				return
			}
			if len(vulns) > 0 {
				key := pkgKey(q.Ecosystem, q.Name)
				out[key] = vulns
			}
		}, nil)
	}
	wg.Wait()

	return out, firstErr
}

// ---------------------------------------------------------------------------
// Parsers for package-manifest files
// ---------------------------------------------------------------------------

// lineRE matches a requirement line: package-name followed by version
// specifiers. Examples: "requests==2.28.1", "flask>=2.0,<3.0",
// "django==4.2" (from pip freeze).
var lineRE = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_.-]*)\s*([><=!~]+\s*[\d.]+(?:\s*,\s*[><=!~]+\s*[\d.]+)*)`)

// ParseRequirementsTxt extracts package names and versions from a
// requirements.txt (pip) content string. Lines that don't match the
// "name==version" format are skipped.
func ParseRequirementsTxt(content string) []OsvPackageQuery {
	var queries []OsvPackageQuery
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == '-' {
			continue
		}
		// Handle inline comments.
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		m := lineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		version := strings.TrimSpace(m[2])
		// For "==", strip the operator to get the bare version.
		version = strings.TrimPrefix(version, "==")
		version = strings.TrimSpace(version)
		queries = append(queries, OsvPackageQuery{
			Ecosystem: "PyPI",
			Name:      name,
			Version:   version,
		})
	}
	return queries
}

// ParsePackageJson extracts package names and versions from a package.json
// content. It reads both "dependencies" and "devDependencies" blocks.
// Version ranges (^, ~) are left as-is because OSV understands semver ranges.
func ParsePackageJson(content string) []OsvPackageQuery {
	var raw struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil
	}

	var queries []OsvPackageQuery
	add := func(deps map[string]string) {
		for name, version := range deps {
			version = strings.TrimSpace(version)
			if version == "" || version == "*" {
				continue
			}
			// Strip leading ^ ~ = >= <= > < to get a base version for the
			// query (OSV accepts ranges, but cleaner to send the minimum).
			version = stripSemverRange(version)
			queries = append(queries, OsvPackageQuery{
				Ecosystem: "npm",
				Name:      name,
				Version:   version,
			})
		}
	}
	add(raw.Dependencies)
	add(raw.DevDependencies)
	return queries
}

// stripSemverRange removes common semver range prefixes (^, ~, >=, >, <=, <,
// =) from a version string so we send a concrete base version to OSV.
func stripSemverRange(v string) string {
	v = strings.TrimPrefix(v, "^")
	v = strings.TrimPrefix(v, "~")
	v = strings.TrimPrefix(v, ">=")
	v = strings.TrimPrefix(v, "<=")
	v = strings.TrimPrefix(v, ">")
	v = strings.TrimPrefix(v, "<")
	v = strings.TrimPrefix(v, "=")
	return strings.TrimSpace(v)
}

// ---------------------------------------------------------------------------
// Report formatting
// ---------------------------------------------------------------------------

// FormatOsvReport produces a human-readable vulnerability report from the
// results map. Packages with zero vulnerabilities are omitted.
func FormatOsvReport(results map[string][]OsvVulnerability) string {
	if len(results) == 0 {
		return "No vulnerabilities found."
	}

	// Sort keys for deterministic output.
	keys := make([]string, 0, len(results))
	for k := range results {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("OSV Vulnerability Report\n")
	b.WriteString(strings.Repeat("=", 72))
	b.WriteByte('\n')

	total := 0
	bySev := map[string]int{"CRITICAL": 0, "HIGH": 0, "MEDIUM": 0, "LOW": 0}

	for _, key := range keys {
		vulns := results[key]
		if len(vulns) == 0 {
			continue
		}
		total += len(vulns)
		b.WriteString(fmt.Sprintf("\n📦 %s  (%d vuln%s)\n", key, len(vulns), plural(len(vulns))))
		for _, v := range vulns {
			sev := v.Severity
			if sev == "" {
				sev = "UNKNOWN"
			}
			bySev[sev]++

			fixedNote := ""
			if v.FixedIn != "" {
				fixedNote = fmt.Sprintf(" [fixed in %s]", v.FixedIn)
			}
			b.WriteString(fmt.Sprintf("  %-7s  %-20s  %s%s\n", sev, v.ID, v.Summary, fixedNote))
		}
	}

	b.WriteByte('\n')
	b.WriteString(strings.Repeat("-", 72))
	b.WriteByte('\n')
	b.WriteString(fmt.Sprintf("Total: %d vulnerability(s) across %d package(s)\n", total, len(keys)))
	b.WriteString(fmt.Sprintf("Breakdown: CRITICAL=%d  HIGH=%d  MEDIUM=%d  LOW=%d\n",
		bySev["CRITICAL"], bySev["HIGH"], bySev["MEDIUM"], bySev["LOW"]))
	b.WriteString(strings.Repeat("=", 72))
	b.WriteByte('\n')

	return b.String()
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// pkgKey returns a stable map key for an ecosystem + package combination.
func pkgKey(ecosystem, name string) string {
	return ecosystem + ":" + name
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ---------------------------------------------------------------------------
// OSV response parsing
// ---------------------------------------------------------------------------

// osvVulnEntry mirrors the per-vulnerability structure in the OSV response.
type osvVulnEntry struct {
	ID        string        `json:"id"`
	Summary   string        `json:"summary"`
	Details   string        `json:"details"`
	Aliases   []string      `json:"aliases"`
	Modified  string        `json:"modified"`
	Published string        `json:"published"`
	Affected  []osvAffected `json:"affected"`
	// database_specific may carry a severity object.
	DatabaseSpecific json.RawMessage `json:"database_specific"`
}

type osvAffected struct {
	Package osvAffectedPackage `json:"package"`
	Ranges  []osvRange         `json:"ranges"`
	// Some ecosystems report severity at the affected level.
	DatabaseSpecific json.RawMessage `json:"database_specific"`
}

type osvAffectedPackage struct {
	Name      string `json:"name"`
	Ecosystem string `json:"ecosystem"`
	Purl      string `json:"purl"`
}

type osvRange struct {
	Type   string     `json:"type"` // "GIT", "SEMVER", "ECOSYSTEM"
	Events []osvEvent `json:"events"`
}

type osvEvent struct {
	Introduced string `json:"introduced"`
	Fixed      string `json:"fixed"`
	Limit      string `json:"limit"`
}

// parseSingleResult takes the raw JSON for one batch-result entry and converts
// it to a slice of OsvVulnerability.
func parseSingleResult(raw json.RawMessage) []OsvVulnerability {
	// Each result can be either:
	//   {"vulns": [...]}  — when vulnerabilities are found
	//   {}                — when none were found (err field may also appear)
	var wrapper struct {
		Vulns []osvVulnEntry `json:"vulns"`
		Err   string         `json:"err"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil
	}
	if len(wrapper.Vulns) == 0 {
		return nil
	}

	out := make([]OsvVulnerability, 0, len(wrapper.Vulns))
	for _, ve := range wrapper.Vulns {
		ov := OsvVulnerability{
			ID:      ve.ID,
			Summary: ve.Summary,
		}
		// Summary may be empty; fall back to details or aliases.
		if ov.Summary == "" {
			ov.Summary = ve.Details
		}
		if ov.Summary == "" && len(ve.Aliases) > 0 {
			ov.Summary = strings.Join(ve.Aliases, ", ")
		}

		// Extract severity.
		ov.Severity = extractSeverity(ve)

		// Extract fixed version from ranges.
		ov.FixedIn = extractFixedVersion(ve.Affected)

		// Gather references (aliases as CVE/GHSA refs).
		if len(ve.Aliases) > 0 {
			ov.References = append(ov.References, ve.Aliases...)
		}

		out = append(out, ov)
	}
	return out
}

// extractSeverity attempts to find a severity string from the vulnerability
// entry. Priority: database_specific.severity → affected[].database_specific
// → derive from CVSS score in database_specific.
func extractSeverity(ve osvVulnEntry) string {
	// Try top-level database_specific.
	if sev := severityFromRaw(ve.DatabaseSpecific); sev != "" {
		return sev
	}
	// Try affected-level database_specific.
	for _, a := range ve.Affected {
		if sev := severityFromRaw(a.DatabaseSpecific); sev != "" {
			return sev
		}
	}
	return "UNKNOWN"
}

// severityFromRaw tries to extract a severity label from an arbitrary JSON
// blob (database_specific). Many OSV ecosystems embed severity here.
func severityFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Common shapes:
	// {"severity": "HIGH"}
	// {"cvss": {"score": 9.8}}  — map numeric score → label
	// {"severity": [{"type": "CVSS_V3", "score": "9.8"}]}  — GitHub Advisory

	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}

	// Direct severity string.
	if sevBytes, ok := m["severity"]; ok {
		// Try as string first.
		var s string
		if json.Unmarshal(sevBytes, &s) == nil {
			return strings.ToUpper(s)
		}
	}

	// CVSS score object: {"cvss": {"score": 9.8}}
	if cvss, ok := m["cvss"]; ok {
		var cvssObj struct {
			Score float64 `json:"score"`
		}
		if json.Unmarshal(cvss, &cvssObj) == nil && cvssObj.Score > 0 {
			return cvssToSeverity(cvssObj.Score)
		}
	}

	return ""
}

// extractFixedVersion walks the affected ranges and collects the earliest
// fixed version from SEMVER/ECOSYSTEM-type ranges.
func extractFixedVersion(affected []osvAffected) string {
	var fixes []string
	for _, a := range affected {
		for _, r := range a.Ranges {
			for _, e := range r.Events {
				if e.Fixed != "" {
					fixes = append(fixes, e.Fixed)
				}
			}
		}
	}
	if len(fixes) == 0 {
		return ""
	}
	sort.Strings(fixes)
	return strings.Join(fixes, ", ")
}

// cvssToSeverity converts a CVSS v3 numeric score to a severity label.
func cvssToSeverity(score float64) string {
	switch {
	case score >= 9.0:
		return "CRITICAL"
	case score >= 7.0:
		return "HIGH"
	case score >= 4.0:
		return "MEDIUM"
	case score > 0:
		return "LOW"
	default:
		return "UNKNOWN"
	}
}
