package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const overridesFile = "feature_overrides.json"

type Stage int

const (
	UnderDevelopment Stage = iota
	Experimental
	Stable
	Deprecated
	Removed
)

func (s Stage) String() string {
	switch s {
	case UnderDevelopment:
		return "under-development"
	case Experimental:
		return "experimental"
	case Stable:
		return "stable"
	case Deprecated:
		return "deprecated"
	case Removed:
		return "removed"
	default:
		return "unknown"
	}
}

// defaultEnabled returns the default enabled state for a given stage.
// Only Stable flags are on by default.
func (s Stage) defaultEnabled() bool {
	return s == Stable
}

// PromotableTo returns true if promoting from stage s to target t is allowed.
// Promotions must move forward in the lifecycle.
func (s Stage) promotableTo(t Stage) bool {
	return t > s && t <= Removed
}

type Flag struct {
	Name        string
	Description string
	Stage       Stage
	Default     bool
}

type Registry struct {
	mu            sync.RWMutex
	flags         map[string]*Flag
	overrides     map[string]bool
	stageOverrides map[string]Stage
}

var globalRegistry = &Registry{
	flags:          make(map[string]*Flag),
	overrides:      make(map[string]bool),
	stageOverrides: make(map[string]Stage),
}

func RegisterFlag(f Flag) {
	if f.Name == "" {
		panic("cli: feature flag must have a name")
	}
	normalName := strings.ToLower(f.Name)
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	if _, ok := globalRegistry.flags[normalName]; ok {
		panic(fmt.Sprintf("cli: duplicate feature flag %q", normalName))
	}
	globalRegistry.flags[normalName] = &f
}

func RegisterFlags(flags ...Flag) {
	for _, f := range flags {
		RegisterFlag(f)
	}
}

func IsEnabled(name string) bool {
	normalName := strings.ToLower(name)
	globalRegistry.mu.RLock()
	f, ok := globalRegistry.flags[normalName]
	override, hasOverride := globalRegistry.overrides[normalName]
	globalRegistry.mu.RUnlock()

	if !ok {
		return false // unknown flags are off
	}
	if hasOverride {
		return override
	}
	envKey := "COVO_FEATURE_" + strings.ToUpper(strings.ReplaceAll(normalName, "-", "_"))
	if v, ok := os.LookupEnv(envKey); ok {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	// Default is determined by lifecycle stage: only Stable flags are on by default.
	return f.Stage.defaultEnabled()
}

func Enable(name string) error {
	normalName := strings.ToLower(name)
	globalRegistry.mu.Lock()
	if _, ok := globalRegistry.flags[normalName]; !ok {
		globalRegistry.mu.Unlock()
		return fmt.Errorf("unknown feature flag: %q", name)
	}
	globalRegistry.overrides[normalName] = true
	globalRegistry.mu.Unlock()
	saveData()
	return nil
}

func Disable(name string) error {
	normalName := strings.ToLower(name)
	globalRegistry.mu.Lock()
	if _, ok := globalRegistry.flags[normalName]; !ok {
		globalRegistry.mu.Unlock()
		return fmt.Errorf("unknown feature flag: %q", name)
	}
	globalRegistry.overrides[normalName] = false
	globalRegistry.mu.Unlock()
	saveData()
	return nil
}

func ResetOverride(name string) {
	normalName := strings.ToLower(name)
	globalRegistry.mu.Lock()
	delete(globalRegistry.overrides, normalName)
	globalRegistry.mu.Unlock()
	saveData()
}

// Override sets a feature flag override without persisting (for tests).
func Override(name string, val bool) {
	normalName := strings.ToLower(name)
	globalRegistry.mu.Lock()
	globalRegistry.overrides[normalName] = val
	globalRegistry.mu.Unlock()
}

// --- Persistence ---

type featureData struct {
	Overrides map[string]bool `json:"overrides"`
	Stages    map[string]int  `json:"stages,omitempty"`
}

func dataPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".covo-agent", overridesFile)
}

func saveData() {
	path := dataPath()
	if path == "" {
		return
	}
	globalRegistry.mu.RLock()
	fd := featureData{
		Overrides: make(map[string]bool, len(globalRegistry.overrides)),
		Stages:    make(map[string]int, len(globalRegistry.stageOverrides)),
	}
	for k, v := range globalRegistry.overrides {
		fd.Overrides[k] = v
	}
	for k, v := range globalRegistry.stageOverrides {
		fd.Stages[k] = int(v)
	}
	globalRegistry.mu.RUnlock()

	data, err := json.MarshalIndent(fd, "", "  ")
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	os.WriteFile(path, data, 0o600)
}

