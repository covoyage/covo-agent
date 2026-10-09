package cyber

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Scan depths bound how far an engagement is expected to go.
const (
	DepthQuick    = "quick"
	DepthStandard = "standard"
	DepthDeep     = "deep"
)

// Proof statuses. confirmed and ruled_out require a PoC or an exchange id.
const (
	StatusOpen          = "open"
	StatusConfirmed     = "confirmed"
	StatusRuledOut      = "ruled_out"
	StatusProofGap      = "open_proof_gap"
	StatusVerified      = "verified" // legacy alias of confirmed
	StatusFalsePositive = "false-positive"
	StatusAccepted      = "accepted"
)

// HandsOnRoles may run scan and payload-bearing shell commands.
// The parent orchestrator must delegate those calls.
var HandsOnRoles = map[string]bool{
	"recon": true, "verifier": true, "exploiter": true, "privesc": true,
	"lateral": true, "persistence": true, "impact": true, "opsec": true,
	"cleanup": true,
}

// RoleSkills are injected into a spawned role so the playbook is in
// context before the first tool call. At most five names.
var RoleSkills = map[string][]string{
	"recon":       {"attack-surface-recon", "component-vuln-intel", "source-code-hunting"},
	"verifier":    {"pentest-verification", "web-attack-methods", "pentest-output-standards"},
	"exploiter":   {"web-attack-methods", "specialized-attack-playbooks", "post-exploitation"},
	"privesc":     {"post-exploitation", "specialized-attack-playbooks"},
	"lateral":     {"active-directory-attack", "post-exploitation", "cloud-attack-methods"},
	"persistence": {"post-exploitation", "redteam-opsec"},
	"impact":      {"pentest-output-standards", "post-exploitation"},
	"opsec":       {"redteam-opsec"},
	"cleanup":     {"post-exploitation", "pentest-output-standards"},
	"reporter":    {"pentest-output-standards", "pentest-verification"},
}

// WhiteboxFloorRecipes must have a recorded run before a local-repository
// recon pass is complete. The first match in each group satisfies that group.
var WhiteboxFloorGroups = [][]string{
	{"semgrep"},
	{"gitleaks", "trufflehog"},
	{"trivy"},
}

