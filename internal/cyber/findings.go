package cyber

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Finding struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Severity    string    `json:"severity"`
	Target      string    `json:"target"`
	Evidence    string    `json:"evidence,omitempty"`
	PoC         string    `json:"poc,omitempty"`
	ExchangeID  string    `json:"exchange_id,omitempty"`
	Status      string    `json:"status,omitempty"`
	RootCause   string    `json:"root_cause,omitempty"`
	Endpoint    string    `json:"endpoint,omitempty"`
	Method      string    `json:"method,omitempty"`
	Parameter   string    `json:"parameter,omitempty"`
	CWE         string    `json:"cwe,omitempty"`
	CVE         string    `json:"cve,omitempty"`
	CVSS        float64   `json:"cvss,omitempty"`
	File        string    `json:"file,omitempty"`
	Line        int       `json:"line,omitempty"`
	Remediation string    `json:"remediation,omitempty"`
	SimilarTo   string    `json:"similar_to,omitempty"`
	DuplicateOf string    `json:"duplicate_of,omitempty"`
	Triage      string    `json:"triage,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

type Asset struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Value     string    `json:"value"`
	Notes     string    `json:"notes,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type Catalog struct {
	dir string
}

// catalogMu serializes ledger read-modify-write across every Catalog in this
// process. Spawned subagents build their own Catalog but inherit the workDir,
// so a per-instance mutex leaves parent and child free to clobber each other's
// findings.json. Two separate covo-agent processes on one workDir are still
// only protected against torn reads, not lost updates.
var catalogMu sync.Mutex

func NewCatalog(workDir string) *Catalog {
	if strings.TrimSpace(workDir) == "" {
		workDir = "."
	}
	return &Catalog{dir: filepath.Join(workDir, ".covo", "cyber")}
}

func (c *Catalog) EnsurePrivateStorage() error {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	root, err := c.openStorage(true)
	if err != nil {
		return err
	}
	defer root.Close()
	return ensurePrivateRoot(root)
}

func ensurePrivateRoot(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Chmod(0o700); err != nil {
		return err
	}
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("cyber storage contains symbolic link %q", entry.Name())
		}
		if entry.IsDir() {
			child, err := root.OpenRoot(entry.Name())
			if err != nil {
				return err
			}
			err = ensurePrivateRoot(child)
			child.Close()
			if err != nil {
				return err
			}
			continue
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("cyber storage contains non-regular file %q", entry.Name())
		}
		file, err := root.Open(entry.Name())
		if err != nil {
			return err
		}
		err = file.Chmod(0o600)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func (c *Catalog) openStorage(create bool) (*os.Root, error) {
	workspace, err := os.OpenRoot(filepath.Dir(filepath.Dir(c.dir)))
	if err != nil {
		return nil, err
	}
	defer workspace.Close()
	for _, name := range []string{".covo", filepath.Join(".covo", "cyber")} {
		info, err := workspace.Lstat(name)
		if os.IsNotExist(err) && create {
			if err = workspace.Mkdir(name, 0o700); err != nil && !os.IsExist(err) {
				return nil, err
			}
			info, err = workspace.Lstat(name)
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("cyber storage path %q must be a directory, not a symbolic link", name)
		}
	}
	root, err := workspace.OpenRoot(filepath.Join(".covo", "cyber"))
	if err != nil {
		return nil, err
	}
	if create {
		dir, err := root.Open(".")
		if err != nil {
			root.Close()
			return nil, err
		}
		err = dir.Chmod(0o700)
		dir.Close()
		if err != nil {
			root.Close()
			return nil, err
		}
	}
	return root, nil
}

func storageFileName(root *os.Root, path string) (string, error) {
	name := filepath.Base(path)
	info, err := root.Lstat(name)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("cyber storage file %q must be regular, not a symbolic link", name)
	}
	return name, nil
}

func (c *Catalog) findingsPath() string { return filepath.Join(c.dir, "findings.json") }
func (c *Catalog) assetsPath() string   { return filepath.Join(c.dir, "assets.json") }

