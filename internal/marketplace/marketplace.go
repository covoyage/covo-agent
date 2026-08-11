// Package marketplace provides a unified plugin marketplace for discovering,
// installing, and managing plugins (skills, commands, agents, hooks, MCP servers).
//
// It bridges the existing skillhub and plugin registry systems into a single
// distribution mechanism with category-based browsing, search, and governance.
package marketplace

import (
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
	Installed   bool       `json:"installed,omitempty"`
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
		return m.installSkill(name)
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
		return os.RemoveAll(skillPath)
	}

	// Try MCP config
	mcpPath := filepath.Join(m.homeDir, "mcp-servers", name+".json")
	if _, err := os.Stat(mcpPath); err == nil {
		return os.Remove(mcpPath)
	}

	// Try generic plugin
	genericPath := filepath.Join(m.homeDir, "plugins", name)
	if _, err := os.Stat(genericPath); err == nil {
		return os.RemoveAll(genericPath)
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

func (m *Marketplace) installSkill(name string) (string, error) {
	path, err := m.skillHub.Install(name)
	if err != nil {
		return "", fmt.Errorf("marketplace: install skill %q: %w", name, err)
	}
	return path, nil
}

func (m *Marketplace) installMCP(entry *PluginEntry) (string, error) {
	// Fetch MCP server config
	url := entry.InstallURL
	if url == "" {
		url = m.registryURL + "/mcp/" + entry.Name + ".json"
	}

	resp, err := m.client.Get(url)
	if err != nil {
		return "", fmt.Errorf("marketplace: fetch MCP config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("marketplace: MCP config returned %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("marketplace: read MCP config: %w", err)
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

	return path, nil
}

func (m *Marketplace) installGeneric(entry *PluginEntry) (string, error) {
	url := entry.InstallURL
	if url == "" {
		url = m.registryURL + "/plugins/" + entry.Name + ".json"
	}

	resp, err := m.client.Get(url)
	if err != nil {
		return "", fmt.Errorf("marketplace: fetch plugin: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("marketplace: plugin returned %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("marketplace: read plugin: %w", err)
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

	return path, nil
}
