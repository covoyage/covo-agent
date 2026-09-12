// Package marketplace provides a unified plugin marketplace for discovering,
// installing, and managing plugins (skills, commands, agents, hooks, MCP servers).
//
// It bridges the existing skillhub and plugin registry systems into a single
// distribution mechanism with category-based browsing, search, and governance.
package marketplace

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/covoyage/covo-agent/internal/skillhub"
)

// PluginType categorizes marketplace plugins.
type PluginType string

const (
	PluginTypeSkill    PluginType = "skill"
	PluginTypeCommand  PluginType = "command"
	PluginTypeAgent    PluginType = "agent"
	PluginTypeHook     PluginType = "hook"
	PluginTypeMCP      PluginType = "mcp"
	PluginTypePlatform PluginType = "platform"
)

// PluginEntry describes a plugin available in the marketplace.
type PluginEntry struct {
	Name        string     `json:"name"`
	DisplayName string     `json:"display_name"`
	Description string     `json:"description"`
	Type        PluginType `json:"type"`
	Version     string     `json:"version,omitempty"`
	Author      string     `json:"author,omitempty"`
	Homepage    string     `json:"homepage,omitempty"`
	Tags        []string   `json:"tags,omitempty"`
	InstallURL  string     `json:"install_url,omitempty"`
	// SHA256 optionally pins the content digest. When present, installs whose
	// payload does not match the pin are rejected — a registry-side defense
	// against content/substitution attacks behind the registry URL.
	SHA256    string `json:"sha256,omitempty"`
	Installed bool   `json:"installed,omitempty"`
}

// InstalledRecord is the provenance of one installed plugin, persisted to
// <homeDir>/marketplace/installed.json.
type InstalledRecord struct {
	Name        string     `json:"name"`
	Type        PluginType `json:"type"`
	SHA256      string     `json:"sha256,omitempty"`
	Source      string     `json:"source"`
	Version     string     `json:"version,omitempty"`
	InstalledAt string     `json:"installed_at"`
}

// Marketplace provides browsing and installation of plugins from a registry.
type Marketplace struct {
	mu          sync.RWMutex
	registryURL string
	client      *http.Client
	homeDir     string
	skillHub    *skillhub.Hub
	cache       []PluginEntry
	cacheTime   time.Time
	logger      *slog.Logger
}

// New creates a Marketplace backed by the given registry URL.
// If registryURL is empty, defaults to the covo-agent marketplace registry.
func New(homeDir string, registryURL string) *Marketplace {
	if registryURL == "" {
		registryURL = os.Getenv("COVO_MARKETPLACE_URL")
	}
	if registryURL == "" {
		registryURL = "https://raw.githubusercontent.com/covoyage/covo-agent-marketplace/main"
	}

	return &Marketplace{
		registryURL: strings.TrimRight(registryURL, "/"),
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		homeDir:  homeDir,
		skillHub: skillhub.New(homeDir),
		logger:   slog.Default(),
	}
}

// SetLogger sets the logger for marketplace operations.
func (m *Marketplace) SetLogger(l *slog.Logger) {
	m.logger = l
}

// List returns all available plugins from the marketplace, optionally filtered by type.
// Results are cached for 5 minutes.
func (m *Marketplace) List(filterType PluginType) ([]PluginEntry, error) {
	entries, err := m.fetchIndex()
	if err != nil {
		return nil, err
	}

	m.markInstalled(entries)

	if filterType != "" {
		var filtered []PluginEntry
		for _, e := range entries {
			if e.Type == filterType {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Type != entries[j].Type {
			return entries[i].Type < entries[j].Type
		}
		return entries[i].Name < entries[j].Name
	})

	return entries, nil
}

// Search returns plugins matching the given query (name, description, or tags).
func (m *Marketplace) Search(query string) ([]PluginEntry, error) {
	entries, err := m.List("")
	if err != nil {
		return nil, err
	}

	query = strings.ToLower(query)
	var results []PluginEntry
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Name), query) ||
			strings.Contains(strings.ToLower(e.DisplayName), query) ||
			strings.Contains(strings.ToLower(e.Description), query) {
			results = append(results, e)
			continue
		}
		for _, tag := range e.Tags {
			if strings.Contains(strings.ToLower(tag), query) {
				results = append(results, e)
				break
			}
		}
	}
	return results, nil
}

