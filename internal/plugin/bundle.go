package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Bundle is a directory that can contribute skills, slash commands, and
// MCP server definitions. Heavy subprocesses stay in the MCP servers;
// this package only loads metadata.
type Bundle struct {
	Name        string            `json:"name"`
	Version     string            `json:"version,omitempty"`
	Description string            `json:"description,omitempty"`
	Skills      []string          `json:"skills,omitempty"`
	Commands    []string          `json:"commands,omitempty"`
	MCPServers  map[string]MCPDef `json:"mcpServers,omitempty"`
	Root        string            `json:"-"`
	Enabled     bool              `json:"-"`
}

type MCPDef struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
	Timeout int      `json:"timeout,omitempty"`
}

type BundleCommand struct {
	Name        string
	Description string
	Prompt      string
	Plugin      string
}

type BundleRegistry struct {
	mu       sync.RWMutex
	bundles  []*Bundle
	commands []BundleCommand
}

func NewBundleRegistry() *BundleRegistry {
	return &BundleRegistry{}
}

func (r *BundleRegistry) Bundles() []*Bundle {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Bundle, len(r.bundles))
	copy(out, r.bundles)
	return out
}

func (r *BundleRegistry) Commands() []BundleCommand {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]BundleCommand, len(r.commands))
	copy(out, r.commands)
	return out
}

func (r *BundleRegistry) SkillRoots() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var roots []string
	for _, b := range r.bundles {
		if !b.Enabled {
			continue
		}
		for _, rel := range b.skillDirs() {
			roots = append(roots, filepath.Join(b.Root, rel))
		}
	}
	return roots
}

func (r *BundleRegistry) MCPServers() map[string]MCPDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]MCPDef)
	for _, b := range r.bundles {
		if !b.Enabled {
			continue
		}
		for name, def := range b.MCPServers {
			out[name] = def
		}
	}
	return out
}

func (b *Bundle) skillDirs() []string {
	if len(b.Skills) > 0 {
		return b.Skills
	}
	return []string{"skills"}
}

func LoadBundle(dir string) (*Bundle, error) {
	candidates := []string{
		filepath.Join(dir, "plugin.json"),
		filepath.Join(dir, ".covo-plugin", "plugin.json"),
	}
	var data []byte
	var err error
	for _, p := range candidates {
		data, err = os.ReadFile(p)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("plugin.json not found in %s", dir)
	}
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parse plugin.json: %w", err)
	}
	if b.Name == "" {
		b.Name = filepath.Base(dir)
	}
	b.Root = dir
	b.Enabled = true
	return &b, nil
}

func (r *BundleRegistry) Add(b *Bundle) {
	if b == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bundles = append(r.bundles, b)
	r.commands = append(r.commands, loadCommands(b)...)
}

func loadCommands(b *Bundle) []BundleCommand {
	dirs := b.Commands
	if len(dirs) == 0 {
		dirs = []string{"commands"}
	}
	var out []BundleCommand
	for _, rel := range dirs {
		root := filepath.Join(b.Root, rel)
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			body, err := os.ReadFile(filepath.Join(root, e.Name()))
			if err != nil {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".md")
			desc, prompt := splitCommandDoc(string(body))
			out = append(out, BundleCommand{
				Name:        name,
				Description: desc,
				Prompt:      prompt,
				Plugin:      b.Name,
			})
		}
	}
	return out
}

func splitCommandDoc(body string) (desc, prompt string) {
	body = strings.TrimSpace(body)
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		return strings.TrimSpace(strings.TrimPrefix(body[:i], "#")), strings.TrimSpace(body[i+1:])
	}
	return body, body
}

// DiscoverHome loads enabled bundles from <home>/plugins/<name>/.
func DiscoverHome(homeDir string) *BundleRegistry {
	reg := NewBundleRegistry()
	root := filepath.Join(homeDir, "plugins")
	entries, err := os.ReadDir(root)
	if err != nil {
		return reg
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		b, err := LoadBundle(dir)
		if err != nil {
			continue
		}
		if disabled, _ := os.ReadFile(filepath.Join(dir, "disabled")); len(disabled) > 0 {
			b.Enabled = false
		}
		reg.Add(b)
	}
	return reg
}
