package ossandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProfileName identifies a sandbox profile.
type ProfileName string

const (
	ProfileOff       ProfileName = "off"
	ProfileWorkspace ProfileName = "workspace"
	ProfileReadOnly  ProfileName = "read-only"
	ProfileStrict    ProfileName = "strict"
	ProfileDevbox    ProfileName = "devbox"
)

// ParseProfileName parses a string into a ProfileName.
// Unknown names are treated as custom profile names.
func ParseProfileName(s string) ProfileName {
	switch s {
	case "off", "none", "":
		return ProfileOff
	case "workspace":
		return ProfileWorkspace
	case "read-only", "readonly":
		return ProfileReadOnly
	case "strict":
		return ProfileStrict
	case "devbox":
		return ProfileDevbox
	default:
		return ProfileName(s) // custom
	}
}

// String returns the string representation.
func (p ProfileName) String() string { return string(p) }

// IsBuiltIn returns true for built-in profiles.
func (p ProfileName) IsBuiltIn() bool {
	switch p {
	case ProfileWorkspace, ProfileReadOnly, ProfileStrict, ProfileDevbox, ProfileOff:
		return true
	default:
		return false
	}
}

// RestrictsNetwork returns true if the profile blocks child network (Linux only).
func (p ProfileName) RestrictsNetwork() bool {
	return p == ProfileReadOnly || p == ProfileStrict
}

// SandboxProfile is a fully resolved profile ready for enforcement.
type SandboxProfile struct {
	Name            string
	ReadOnlyPaths   []string
	ReadWritePaths  []string
	DenyPaths       []string
	DefaultRead     bool // grant read access to entire filesystem
	RestrictNetwork bool // block child process network (Linux only)
}

// ProfileConfig is the YAML representation of a custom profile.
type ProfileConfig struct {
	Extends         string   `yaml:"extends,omitempty"`
	RestrictNetwork *bool   `yaml:"restrict_network,omitempty"`
	ReadOnly        []string `yaml:"read_only,omitempty"`
	ReadWrite       []string `yaml:"read_write,omitempty"`
	Deny            []string `yaml:"deny,omitempty"`
}

// SandboxConfig holds all custom profiles from sandbox.yaml.
type SandboxConfig struct {
	Profiles map[string]ProfileConfig `yaml:"profiles"`
}

// HomeDir returns the covo-agent home directory (~/.covo-agent or COVO_AGENT_HOME).
func HomeDir() string {
	if h := os.Getenv("COVO_AGENT_HOME"); h != "" {
		return h
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".covo-agent")
	}
	return filepath.Join(os.TempDir(), ".covo-agent")
}

// LoadSandboxConfig loads sandbox config from global and project config files.
// Project config can only ADD new profiles, not redefine global ones.
func LoadSandboxConfig(workspace string) *SandboxConfig {
	cfg := &SandboxConfig{Profiles: make(map[string]ProfileConfig)}

	// Global config: ~/.covo-agent/sandbox.yaml
	globalPath := filepath.Join(HomeDir(), "sandbox.yaml")
	if global := loadConfigFile(globalPath); global != nil {
		cfg = global
	}

	// Project config: <workspace>/.covo-agent/sandbox.yaml (additive only)
	projectPath := filepath.Join(workspace, ".covo-agent", "sandbox.yaml")
	if project := loadConfigFile(projectPath); project != nil {
		for name, profile := range project.Profiles {
			if _, exists := cfg.Profiles[name]; !exists {
				cfg.Profiles[name] = profile
			}
		}
	}

	return cfg
}

func loadConfigFile(path string) *SandboxConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg SandboxConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	if cfg.Profiles == nil {
		cfg.Profiles = make(map[string]ProfileConfig)
	}
	return &cfg
}

// essentialWritablePaths returns paths that must always be writable for the agent to function.
func essentialWritablePaths(workspace string) []string {
	home := HomeDir()
	tmp := os.TempDir()
	return []string{
		workspace,
		home,
		tmp,
	}
}

// essentialWritablePathsMinimal returns the minimum writable paths (for read-only mode).
func essentialWritablePathsMinimal() []string {
	home := HomeDir()
	tmp := os.TempDir()
	return []string{
		home,
		tmp,
	}
}

// systemReadPaths returns essential system paths for read access (strict mode).
func systemReadPaths() []string {
	var paths []string
	candidates := []string{
		"/usr", "/lib", "/lib64", "/bin", "/sbin", "/etc", "/dev", "/proc", "/sys",
		"/tmp", "/run", "/var",
	}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, "/System", "/Library", "/private")
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}
	return paths
}