func (c *Catalog) UpsertFinding(f Finding) (Finding, error) {
	f.Title = strings.TrimSpace(f.Title)
	f.Target = CanonicalTarget(f.Target)
	f.Severity = normalizeSeverity(f.Severity)
	f.Status = normalizeStatus(f.Status)
	f.PoC = strings.TrimSpace(f.PoC)
	f.ExchangeID = strings.TrimSpace(f.ExchangeID)
	if f.Title == "" {
		return Finding{}, fmt.Errorf("finding title is required")
	}
	if f.Target == "" {
		return Finding{}, fmt.Errorf("finding target is required")
	}
	if err := c.validateDiffFinding(f); err != nil {
		return Finding{}, err
	}
	f.UpdatedAt = time.Now().UTC()

	catalogMu.Lock()
	defer catalogMu.Unlock()
	items, err := c.loadFindingsLocked()
	if err != nil {
		return Finding{}, err
	}
	for _, existing := range items {
		if existing.ID != f.ID {
			continue
		}
		if existing.Target != f.Target ||
			(existing.Endpoint != "" && f.Endpoint != "" && existing.Endpoint != f.Endpoint) ||
			(existing.Method != "" && f.Method != "" && !strings.EqualFold(existing.Method, f.Method)) {
			return Finding{}, fmt.Errorf("finding identity cannot change under the same id; create a new finding")
		}
		if f.Endpoint == "" {
			f.Endpoint = existing.Endpoint
		}
		if f.Method == "" {
			f.Method = existing.Method
		}
		f = mergeFindingEvidence(existing, f)
		break
	}
	if err := ValidateFindingProof(f); err != nil {
		return Finding{}, err
	}
	if f.ExchangeID != "" {
		exchanges, err := c.loadExchangesLocked()
		if err != nil {
			return Finding{}, err
		}
		var ex Exchange
		found := false
		for _, item := range exchanges {
			if item.ID == f.ExchangeID {
				ex, found = item, true
				break
			}
		}
		if !found {
			return Finding{}, fmt.Errorf("finding exchange %q not found", f.ExchangeID)
		} else if CanonicalTarget(ex.URL) != f.Target {
			return Finding{}, fmt.Errorf("finding exchange %q has a different target", f.ExchangeID)
		} else if f.Method != "" && !strings.EqualFold(f.Method, ex.Method) {
			return Finding{}, fmt.Errorf("finding exchange %q has a different method", f.ExchangeID)
		} else if NeedsPoC(f.Status) && ex.Redacted && f.PoC == "" {
			return Finding{}, fmt.Errorf("redacted exchange %q needs independent proof to confirm or rule out", f.ExchangeID)
		} else {
			endpoint := strings.TrimSpace(f.Endpoint)
			if method, path, ok := strings.Cut(endpoint, " "); ok && strings.HasPrefix(path, "/") {
				if !strings.EqualFold(method, ex.Method) {
					return Finding{}, fmt.Errorf("finding exchange %q has a different endpoint method", f.ExchangeID)
				}
				endpoint = path
			}
			if strings.HasPrefix(endpoint, "/") && !strings.ContainsAny(endpoint, "{*}") {
				parsed, err := url.Parse(ex.URL)
				if err != nil || parsed.EscapedPath() != endpoint {
					return Finding{}, fmt.Errorf("finding exchange %q has a different endpoint", f.ExchangeID)
				}
			}
		}
	}
	if f.ID == "" {
		f.ID = generatedFindingID(f)
		if similar := semanticFindingID(f, items); similar != "" && similar != f.ID {
			f.SimilarTo = similar
			f.Triage = "pending"
		}
	}
	replaced := false
	for i, existing := range items {
		if existing.ID == f.ID {
			items[i] = f
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, f)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if err := c.writeJSON(c.findingsPath(), items); err != nil {
		return Finding{}, err
	}
	return f, nil
}

func (c *Catalog) UpsertAsset(a Asset) (Asset, error) {
	a.Value = strings.TrimSpace(a.Value)
	a.Kind = strings.ToLower(strings.TrimSpace(a.Kind))
	if a.Kind == "" {
		a.Kind = "host"
	}
	if a.Value == "" {
		return Asset{}, fmt.Errorf("asset value is required")
	}
	if a.Kind == "host" || a.Kind == "url" || a.Kind == "domain" {
		if canon := CanonicalTarget(a.Value); canon != "" {
			a.Value = canon
		}
	}
	if a.ID == "" {
		a.ID = slugID("a", a.Kind, a.Value)
	}
	a.UpdatedAt = time.Now().UTC()

	catalogMu.Lock()
	defer catalogMu.Unlock()
	items, err := c.loadAssetsLocked()
	if err != nil {
		return Asset{}, err
	}
	replaced := false
	for i, existing := range items {
		if existing.ID == a.ID {
			items[i] = a
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, a)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if err := c.writeJSON(c.assetsPath(), items); err != nil {
		return Asset{}, err
	}
	return a, nil
}

func (c *Catalog) ListFindings() ([]Finding, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	return c.loadFindingsLocked()
}

func (c *Catalog) TriageFinding(id, decision, duplicateOf string) (Finding, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	items, err := c.loadFindingsLocked()
	if err != nil {
		return Finding{}, err
	}
	index := -1
	for i, item := range items {
		if item.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return Finding{}, fmt.Errorf("finding %q not found", id)
	}
	switch decision {
	case "approve":
		items[index].Triage, items[index].DuplicateOf = "approved", ""
	case "duplicate":
		if duplicateOf == "" || duplicateOf == id {
			return Finding{}, fmt.Errorf("duplicate_of must reference a different finding")
		}
		valid := false
		for _, item := range items {
			if item.ID == duplicateOf && item.Target == items[index].Target && item.DuplicateOf == "" {
				valid = true
				break
			}
		}
		if !valid {
			return Finding{}, fmt.Errorf("duplicate_of must reference an independent finding on the same target")
		}
		items[index].Triage, items[index].DuplicateOf = "duplicate", duplicateOf
	default:
		return Finding{}, fmt.Errorf("decision must be approve or duplicate")
	}
	items[index].UpdatedAt = time.Now().UTC()
	return items[index], c.writeJSON(c.findingsPath(), items)
}

func (c *Catalog) ListAssets() ([]Asset, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	return c.loadAssetsLocked()
}

func (c *Catalog) loadFindingsLocked() ([]Finding, error) {
	var items []Finding
	if err := c.readJSON(c.findingsPath(), &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (c *Catalog) loadAssetsLocked() ([]Asset, error) {
	var items []Asset
	if err := c.readJSON(c.assetsPath(), &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (c *Catalog) readJSON(path string, dest any) error {
	root, err := c.openStorage(false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	name, err := storageFileName(root, path)
	if err != nil {
		return err
	}
	data, err := root.ReadFile(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	return json.Unmarshal(data, dest)
}

func (c *Catalog) writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return c.writeAtomic(path, data)
}

func (c *Catalog) writeAtomic(path string, data []byte) error {
	root, err := c.openStorage(true)
	if err != nil {
		return err
	}
	defer root.Close()
	name, err := storageFileName(root, path)
	if err != nil {
		return err
	}
	tmpName := name + ".tmp-" + rand.Text()
	tmp, err := root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return root.Rename(tmpName, name)
}

// FormatCatalog renders a short asset/finding inventory for prompts and status output.
func FormatCatalog(findings []Finding, assets []Asset) string {
	var b strings.Builder
	if len(assets) == 0 && len(findings) == 0 {
		return ""
	}
	if len(assets) > 0 {
		b.WriteString("Assets:\n")
		for i, a := range assets {
			if i >= 20 {
				b.WriteString(fmt.Sprintf("  ... %d more\n", len(assets)-20))
				break
			}
			line := "  - " + a.Kind + " " + a.Value
			if strings.TrimSpace(a.Notes) != "" {
				line += " (" + strings.TrimSpace(a.Notes) + ")"
			}
			b.WriteString(line + "\n")
		}
	}
	if len(findings) > 0 {
		b.WriteString("Findings:\n")
		for i, f := range findings {
			if i >= 20 {
				b.WriteString(fmt.Sprintf("  ... %d more\n", len(findings)-20))
				break
			}
			b.WriteString(fmt.Sprintf("  - [%s] %s @ %s (%s)\n", f.Severity, f.Title, f.Target, f.Status))
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func normalizeSeverity(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "critical", "high", "medium", "low", "info":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "info"
	}
}

// CanonicalTarget normalizes a host/URL/CIDR string to a bare lowercase host.
func CanonicalTarget(raw string) string {
	v := strings.TrimSpace(strings.ToLower(raw))
	v = strings.Trim(v, `"'`)
	if v == "" {
		return ""
	}
	if strings.Contains(v, "://") {
		if u, err := url.Parse(v); err == nil && u.Host != "" {
			v = u.Host
		}
	}
	if host, _, err := net.SplitHostPort(v); err == nil {
		v = host
	}
	v = strings.TrimSuffix(v, ".")
	v = strings.TrimPrefix(v, "*.")
	return v
}

func normalizeStatus(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case StatusOpen, StatusProofGap, StatusFalsePositive:
		return strings.ToLower(strings.TrimSpace(v))
	case StatusConfirmed, StatusVerified, StatusAccepted:
		return StatusConfirmed
	case StatusRuledOut:
		return StatusRuledOut
	case "":
		return StatusOpen
	default:
		return StatusOpen
	}
}

func slugID(prefix, a, b string) string {
	clean := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		var b strings.Builder
		for _, r := range s {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			} else if r == '.' || r == '-' || r == '_' || r == '/' || r == ' ' {
				b.WriteByte('-')
			}
		}
		out := strings.Trim(b.String(), "-")
		if len(out) > 24 {
			out = out[:24]
		}
		return out
	}
	id := prefix + "-" + clean(a) + "-" + clean(b)
	id = strings.Trim(id, "-")
	if id == prefix {
		id = prefix + "-" + fmt.Sprintf("%d", time.Now().UTC().Unix())
	}
	return id
}
