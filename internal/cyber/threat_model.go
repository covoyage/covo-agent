package cyber

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ThreatModel struct {
	Target              string            `json:"target"`
	Identity            string            `json:"identity"`
	Overview            string            `json:"overview"`
	Attacker            string            `json:"attacker"`
	TrustBoundaries     []string          `json:"trust_boundaries"`
	AttackSurface       []string          `json:"attack_surface"`
	SeverityCalibration string            `json:"severity_calibration"`
	Amendments          []ThreatAmendment `json:"amendments,omitempty"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

type ThreatAmendment struct {
	Author string    `json:"author,omitempty"`
	Body   string    `json:"body"`
	At     time.Time `json:"at"`
}

func (c *Catalog) threatModelsPath() string { return filepath.Join(c.dir, "threat_models.json") }

type ThreatModelValidationError struct {
	MissingFields []string
}

func (e *ThreatModelValidationError) Error() string {
	return "threat model requires non-empty fields: " + strings.Join(e.MissingFields, ", ")
}

func (c *Catalog) SaveThreatModel(model ThreatModel) (ThreatModel, error) {
	model.Target = strings.TrimSpace(model.Target)
	var missing []string
	for _, field := range []struct{ name, value string }{
		{"target", model.Target},
		{"overview", model.Overview},
		{"attacker", model.Attacker},
		{"trust_boundaries", strings.Join(model.TrustBoundaries, "")},
		{"attack_surface", strings.Join(model.AttackSurface, "")},
		{"severity_calibration", model.SeverityCalibration},
	} {
		if strings.TrimSpace(field.value) == "" {
			missing = append(missing, field.name)
		}
	}
	if len(missing) > 0 {
		return ThreatModel{}, &ThreatModelValidationError{MissingFields: missing}
	}
	model.Identity = threatTargetIdentity(model.Target)
	model.UpdatedAt = time.Now().UTC()

	catalogMu.Lock()
	defer catalogMu.Unlock()
	models, err := c.loadThreatModelsLocked()
	if err != nil {
		return ThreatModel{}, err
	}
	if previous, ok := models[model.Identity]; ok && len(model.Amendments) == 0 {
		model.Amendments = previous.Amendments
	}
	models[model.Identity] = model
	if err := c.writeJSON(c.threatModelsPath(), models); err != nil {
		return ThreatModel{}, err
	}
	return model, nil
}

func (c *Catalog) GetThreatModel(target string) (ThreatModel, bool, error) {
	identity := threatTargetIdentity(target)
	catalogMu.Lock()
	defer catalogMu.Unlock()
	models, err := c.loadThreatModelsLocked()
	if err != nil {
		return ThreatModel{}, false, err
	}
	model, ok := models[identity]
	return model, ok, nil
}

func (c *Catalog) AmendThreatModel(target, author, body string) (ThreatModel, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return ThreatModel{}, fmt.Errorf("threat model amendment is required")
	}
	identity := threatTargetIdentity(target)
	catalogMu.Lock()
	defer catalogMu.Unlock()
	models, err := c.loadThreatModelsLocked()
	if err != nil {
		return ThreatModel{}, err
	}
	model, ok := models[identity]
	if !ok {
		return ThreatModel{}, fmt.Errorf("threat model for %q not found", strings.TrimSpace(target))
	}
	model.Amendments = append(model.Amendments, ThreatAmendment{Author: strings.TrimSpace(author), Body: body, At: time.Now().UTC()})
	if len(model.Amendments) > 40 {
		model.Amendments = model.Amendments[len(model.Amendments)-40:]
	}
	model.UpdatedAt = time.Now().UTC()
	models[identity] = model
	if err := c.writeJSON(c.threatModelsPath(), models); err != nil {
		return ThreatModel{}, err
	}
	return model, nil
}

func (c *Catalog) ListThreatModels() ([]ThreatModel, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	models, err := c.loadThreatModelsLocked()
	if err != nil {
		return nil, err
	}
	items := make([]ThreatModel, 0, len(models))
	for _, model := range models {
		items = append(items, model)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Identity < items[j].Identity })
	return items, nil
}

func (c *Catalog) loadThreatModelsLocked() (map[string]ThreatModel, error) {
	models := map[string]ThreatModel{}
	if err := c.readJSON(c.threatModelsPath(), &models); err != nil {
		return nil, err
	}
	return models, nil
}

func threatTargetIdentity(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		root := target
		if out, err := exec.Command("git", "-C", target, "rev-parse", "--show-toplevel").Output(); err == nil {
			root = strings.TrimSpace(string(out))
		}
		if out, err := exec.Command("git", "-C", root, "config", "--get", "remote.origin.url").Output(); err == nil {
			if remote := normalizeGitRemote(strings.TrimSpace(string(out))); remote != "" {
				return remote
			}
		}
		if absolute, err := filepath.Abs(root); err == nil {
			return filepath.Clean(absolute)
		}
	}
	candidate := target
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}
	if parsed, err := url.Parse(candidate); err == nil && parsed.Hostname() != "" {
		host := strings.ToLower(parsed.Hostname())
		port := parsed.Port()
		if port != "" && !((parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80")) {
			host += ":" + port
		}
		return host + strings.TrimSuffix(parsed.EscapedPath(), "/")
	}
	return strings.ToLower(strings.Join(strings.Fields(target), " "))
}

func normalizeGitRemote(remote string) string {
	remote = strings.TrimSpace(strings.TrimSuffix(remote, ".git"))
	if at := strings.LastIndex(remote, "@"); at >= 0 && !strings.Contains(remote[:at], "://") {
		remote = remote[at+1:]
	}
	if !strings.Contains(remote, "://") && strings.Contains(remote, ":") {
		parts := strings.SplitN(remote, ":", 2)
		remote = parts[0] + "/" + parts[1]
	}
	if parsed, err := url.Parse(remote); err == nil && parsed.Hostname() != "" {
		return strings.ToLower(parsed.Hostname() + strings.TrimSuffix(parsed.Path, ".git"))
	}
	return strings.ToLower(strings.TrimPrefix(remote, "git@"))
}

func FormatThreatModels(models []ThreatModel) string {
	if len(models) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Shared threat models:\n")
	for _, model := range models {
		fmt.Fprintf(&b, "- %s: attacker=%s; boundaries=%s; surface=%s; severity=%s\n",
			model.Target, model.Attacker, strings.Join(model.TrustBoundaries, "; "),
			strings.Join(model.AttackSurface, "; "), model.SeverityCalibration)
		for _, amendment := range model.Amendments {
			fmt.Fprintf(&b, "  amendment (%s): %s\n", amendment.Author, amendment.Body)
		}
	}
	return strings.TrimSpace(b.String())
}