// ResolveProfile resolves a ProfileName into a fully specified SandboxProfile.
func ResolveProfile(name ProfileName, workspace string, config *SandboxConfig) (*SandboxProfile, error) {
	if name == ProfileOff {
		return nil, fmt.Errorf("sandbox profile 'off' cannot be resolved as a base profile")
	}

	if name == ProfileWorkspace {
		return &SandboxProfile{
			Name:           "workspace",
			ReadWritePaths: essentialWritablePaths(workspace),
			DefaultRead:    true,
			RestrictNetwork: false,
		}, nil
	}

	if name == ProfileDevbox {
		// Everything writable except /data
		rw := []string{workspace}
		if entries, err := os.ReadDir("/"); err == nil {
			for _, entry := range entries {
				p := filepath.Join("/", entry.Name())
				if p == "/data" || p == "/proc" || p == "/sys" || p == "/dev" {
					continue
				}
				if entry.IsDir() {
					rw = append(rw, p)
				}
			}
		}
		return &SandboxProfile{
			Name:           "devbox",
			ReadWritePaths: rw,
			DefaultRead:    true,
			RestrictNetwork: false,
		}, nil
	}

	if name == ProfileReadOnly {
		return &SandboxProfile{
			Name:           "read-only",
			ReadWritePaths: essentialWritablePathsMinimal(),
			DefaultRead:    true,
			RestrictNetwork: true,
		}, nil
	}

	if name == ProfileStrict {
		readPaths := systemReadPaths()
		readPaths = append(readPaths, workspace)
		readPaths = append(readPaths, HomeDir())
		return &SandboxProfile{
			Name:            "strict",
			ReadOnlyPaths:   readPaths,
			ReadWritePaths:  essentialWritablePaths(workspace),
			DefaultRead:     false,
			RestrictNetwork: true,
		}, nil
	}

	// Custom profile
	customName := string(name)
	profileCfg, ok := config.Profiles[customName]
	if !ok {
		return nil, fmt.Errorf(
			"custom sandbox profile '%s' not found. Define it in ~/.covo-agent/sandbox.yaml:\n\n"+
				"profiles:\n"+
				"  %s:\n"+
				"    extends: workspace\n"+
				"    read_only:\n      - /data\n",
			customName, customName,
		)
	}

	// Determine base profile from extends field
	baseName := profileCfg.Extends
	if baseName == "" {
		baseName = "workspace"
	}
	base := ParseProfileName(baseName)
	if base == ProfileOff {
		return nil, fmt.Errorf("profile '%s' extends 'off', which is not a valid base", customName)
	}
	if !base.IsBuiltIn() {
		return nil, fmt.Errorf("profile '%s' extends '%s', but custom profiles cannot extend other custom profiles", customName, baseName)
	}

	resolved, err := ResolveProfile(base, workspace, config)
	if err != nil {
		return nil, fmt.Errorf("profile '%s' base resolution failed: %w", customName, err)
	}

	resolved.Name = customName

	if profileCfg.RestrictNetwork != nil {
		resolved.RestrictNetwork = *profileCfg.RestrictNetwork
	}
	resolved.ReadOnlyPaths = append(resolved.ReadOnlyPaths, profileCfg.ReadOnly...)
	resolved.ReadWritePaths = append(resolved.ReadWritePaths, profileCfg.ReadWrite...)
	resolved.DenyPaths = append(resolved.DenyPaths, profileCfg.Deny...)

	return resolved, nil
}

// ProfileConflicts returns names of custom profiles that differ between global and project config.
func ProfileConflicts(workspace string) []string {
	global := loadConfigFile(filepath.Join(HomeDir(), "sandbox.yaml"))
	project := loadConfigFile(filepath.Join(workspace, ".covo-agent", "sandbox.yaml"))
	if global == nil || project == nil {
		return nil
	}
	var conflicts []string
	for name, projProfile := range project.Profiles {
		if globalProfile, exists := global.Profiles[name]; exists {
			if !profileEquals(globalProfile, projProfile) {
				conflicts = append(conflicts, name)
			}
		}
	}
	return conflicts
}

func profileEquals(a, b ProfileConfig) bool {
	if a.Extends != b.Extends {
		return false
	}
	if (a.RestrictNetwork == nil) != (b.RestrictNetwork == nil) {
		return false
	}
	if a.RestrictNetwork != nil && b.RestrictNetwork != nil && *a.RestrictNetwork != *b.RestrictNetwork {
		return false
	}
	if !sliceEqual(a.ReadOnly, b.ReadOnly) || !sliceEqual(a.ReadWrite, b.ReadWrite) || !sliceEqual(a.Deny, b.Deny) {
		return false
	}
	return true
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// IsGlob returns true if the path contains glob metacharacters.
func IsGlob(path string) bool {
	return strings.ContainsAny(path, "*?[")
}