func loadData() {
	path := dataPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	// Try new format (versioned struct)
	var fd featureData
	if err := json.Unmarshal(data, &fd); err == nil && (len(fd.Overrides) > 0 || len(fd.Stages) > 0) {
		globalRegistry.mu.Lock()
		for k, v := range fd.Overrides {
			normalName := normalizeName(k)
			if _, ok := globalRegistry.flags[normalName]; ok {
				globalRegistry.overrides[normalName] = v
			}
		}
		for k, v := range fd.Stages {
			normalName := normalizeName(k)
			if f, ok := globalRegistry.flags[normalName]; ok {
				stage := Stage(v)
				f.Stage = stage
				globalRegistry.stageOverrides[normalName] = stage
			}
		}
		globalRegistry.mu.Unlock()
		return
	}

	// Fallback: old format (plain map[string]bool)
	var loaded map[string]bool
	if err := json.Unmarshal(data, &loaded); err != nil {
		return
	}
	globalRegistry.mu.Lock()
	for k, v := range loaded {
		normalName := normalizeName(k)
		if _, ok := globalRegistry.flags[normalName]; ok {
			globalRegistry.overrides[normalName] = v
		}
	}
	globalRegistry.mu.Unlock()
}

func normalizeName(name string) string {
	return strings.ToLower(name)
}

type FlagInfo struct {
	Name        string
	Description string
	Stage       Stage
	Default     bool
	Enabled     bool
	Overridden  bool
}

func ListFlags() []FlagInfo {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	out := make([]FlagInfo, 0, len(globalRegistry.flags))
	for _, f := range globalRegistry.flags {
		enabled := IsEnabled(f.Name)
		_, hasOverride := globalRegistry.overrides[f.Name]
		def := f.Stage.defaultEnabled()
		out = append(out, FlagInfo{
			Name:        f.Name,
			Description: f.Description,
			Stage:       f.Stage,
			Default:     def,
			Enabled:     enabled,
			Overridden:  hasOverride,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Stage != out[j].Stage {
			return out[i].Stage < out[j].Stage
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func Lookup(name string) (*Flag, bool) {
	normalName := strings.ToLower(name)
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	f, ok := globalRegistry.flags[normalName]
	return f, ok
}

// Promote advances a feature flag to a later lifecycle stage.
// Only forward promotions are allowed (e.g. Experimental → Stable).
// Clears any user override so the new default takes effect.
// Stage changes are persisted across restarts.
func Promote(name string, to Stage) error {
	normalName := normalizeName(name)
	globalRegistry.mu.Lock()
	f, ok := globalRegistry.flags[normalName]
	if !ok {
		globalRegistry.mu.Unlock()
		return fmt.Errorf("unknown feature flag: %q", name)
	}
	if !f.Stage.promotableTo(to) {
		globalRegistry.mu.Unlock()
		return fmt.Errorf("cannot promote %q from %s to %s: stage order must be forward", name, f.Stage, to)
	}
	f.Stage = to
	globalRegistry.stageOverrides[normalName] = to
	delete(globalRegistry.overrides, normalName)
	globalRegistry.mu.Unlock()
	saveData()
	return nil
}

// StageOverride returns the persisted stage override for a flag, or zero value if none.
func StageOverride(name string) (Stage, bool) {
	normalName := normalizeName(name)
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	s, ok := globalRegistry.stageOverrides[normalName]
	return s, ok
}

func init() {
	RegisterFlags(
		Flag{
			Name:        "exec-policy",
			Description: "Enable declarative command execution policy (.covo-agent-policy.yaml)",
			Stage:       Experimental,
			Default:     false,
		},
		Flag{
			Name:        "fuzzy-file-search",
			Description: "Use fzf-style fuzzy matching for @file/@folder autocomplete",
			Stage:       Experimental,
			Default:     false,
		},
		Flag{
			Name:        "config-schema",
			Description: "Enable config JSON schema generation and validation",
			Stage:       Experimental,
			Default:     false,
		},
		Flag{
			Name:        "computer-use",
			Description: "Enable experimental computer-use tool",
			Stage:       Experimental,
			Default:     false,
		},
	)
	loadData()
}