// Install installs a plugin by name. The installation method depends on the plugin type:
//   - skills: uses skillhub to fetch SKILL.md
//   - mcp: writes MCP server config to ~/.covo-agent/mcp-servers/<name>.json
//   - others: fetches and writes to the appropriate directory
func (m *Marketplace) Install(name string) (string, error) {
	entries, err := m.fetchIndex()
	if err != nil {
		return "", fmt.Errorf("marketplace: fetch index: %w", err)
	}

	var entry *PluginEntry
	for i := range entries {
		if entries[i].Name == name {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		return "", fmt.Errorf("marketplace: plugin %q not found", name)
	}

	switch entry.Type {
	case PluginTypeSkill:
		return m.installSkill(entry)
	case PluginTypeMCP:
		return m.installMCP(entry)
	case PluginTypeCommand, PluginTypeAgent, PluginTypeHook:
		return m.installGeneric(entry)
	default:
		return m.installGeneric(entry)
	}
}

// Uninstall removes a plugin by name.
func (m *Marketplace) Uninstall(name string) error {
	// Try skill first
	skillPath := filepath.Join(m.homeDir, "skills", name)
	if _, err := os.Stat(skillPath); err == nil {
		if err := os.RemoveAll(skillPath); err != nil {
			return err
		}
		m.removeInstallRecord(name)
		return nil
	}

	// Try MCP config
	mcpPath := filepath.Join(m.homeDir, "mcp-servers", name+".json")
	if _, err := os.Stat(mcpPath); err == nil {
		if err := os.Remove(mcpPath); err != nil {
			return err
		}
		m.removeInstallRecord(name)
		return nil
	}

	// Try generic plugin
	genericPath := filepath.Join(m.homeDir, "plugins", name)
	if _, err := os.Stat(genericPath); err == nil {
		if err := os.RemoveAll(genericPath); err != nil {
			return err
		}
		m.removeInstallRecord(name)
		return nil
	}

	return fmt.Errorf("marketplace: plugin %q not installed", name)
}

// IsInstalled checks if a plugin is installed locally.
func (m *Marketplace) IsInstalled(name string, pluginType PluginType) bool {
	switch pluginType {
	case PluginTypeSkill:
		_, err := os.Stat(filepath.Join(m.homeDir, "skills", name, "SKILL.md"))
		return err == nil
	case PluginTypeMCP:
		_, err := os.Stat(filepath.Join(m.homeDir, "mcp-servers", name+".json"))
		return err == nil
	default:
		_, err := os.Stat(filepath.Join(m.homeDir, "plugins", name))
		return err == nil
	}
}

// Categories returns the list of plugin types that have entries.
func (m *Marketplace) Categories() []PluginType {
	entries, err := m.fetchIndex()
	if err != nil {
		return nil
	}
	seen := make(map[PluginType]bool)
	var cats []PluginType
	for _, e := range entries {
		if !seen[e.Type] {
			seen[e.Type] = true
			cats = append(cats, e.Type)
		}
	}
	return cats
}

// --- Internal methods ---

func (m *Marketplace) fetchIndex() ([]PluginEntry, error) {
	m.mu.RLock()
	if len(m.cache) > 0 && time.Since(m.cacheTime) < 5*time.Minute {
		entries := make([]PluginEntry, len(m.cache))
		copy(entries, m.cache)
		m.mu.RUnlock()
		return entries, nil
	}
	m.mu.RUnlock()

	resp, err := m.client.Get(m.registryURL + "/marketplace.json")
	if err != nil {
		// Try cache even if stale
		m.mu.RLock()
		if len(m.cache) > 0 {
			entries := make([]PluginEntry, len(m.cache))
			copy(entries, m.cache)
			m.mu.RUnlock()
			m.logger.Warn("marketplace: using stale cache", "err", err)
			return entries, nil
		}
		m.mu.RUnlock()
		return nil, fmt.Errorf("marketplace: fetch index: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("marketplace: registry returned %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("marketplace: read index: %w", err)
	}

	var entries []PluginEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("marketplace: parse index: %w", err)
	}

	m.mu.Lock()
	m.cache = entries
	m.cacheTime = time.Now()
	m.mu.Unlock()

	return entries, nil
}

func (m *Marketplace) markInstalled(entries []PluginEntry) {
	for i := range entries {
		entries[i].Installed = m.IsInstalled(entries[i].Name, entries[i].Type)
	}
}

// installPayload fetches the plugin body for an entry and enforces the
// registry content pin when one is declared.
func (m *Marketplace) installPayload(entry *PluginEntry, defaultPath string) ([]byte, error) {
	url := entry.InstallURL
	if url == "" {
		url = m.registryURL + defaultPath
	}

	resp, err := m.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if entry.SHA256 != "" {
		digest := fmt.Sprintf("%x", sha256.Sum256(body))
		if !strings.EqualFold(entry.SHA256, digest) {
			return nil, fmt.Errorf(
				"content digest %s does not match registry pin %s — install blocked",
				digest, entry.SHA256)
		}
	}
	return body, nil
}

// recordInstall writes an InstalledRecord into the installed manifest.
func (m *Marketplace) recordInstall(entry *PluginEntry, digest, source string) {
	manifest := m.installedManifest()
	manifest[entry.Name] = InstalledRecord{
		Name:        entry.Name,
		Type:        entry.Type,
		SHA256:      digest,
		Source:      source,
		Version:     entry.Version,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := os.MkdirAll(filepath.Dir(m.manifestPath()), 0755); err != nil {
		return
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(m.manifestPath(), data, 0644)
}

// removeInstallRecord drops a plugin's record from the installed manifest.
func (m *Marketplace) removeInstallRecord(name string) {
	manifest := m.installedManifest()
	if _, ok := manifest[name]; !ok {
		return
	}
	delete(manifest, name)
	if err := os.MkdirAll(filepath.Dir(m.manifestPath()), 0755); err != nil {
		return
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(m.manifestPath(), data, 0644)
}

func (m *Marketplace) manifestPath() string {
	return filepath.Join(m.homeDir, "marketplace", "installed.json")
}

func (m *Marketplace) installedManifest() map[string]InstalledRecord {
	data, err := os.ReadFile(m.manifestPath())
	if err != nil {
		return make(map[string]InstalledRecord)
	}
	var manifest map[string]InstalledRecord
	if json.Unmarshal(data, &manifest) != nil || manifest == nil {
		return make(map[string]InstalledRecord)
	}
	return manifest
}

func (m *Marketplace) installSkill(entry *PluginEntry) (string, error) {
	path, err := m.skillHub.Install(entry.Name)
	if err != nil {
		return "", fmt.Errorf("marketplace: install skill %q: %w", entry.Name, err)
	}
	// Record provenance in the marketplace manifest; the digest comes from
	// the freshly installed SKILL.md (skillhub already verified any pin).
	digest := ""
	if body, err := os.ReadFile(path); err == nil {
		digest = fmt.Sprintf("%x", sha256.Sum256(body))
	}
	source := entry.InstallURL
	if source == "" {
		source = m.registryURL + "/skills/" + entry.Name + "/SKILL.md"
	}
	m.recordInstall(entry, digest, source)
	return path, nil
}

func (m *Marketplace) installMCP(entry *PluginEntry) (string, error) {
	// Fetch MCP server config
	body, err := m.installPayload(entry, "/mcp/"+entry.Name+".json")
	if err != nil {
		return "", fmt.Errorf("marketplace: fetch MCP config: %w", err)
	}

	// Security scan
	bodyStr := string(body)
	findings := evolution.ScanContent(bodyStr, entry.Name+".json")
	for _, f := range findings {
		if f.Severity == "critical" || f.Severity == "high" {
			return "", fmt.Errorf("marketplace: MCP config blocked by security scan: [%s] %s", f.Severity, f.Description)
		}
	}

	mcpDir := filepath.Join(m.homeDir, "mcp-servers")
	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		return "", fmt.Errorf("marketplace: create mcp dir: %w", err)
	}

	path := filepath.Join(mcpDir, entry.Name+".json")
	if err := os.WriteFile(path, body, 0644); err != nil {
		return "", fmt.Errorf("marketplace: write MCP config: %w", err)
	}

	source := entry.InstallURL
	if source == "" {
		source = m.registryURL + "/mcp/" + entry.Name + ".json"
	}
	m.recordInstall(entry, fmt.Sprintf("%x", sha256.Sum256(body)), source)

	return path, nil
}

func (m *Marketplace) installGeneric(entry *PluginEntry) (string, error) {
	// Fetch plugin config
	body, err := m.installPayload(entry, "/plugins/"+entry.Name+".json")
	if err != nil {
		return "", fmt.Errorf("marketplace: fetch plugin: %w", err)
	}

	// Security scan
	bodyStr := string(body)
	findings := evolution.ScanContent(bodyStr, entry.Name)
	for _, f := range findings {
		if f.Severity == "critical" {
			return "", fmt.Errorf("marketplace: plugin blocked by security scan: [%s] %s", f.Severity, f.Description)
		}
	}

	pluginDir := filepath.Join(m.homeDir, "plugins", entry.Name)
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return "", fmt.Errorf("marketplace: create plugin dir: %w", err)
	}

	path := filepath.Join(pluginDir, "plugin.json")
	if err := os.WriteFile(path, body, 0644); err != nil {
		return "", fmt.Errorf("marketplace: write plugin: %w", err)
	}

	source := entry.InstallURL
	if source == "" {
		source = m.registryURL + "/plugins/" + entry.Name + ".json"
	}
	m.recordInstall(entry, fmt.Sprintf("%x", sha256.Sum256(body)), source)

	return path, nil
}
