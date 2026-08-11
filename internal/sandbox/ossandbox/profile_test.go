package ossandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseProfileName(t *testing.T) {
	tests := []struct {
		input string
		want  ProfileName
	}{
		{"off", ProfileOff},
		{"none", ProfileOff},
		{"", ProfileOff},
		{"workspace", ProfileWorkspace},
		{"read-only", ProfileReadOnly},
		{"readonly", ProfileReadOnly},
		{"strict", ProfileStrict},
		{"devbox", ProfileDevbox},
		{"my-custom", ProfileName("my-custom")},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := ParseProfileName(tc.input)
			if got != tc.want {
				t.Errorf("ParseProfileName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestProfileNameString(t *testing.T) {
	tests := []struct {
		name ProfileName
		want string
	}{
		{ProfileOff, "off"},
		{ProfileWorkspace, "workspace"},
		{ProfileReadOnly, "read-only"},
		{ProfileStrict, "strict"},
		{ProfileDevbox, "devbox"},
		{ProfileName("custom"), "custom"},
	}
	for _, tc := range tests {
		t.Run(string(tc.name), func(t *testing.T) {
			if got := tc.name.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProfileNameIsBuiltIn(t *testing.T) {
	builtIns := []ProfileName{ProfileOff, ProfileWorkspace, ProfileReadOnly, ProfileStrict, ProfileDevbox}
	for _, p := range builtIns {
		if !p.IsBuiltIn() {
			t.Errorf("%q should be built-in", p)
		}
	}
	if ProfileName("custom").IsBuiltIn() {
		t.Error("custom should not be built-in")
	}
}

func TestProfileNameRestrictsNetwork(t *testing.T) {
	if !ProfileReadOnly.RestrictsNetwork() {
		t.Error("read-only should restrict network")
	}
	if !ProfileStrict.RestrictsNetwork() {
		t.Error("strict should restrict network")
	}
	if ProfileWorkspace.RestrictsNetwork() {
		t.Error("workspace should not restrict network")
	}
	if ProfileDevbox.RestrictsNetwork() {
		t.Error("devbox should not restrict network")
	}
}

func TestResolveProfileWorkspace(t *testing.T) {
	workspace := t.TempDir()
	config := &SandboxConfig{Profiles: make(map[string]ProfileConfig)}

	profile, err := ResolveProfile(ProfileWorkspace, workspace, config)
	if err != nil {
		t.Fatalf("ResolveProfile failed: %v", err)
	}
	if profile.Name != "workspace" {
		t.Errorf("Name = %q, want workspace", profile.Name)
	}
	if !profile.DefaultRead {
		t.Error("workspace should have DefaultRead=true")
	}
	if profile.RestrictNetwork {
		t.Error("workspace should not restrict network")
	}
	if len(profile.ReadWritePaths) == 0 {
		t.Error("workspace should have read-write paths")
	}
}

func TestResolveProfileReadOnly(t *testing.T) {
	workspace := t.TempDir()
	config := &SandboxConfig{Profiles: make(map[string]ProfileConfig)}

	profile, err := ResolveProfile(ProfileReadOnly, workspace, config)
	if err != nil {
		t.Fatalf("ResolveProfile failed: %v", err)
	}
	if profile.Name != "read-only" {
		t.Errorf("Name = %q, want read-only", profile.Name)
	}
	if !profile.DefaultRead {
		t.Error("read-only should have DefaultRead=true")
	}
	if !profile.RestrictNetwork {
		t.Error("read-only should restrict network")
	}
}

func TestResolveProfileStrict(t *testing.T) {
	workspace := t.TempDir()
	config := &SandboxConfig{Profiles: make(map[string]ProfileConfig)}

	profile, err := ResolveProfile(ProfileStrict, workspace, config)
	if err != nil {
		t.Fatalf("ResolveProfile failed: %v", err)
	}
	if profile.Name != "strict" {
		t.Errorf("Name = %q, want strict", profile.Name)
	}
	if profile.DefaultRead {
		t.Error("strict should have DefaultRead=false")
	}
	if !profile.RestrictNetwork {
		t.Error("strict should restrict network")
	}
	if len(profile.ReadOnlyPaths) == 0 {
		t.Error("strict should have read-only paths")
	}
}

func TestResolveProfileOff(t *testing.T) {
	workspace := t.TempDir()
	config := &SandboxConfig{Profiles: make(map[string]ProfileConfig)}

	_, err := ResolveProfile(ProfileOff, workspace, config)
	if err == nil {
		t.Error("ResolveProfile(off) should return error")
	}
}

func TestResolveProfileCustomNotFound(t *testing.T) {
	workspace := t.TempDir()
	config := &SandboxConfig{Profiles: make(map[string]ProfileConfig)}

	_, err := ResolveProfile(ProfileName("nonexistent"), workspace, config)
	if err == nil {
		t.Error("ResolveProfile(nonexistent) should return error")
	}
}

func TestResolveProfileCustomExtendsWorkspace(t *testing.T) {
	workspace := t.TempDir()
	restrictNet := true
	config := &SandboxConfig{
		Profiles: map[string]ProfileConfig{
			"project": {
				Extends:         "workspace",
				RestrictNetwork: &restrictNet,
				ReadOnly:        []string{"/data"},
				Deny:            []string{"/data/private"},
			},
		},
	}

	profile, err := ResolveProfile(ProfileName("project"), workspace, config)
	if err != nil {
		t.Fatalf("ResolveProfile failed: %v", err)
	}
	if profile.Name != "project" {
		t.Errorf("Name = %q, want project", profile.Name)
	}
	if !profile.RestrictNetwork {
		t.Error("custom profile should inherit restrict_network=true")
	}
	if !contains(profile.ReadOnlyPaths, "/data") {
		t.Error("custom profile should have /data in read-only paths")
	}
	if !contains(profile.DenyPaths, "/data/private") {
		t.Error("custom profile should have /data/private in deny paths")
	}
}

func TestResolveProfileCustomExtendsOff(t *testing.T) {
	workspace := t.TempDir()
	config := &SandboxConfig{
		Profiles: map[string]ProfileConfig{
			"broken": {
				Extends: "off",
			},
		},
	}

	_, err := ResolveProfile(ProfileName("broken"), workspace, config)
	if err == nil {
		t.Error("extends=off should return error")
	}
}

func TestResolveProfileCustomExtendsCustom(t *testing.T) {
	workspace := t.TempDir()
	config := &SandboxConfig{
		Profiles: map[string]ProfileConfig{
			"first": {
				Extends: "workspace",
			},
			"second": {
				Extends: "first",
			},
		},
	}

	_, err := ResolveProfile(ProfileName("second"), workspace, config)
	if err == nil {
		t.Error("extends=custom should return error")
	}
}

func TestLoadSandboxConfigFromYAML(t *testing.T) {
	workspace := t.TempDir()

	// Create project-level sandbox.yaml
	yamlContent := `profiles:
  project:
    extends: workspace
    restrict_network: true
    read_only:
      - /data
    deny:
      - .env
`
	covoDir := filepath.Join(workspace, ".covo-agent")
	if err := os.MkdirAll(covoDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(covoDir, "sandbox.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}

	config := LoadSandboxConfig(workspace)
	if config == nil {
		t.Fatal("LoadSandboxConfig returned nil")
	}
	profileCfg, ok := config.Profiles["project"]
	if !ok {
		t.Fatal("project profile not found")
	}
	if profileCfg.Extends != "workspace" {
		t.Errorf("Extends = %q, want workspace", profileCfg.Extends)
	}
	if profileCfg.RestrictNetwork == nil || !*profileCfg.RestrictNetwork {
		t.Error("RestrictNetwork should be true")
	}
	if len(profileCfg.ReadOnly) != 1 || profileCfg.ReadOnly[0] != "/data" {
		t.Errorf("ReadOnly = %v, want [/data]", profileCfg.ReadOnly)
	}
	if len(profileCfg.Deny) != 1 || profileCfg.Deny[0] != ".env" {
		t.Errorf("Deny = %v, want [.env]", profileCfg.Deny)
	}
}

func TestProjectConfigCannotRedefineGlobalProfile(t *testing.T) {
	// Set up a temp COVO_AGENT_HOME
	tmpHome := t.TempDir()
	t.Setenv("COVO_AGENT_HOME", tmpHome)

	// Write global config
	globalYAML := `profiles:
  secure:
    extends: workspace
    restrict_network: true
    deny:
      - /home/user/.ssh
`
	globalPath := filepath.Join(tmpHome, "sandbox.yaml")
	if err := os.WriteFile(globalPath, []byte(globalYAML), 0644); err != nil {
		t.Fatal(err)
	}

	// Write project config that tries to redefine "secure" with weaker rules
	workspace := t.TempDir()
	covoDir := filepath.Join(workspace, ".covo-agent")
	os.MkdirAll(covoDir, 0755)
	projectYAML := `profiles:
  secure:
    extends: workspace
    restrict_network: false
    read_write:
      - /
    deny: []
  project-only:
    extends: workspace
    deny:
      - ./secrets
`
	projectPath := filepath.Join(covoDir, "sandbox.yaml")
	os.WriteFile(projectPath, []byte(projectYAML), 0644)

	config := LoadSandboxConfig(workspace)

	// Global "secure" should be preserved
	secureProfile := config.Profiles["secure"]
	if len(secureProfile.Deny) == 0 || secureProfile.Deny[0] != "/home/user/.ssh" {
		t.Errorf("global deny should be preserved, got %v", secureProfile.Deny)
	}
	if secureProfile.RestrictNetwork == nil || !*secureProfile.RestrictNetwork {
		t.Error("global restrict_network should be preserved")
	}
	if len(secureProfile.ReadWrite) != 0 {
		t.Error("project should not widen global read_write")
	}

	// New project-only profile should be allowed
	if _, exists := config.Profiles["project-only"]; !exists {
		t.Error("project-only profile should be allowed")
	}
}

func TestProfileConflicts(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("COVO_AGENT_HOME", tmpHome)

	// Global config
	globalYAML := `profiles:
  dev:
    extends: workspace
    restrict_network: false
`
	globalPath := filepath.Join(tmpHome, "sandbox.yaml")
	os.WriteFile(globalPath, []byte(globalYAML), 0644)

	// Project config with a conflicting "dev" profile
	workspace := t.TempDir()
	covoDir := filepath.Join(workspace, ".covo-agent")
	os.MkdirAll(covoDir, 0755)
	projectYAML := `profiles:
  dev:
    extends: workspace
    restrict_network: true
`
	projectPath := filepath.Join(covoDir, "sandbox.yaml")
	os.WriteFile(projectPath, []byte(projectYAML), 0644)

	conflicts := ProfileConflicts(workspace)
	if len(conflicts) != 1 || conflicts[0] != "dev" {
		t.Errorf("conflicts = %v, want [dev]", conflicts)
	}
}

func TestIsGlob(t *testing.T) {
	if !IsGlob("*.go") {
		t.Error("*.go should be glob")
	}
	if !IsGlob("data/[abc]") {
		t.Error("data/[abc] should be glob")
	}
	if !IsGlob("file?.txt") {
		t.Error("file?.txt should be glob")
	}
	if IsGlob("/data/file.txt") {
		t.Error("/data/file.txt should not be glob")
	}
}

func TestApplyWithOffProfile(t *testing.T) {
	result := Apply(ApplyOptions{
		Profile:   ProfileOff,
		Workspace: t.TempDir(),
		Logger:    testLogger(),
	})
	if result.Applied {
		t.Error("off profile should not be applied")
	}
	if result.Profile != "off" {
		t.Errorf("Profile = %q, want off", result.Profile)
	}
}

func TestApplyWithInvalidProfile(t *testing.T) {
	result := Apply(ApplyOptions{
		Profile:   ProfileName("nonexistent-profile"),
		Workspace: t.TempDir(),
		Logger:    testLogger(),
	})
	if result.Applied {
		t.Error("nonexistent profile should not be applied")
	}
}

func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