type Coverage struct {
	ID        string    `json:"id"`
	Surface   string    `json:"surface"`
	Target    string    `json:"target"`
	RiskClass string    `json:"risk_class,omitempty"`
	Outcome   string    `json:"outcome"`
	Notes     string    `json:"notes,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type ScanRun struct {
	Recipe string    `json:"recipe"`
	Target string    `json:"target"`
	ExitOK bool      `json:"exit_ok"`
	At     time.Time `json:"at"`
}

type Engagement struct {
	Depth          string                 `json:"depth,omitempty"`
	Whitebox       bool                   `json:"whitebox,omitempty"`
	WhiteboxTarget string                 `json:"whitebox_target,omitempty"`
	AllowedHosts   []string               `json:"allowed_hosts,omitempty"`
	ScopeMode      string                 `json:"scope_mode,omitempty"`
	BaseRef        string                 `json:"base_ref,omitempty"`
	HeadRef        string                 `json:"head_ref,omitempty"`
	ChangedRanges  map[string][]LineRange `json:"changed_ranges,omitempty"`
	UpdatedAt      time.Time              `json:"updated_at,omitempty"`
}

type ScanReport struct {
	Depth            string             `json:"depth,omitempty"`
	Whitebox         bool               `json:"whitebox"`
	WhiteboxGaps     []string           `json:"whitebox_gaps,omitempty"`
	Findings         []Finding          `json:"findings,omitempty"`
	Assets           []Asset            `json:"assets,omitempty"`
	Coverage         []Coverage         `json:"coverage,omitempty"`
	CoverageMatrix   []CoverageCell     `json:"coverage_matrix,omitempty"`
	ChainCandidates  []ChainCandidate   `json:"chain_candidates,omitempty"`
	Confirmed        int                `json:"confirmed"`
	OpenProofGaps    int                `json:"open_proof_gaps"`
	UntestedSurfaces int                `json:"untested_surfaces"`
	Complete         bool               `json:"complete"`
	CoverageGaps     []string           `json:"coverage_gaps,omitempty"`
	AgentRuns        []AgentObservation `json:"agent_runs,omitempty"`
	FinishedAt       time.Time          `json:"finished_at"`
}

func (c *Catalog) engagementPath() string { return filepath.Join(c.dir, "engagement.json") }
func (c *Catalog) coveragePath() string   { return filepath.Join(c.dir, "coverage.json") }
func (c *Catalog) scanRunsPath() string   { return filepath.Join(c.dir, "scan_runs.json") }
func (c *Catalog) reportPath() string     { return filepath.Join(c.dir, "scan_report.json") }

func (c *Catalog) SetEngagement(depth string, whitebox bool) (Engagement, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var e Engagement
	if err := c.readJSON(c.engagementPath(), &e); err != nil && !os.IsNotExist(err) {
		return Engagement{}, err
	}
	e.Depth = normalizeDepth(depth)
	e.Whitebox = whitebox
	e.UpdatedAt = time.Now().UTC()
	if err := c.writeJSON(c.engagementPath(), e); err != nil {
		return Engagement{}, err
	}
	return e, nil
}

func (c *Catalog) GetEngagement() (Engagement, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var e Engagement
	if err := c.readJSON(c.engagementPath(), &e); err != nil {
		if os.IsNotExist(err) {
			return Engagement{Depth: DepthDeep}, nil
		}
		return Engagement{}, err
	}
	if e.Depth == "" {
		e.Depth = DepthDeep
	}
	return e, nil
}

func (c *Catalog) SetWhiteboxTarget(target string) error {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = resolved
	}
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var engagement Engagement
	if err := c.readJSON(c.engagementPath(), &engagement); err != nil && !os.IsNotExist(err) {
		return err
	}
	engagement.WhiteboxTarget = absolute
	engagement.UpdatedAt = time.Now().UTC()
	return c.writeJSON(c.engagementPath(), engagement)
}

func (c *Catalog) SetAllowedHosts(hosts []string) (Engagement, error) {
	allowed := make([]string, 0, len(hosts))
	seen := map[string]bool{}
	for _, raw := range hosts {
		host := normalizeIntakeHost(raw)
		if host == "" {
			return Engagement{}, fmt.Errorf("invalid allowed host %q", raw)
		}
		if !seen[host] {
			allowed = append(allowed, host)
			seen[host] = true
		}
	}
	sort.Strings(allowed)
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var eng Engagement
	if err := c.readJSON(c.engagementPath(), &eng); err != nil && !os.IsNotExist(err) {
		return Engagement{}, err
	}
	eng.AllowedHosts = allowed
	eng.UpdatedAt = time.Now().UTC()
	return eng, c.writeJSON(c.engagementPath(), eng)
}

func (c *Catalog) CheckActiveTarget(target string, kind TargetKind) error {
	if kind != TargetHost && kind != TargetURL {
		return nil
	}
	eng, err := c.GetEngagement()
	if err != nil {
		return err
	}
	if len(eng.AllowedHosts) == 0 {
		return fmt.Errorf("network testing requires allowed_hosts in scan_profile")
	}
	raw := strings.TrimSpace(target)
	if kind == TargetHost {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil ||
		(kind == TargetURL && parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(kind == TargetHost && (parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "")) {
		return fmt.Errorf("invalid network scan target %q", target)
	}
	for _, host := range eng.AllowedHosts {
		if strings.EqualFold(parsed.Hostname(), host) {
			return nil
		}
	}
	return fmt.Errorf("target host %q is outside allowed_hosts", parsed.Hostname())
}

func (c *Catalog) CheckReplayTarget(target string) error {
	eng, err := c.GetEngagement()
	if err != nil {
		return err
	}
	if len(eng.AllowedHosts) > 0 {
		return c.CheckActiveTarget(target, TargetURL)
	}
	intake, err := c.IntakeConfig()
	if err != nil {
		return err
	}
	if intake.Enabled {
		parsed, err := url.Parse(target)
		if err == nil && parsed.User == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") {
			for _, host := range intake.Hosts {
				if strings.EqualFold(parsed.Hostname(), host) {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("replay target requires allowed_hosts or enabled request_intake for this host")
}

func (c *Catalog) RecordScanRun(recipe, target string, exitOK bool) error {
	recipe = strings.ToLower(strings.TrimSpace(recipe))
	if recipe == "" || recipe == "list" {
		return nil
	}
	run := ScanRun{Recipe: recipe, Target: strings.TrimSpace(target), ExitOK: exitOK, At: time.Now().UTC()}
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var runs []ScanRun
	if err := c.readJSON(c.scanRunsPath(), &runs); err != nil && !os.IsNotExist(err) {
		return err
	}
	runs = append(runs, run)
	if len(runs) > 400 {
		runs = runs[len(runs)-400:]
	}
	return c.writeJSON(c.scanRunsPath(), runs)
}

func (c *Catalog) ScanRuns() ([]ScanRun, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var runs []ScanRun
	if err := c.readJSON(c.scanRunsPath(), &runs); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return runs, nil
}

func (c *Catalog) UpsertCoverage(item Coverage) (Coverage, error) {
	item.Surface = strings.TrimSpace(item.Surface)
	item.Target = strings.TrimSpace(item.Target)
	item.RiskClass = strings.ToLower(strings.TrimSpace(item.RiskClass))
	item.Outcome = normalizeOutcome(item.Outcome)
	if item.Surface == "" {
		return Coverage{}, fmt.Errorf("coverage surface is required")
	}
	if item.Target == "" {
		item.Target = "*"
	}
	generatedID := item.ID == ""
	if item.ID == "" {
		sum := sha256.Sum256([]byte(item.Target + "\x00" + item.Surface + "\x00" + item.RiskClass))
		item.ID = fmt.Sprintf("c-%x", sum[:8])
	}
	item.UpdatedAt = time.Now().UTC()

	catalogMu.Lock()
	defer catalogMu.Unlock()
	items, err := c.loadCoverageLocked()
	if err != nil {
		return Coverage{}, err
	}
	replaced := false
	for i, existing := range items {
		if existing.ID == item.ID || (generatedID && existing.Target == item.Target && existing.Surface == item.Surface && existing.RiskClass == item.RiskClass) {
			if existing.ID == item.ID && (existing.Target != item.Target || existing.Surface != item.Surface || existing.RiskClass != item.RiskClass) {
				return Coverage{}, fmt.Errorf("coverage id %q belongs to a different surface or risk class", item.ID)
			}
			item.ID = existing.ID
			items[i] = item
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if err := c.writeJSON(c.coveragePath(), items); err != nil {
		return Coverage{}, err
	}
	return item, nil
}

func (c *Catalog) ListCoverage() ([]Coverage, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	return c.loadCoverageLocked()
}

func (c *Catalog) loadCoverageLocked() ([]Coverage, error) {
	var items []Coverage
	if err := c.readJSON(c.coveragePath(), &items); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return items, nil
}

// WhiteboxGaps reports floor groups with no successful scan run.
// An empty slice means the floor is met (or whitebox is off).
func (c *Catalog) WhiteboxGaps() ([]string, error) {
	eng, err := c.GetEngagement()
	if err != nil {
		return nil, err
	}
	if !eng.Whitebox || eng.Depth == DepthQuick {
		return nil, nil
	}
	runs, err := c.ScanRuns()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, run := range runs {
		absolute, err := filepath.Abs(run.Target)
		if err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			absolute = resolved
		}
		if run.ExitOK && eng.WhiteboxTarget != "" && absolute == eng.WhiteboxTarget {
			seen[run.Recipe] = true
		}
	}
	var gaps []string
	for _, group := range WhiteboxFloorGroups {
		ok := false
		for _, name := range group {
			if seen[name] {
				ok = true
				break
			}
		}
		if !ok {
			gaps = append(gaps, strings.Join(group, "|"))
		}
	}
	return gaps, nil
}

// FinishScan writes scan_report.json. depth deep refuses to finish while
// the whitebox floor is unmet.
func (c *Catalog) FinishScan() (ScanReport, error) {
	findings, err := c.ListFindings()
	if err != nil {
		return ScanReport{}, err
	}
	assets, err := c.ListAssets()
	if err != nil {
		return ScanReport{}, err
	}
	coverage, err := c.ListCoverage()
	if err != nil {
		return ScanReport{}, err
	}
	matrix, err := c.CoverageMatrix()
	if err != nil {
		return ScanReport{}, err
	}
	chains, err := c.ChainCandidates()
	if err != nil {
		return ScanReport{}, err
	}
	observations, err := c.ListAgentObservations()
	if err != nil {
		return ScanReport{}, err
	}
	eng, err := c.GetEngagement()
	if err != nil {
		return ScanReport{}, err
	}
	gaps, err := c.WhiteboxGaps()
	if err != nil {
		return ScanReport{}, err
	}
	if eng.Depth == DepthDeep && len(gaps) > 0 {
		return ScanReport{}, fmt.Errorf("whitebox floor unmet: %s", strings.Join(gaps, ", "))
	}
	report := ScanReport{
		Depth:           eng.Depth,
		Whitebox:        eng.Whitebox,
		WhiteboxGaps:    gaps,
		Findings:        findings,
		Assets:          assets,
		Coverage:        coverage,
		CoverageMatrix:  matrix,
		ChainCandidates: chains,
		AgentRuns:       observations,
		CoverageGaps:    machineCoverageGaps(coverage, observations),
		Complete:        observationsComplete(observations),
		FinishedAt:      time.Now().UTC(),
	}
	if len(coverage) == 0 && len(observations) == 0 {
		report.CoverageGaps = append(report.CoverageGaps, "no coverage or agent checks recorded")
	}
	for _, f := range findings {
		if f.DuplicateOf != "" {
			continue
		}
		switch normalizeStatus(f.Status) {
		case StatusConfirmed, StatusVerified, StatusAccepted:
			report.Confirmed++
		case StatusProofGap, StatusOpen:
			report.OpenProofGaps++
		}
	}
	for _, item := range coverage {
		if item.Outcome == "untested" || item.Outcome == "" {
			report.UntestedSurfaces++
		}
	}
	for _, cell := range matrix {
		if cell.Outcome == "untested" && len(cell.Surfaces) == 0 {
			report.UntestedSurfaces++
			report.CoverageGaps = append(report.CoverageGaps, cell.Target+": "+cell.RiskClass+" untested")
		}
	}
	if len(report.CoverageGaps) > 0 || report.UntestedSurfaces > 0 || len(report.WhiteboxGaps) > 0 {
		report.Complete = false
	}
	catalogMu.Lock()
	err = c.writeJSON(c.reportPath(), report)
	catalogMu.Unlock()
	if err != nil {
		return ScanReport{}, err
	}
	if err := c.writeReportArtifacts(report); err != nil {
		return ScanReport{}, err
	}
	runtimeStatus := "completed"
	if !report.Complete {
		runtimeStatus = "incomplete"
	}
	_ = c.UpdateRuntimeStatus(runtimeStatus)
	return report, nil
}

func (c *Catalog) LoadReport() (ScanReport, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var report ScanReport
	if err := c.readJSON(c.reportPath(), &report); err != nil {
		return ScanReport{}, err
	}
	return report, nil
}

// NeedsPoC reports whether status cannot be saved without a PoC or exchange id.
func NeedsPoC(status string) bool {
	switch normalizeStatus(status) {
	case StatusConfirmed, StatusVerified, StatusRuledOut, StatusAccepted:
		return true
	default:
		return false
	}
}

func ValidateFindingProof(f Finding) error {
	if !NeedsPoC(f.Status) {
		return nil
	}
	if strings.TrimSpace(f.PoC) != "" || strings.TrimSpace(f.ExchangeID) != "" {
		return nil
	}
	return fmt.Errorf("status %q requires poc or exchange_id; use open_proof_gap when reproduction is incomplete", normalizeStatus(f.Status))
}

func RoleSkillNames(role string) []string {
	names := RoleSkills[strings.ToLower(strings.TrimSpace(role))]
	if len(names) > 5 {
		return append([]string(nil), names[:5]...)
	}
	return append([]string(nil), names...)
}

// OrchestratorToolBlocked is the hands-on tool set the parent must not call.
func OrchestratorToolBlocked(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "scan", "bash", "execute_code", "sandbox", "exchange_replay", "exchange_compare", "web_param_probe", "csrf_retest", "remote_node_exec":
		return true
	default:
		return false
	}
}

func AllowOrchestratorTool(role, toolName string) bool {
	if !OrchestratorToolBlocked(toolName) {
		return true
	}
	return HandsOnRoles[strings.ToLower(strings.TrimSpace(role))]
}

// AllowAgentTool prefers the role fixed on an agent instance and falls back to
// the call context for direct invocation compatibility.
func AllowAgentTool(instanceRole, contextRole, toolName string) bool {
	role := strings.TrimSpace(instanceRole)
	if role == "" {
		role = strings.TrimSpace(contextRole)
	}
	return AllowOrchestratorTool(role, toolName)
}

func normalizeDepth(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case DepthQuick, DepthStandard, DepthDeep:
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return DepthDeep
	}
}

func normalizeOutcome(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "clean", "finding", "blocked", "untested":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		if strings.TrimSpace(v) == "" {
			return "untested"
		}
		return "untested"
	}
}

func LooksLikeRepo(target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return false
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return false
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err == nil {
		return true
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return false
	}
	for _, e := range entries {
		switch strings.ToLower(e.Name()) {
		case "go.mod", "package.json", "pyproject.toml", "cargo.toml", "pom.xml", "gemfile":
			return true
		}
	}
	return false
}

// FormatCoverage renders a short coverage summary for the volatile prompt.
func FormatCoverage(items []Coverage) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Coverage ledger:\n")
	limit := len(items)
	if limit > 40 {
		limit = 40
	}
	for _, it := range items[:limit] {
		fmt.Fprintf(&b, "- %s [%s] %s", it.Surface, it.Outcome, it.Target)
		if it.Notes != "" {
			fmt.Fprintf(&b, " (%s)", it.Notes)
		}
		b.WriteString("\n")
	}
	if len(items) > limit {
		fmt.Fprintf(&b, "... %d more\n", len(items)-limit)
	}
	return strings.TrimSpace(b.String())
}
