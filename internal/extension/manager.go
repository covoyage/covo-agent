package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Extension describes a loaded extension.
type Extension struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Version     string    `json:"version"`
	BinaryPath  string    `json:"-"`
	Tools       []ToolDef `json:"tools"`
	Enabled     bool
}

// ToolDef describes a tool provided by an extension.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// manifest represents the on-disk manifest.json format.
type manifest struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Version     string    `json:"version"`
	Runtime     string    `json:"runtime"`
	Tools       []ToolDef `json:"tools"`
}

// Manager discovers and manages extensions.
type Manager struct {
	mu         sync.RWMutex
	extensions map[string]*Extension
	extDir     string
}

// NewManager creates a new extension manager rooted at extDir.
func NewManager(extDir string) *Manager {
	return &Manager{
		extensions: make(map[string]*Extension),
		extDir:     extDir,
	}
}

// Discover scans the extension directory for extensions.
func (m *Manager) Discover(ctx context.Context) error {
	entries, err := os.ReadDir(m.extDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read extensions dir %s: %w", m.extDir, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		extDir := filepath.Join(m.extDir, name)

		manifestPath := filepath.Join(extDir, "manifest.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read manifest %s: %w", manifestPath, err)
		}

		var mf manifest
		if err := json.Unmarshal(data, &mf); err != nil {
			return fmt.Errorf("parse manifest %s: %w", manifestPath, err)
		}

		if mf.Name == "" {
			mf.Name = name
		}

		binaryPath := filepath.Join(extDir, "extension")
		if _, err := os.Stat(binaryPath); err != nil {
			binaryPath = ""
		}

		ext := &Extension{
			Name:        mf.Name,
			Description: mf.Description,
			Version:     mf.Version,
			BinaryPath:  binaryPath,
			Tools:       mf.Tools,
			Enabled:     true,
		}

		m.extensions[name] = ext
	}

	return nil
}

// List returns all discovered extensions.
func (m *Manager) List() []*Extension {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Extension, 0, len(m.extensions))
	for _, ext := range m.extensions {
		result = append(result, ext)
	}
	return result
}

// Get returns an extension by name, or nil if not found.
func (m *Manager) Get(name string) *Extension {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.extensions[name]
}

// EnabledCount returns the number of enabled extensions.
func (m *Manager) EnabledCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, ext := range m.extensions {
		if ext.Enabled {
			count++
		}
	}
	return count
}

// SetEnabled enables or disables an extension by name.
func (m *Manager) SetEnabled(name string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ext, ok := m.extensions[name]
	if !ok {
		return fmt.Errorf("extension %q not found", name)
	}
	ext.Enabled = enabled
	return nil
}

// Reload re-discovers all extensions, preserving enabled state of existing ones.
func (m *Manager) Reload(ctx context.Context) error {
	oldEnabled := make(map[string]bool)
	m.mu.RLock()
	for name, ext := range m.extensions {
		oldEnabled[name] = ext.Enabled
	}
	m.mu.RUnlock()

	if err := m.Discover(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	for name, ext := range m.extensions {
		if enabled, ok := oldEnabled[name]; ok {
			ext.Enabled = enabled
		}
	}
	m.mu.Unlock()

	return nil
}
