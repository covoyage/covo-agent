package skillhub

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/evolution"
)

type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version,omitempty"`
	Author      string `json:"author,omitempty"`
	URL         string `json:"url,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	// SHA256 optionally pins the SKILL.md content digest. When present, the
	// registry declares exactly which bytes are trusted; installs whose
	// content does not match are rejected.
	SHA256 string `json:"sha256,omitempty"`
}

type Hub struct {
	registryURL string
	client      *http.Client
	skillsDir   string
	cacheDir    string
}

func New(homeDir string, urlOverrides ...string) *Hub {
	registryURL := os.Getenv("COVO_SKILLS_HUB_URL")
	if registryURL == "" && len(urlOverrides) > 0 && urlOverrides[0] != "" {
		registryURL = urlOverrides[0]
	}
	if registryURL == "" {
		registryURL = "https://raw.githubusercontent.com/covoyage/skills-hub/main"
	}

	return &Hub{
		registryURL: strings.TrimRight(registryURL, "/"),
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		skillsDir: filepath.Join(homeDir, "skills"),
		cacheDir:  filepath.Join(homeDir, "cache", "skillhub"),
	}
}

func (h *Hub) cachePath() string {
	name := strings.ReplaceAll(strings.TrimPrefix(h.registryURL, "https://"), "/", "_")
	return filepath.Join(h.cacheDir, name+".json")
}

func (h *Hub) List() ([]SkillInfo, error) {
	skills, err := h.fetchIndex()
	if err == nil {
		h.writeCache(skills)
		return skills, nil
	}

	cached, cErr := h.readCache()
	if cErr == nil {
		return cached, nil
	}

	return nil, err
}

func (h *Hub) fetchIndex() ([]SkillInfo, error) {
	resp, err := h.client.Get(h.registryURL + "/index.json")
	if err != nil {
		return nil, fmt.Errorf("fetch skill index: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var skills []SkillInfo
	if err := json.Unmarshal(body, &skills); err != nil {
		return nil, fmt.Errorf("parse index: %w", err)
	}
	return skills, nil
}

func (h *Hub) writeCache(skills []SkillInfo) {
	if err := os.MkdirAll(h.cacheDir, 0755); err != nil {
		return
	}
	data, err := json.Marshal(skills)
	if err != nil {
		return
	}
	_ = os.WriteFile(h.cachePath(), data, 0644)
}

func (h *Hub) readCache() ([]SkillInfo, error) {
	data, err := os.ReadFile(h.cachePath())
	if err != nil {
		return nil, err
	}
	var skills []SkillInfo
	if err := json.Unmarshal(data, &skills); err != nil {
		return nil, err
	}
	return skills, nil
}

func (h *Hub) Search(query string) ([]SkillInfo, error) {
	all, err := h.List()
	if err != nil {
		return nil, err
	}

	q := strings.ToLower(query)
	var results []SkillInfo
	for _, s := range all {
		if strings.Contains(strings.ToLower(s.Name), q) ||
			strings.Contains(strings.ToLower(s.Description), q) {
			results = append(results, s)
		}
	}
	return results, nil
}

func (h *Hub) Install(name string) (string, error) {
	resp, err := h.client.Get(h.registryURL + "/skills/" + url.PathEscape(name) + "/SKILL.md")
	if err != nil {
		return "", fmt.Errorf("fetch skill %q: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("skill %q not found in registry", name)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry returned %s for skill %q", resp.Status, name)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read skill %q: %w", name, err)
	}

	// Verify the registry content pin when the index declares one. A pin
	// mismatch means the index and the skill content disagree — treat that
	// as a supply-chain signal and refuse the install.
	digest := fmt.Sprintf("%x", sha256.Sum256(body))
	if entry, err := h.findIndexEntry(name); err == nil && entry != nil && entry.SHA256 != "" {
		if !strings.EqualFold(entry.SHA256, digest) {
			return "", fmt.Errorf(
				"skill %q blocked: content digest %s does not match registry pin %s",
				name, digest, entry.SHA256)
		}
	}

	// --- Security scan on skill content before writing to disk ---
	bodyStr := string(body)
	findings := evolution.ScanContent(bodyStr, "SKILL.md")
	for _, f := range findings {
		if f.Severity == "critical" || f.Severity == "high" {
			return "", fmt.Errorf(
				"skill blocked by security scan: [%s] %s: %s",
				f.Severity, f.PatternID, f.Description)
		}
		if f.Severity == "medium" {
			slog.Warn("suspicious content found during skill install",
				"skill", name,
				"pattern", f.PatternID,
				"severity", f.Severity,
				"description", f.Description)
		}
	}
	// --- end security scan ---

	skillDir := filepath.Join(h.skillsDir, name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", fmt.Errorf("create skill dir: %w", err)
	}

	path := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(path, body, 0644); err != nil {
		return "", fmt.Errorf("write SKILL.md: %w", err)
	}

	writeProvenance(skillDir, Provenance{
		Name:        name,
		Source:      h.registryURL + "/skills/" + url.PathEscape(name) + "/SKILL.md",
		SHA256:      digest,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	})

	return path, nil
}

// findIndexEntry looks up a skill in the registry index. Errors (including
// an unreachable registry) are returned to the caller, which decides whether
// a missing pin is acceptable.
func (h *Hub) findIndexEntry(name string) (*SkillInfo, error) {
	skills, err := h.List()
	if err != nil {
		return nil, err
	}
	for i := range skills {
		if skills[i].Name == name {
			return &skills[i], nil
		}
	}
	return nil, nil
}
