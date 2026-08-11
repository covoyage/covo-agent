package ossandbox

import (
	"strings"
	"testing"
)

func TestGenerateSeatbeltProfile_Workspace(t *testing.T) {
	profile := &SandboxProfile{
		Name:            "workspace",
		ReadWritePaths:  []string{"/workspace", "/tmp", "/home/user/.covo-agent"},
		DefaultRead:     true,
		RestrictNetwork: false,
	}
	policy := generateSeatbeltProfile(profile, "/workspace")

	if !strings.Contains(policy, "(version 1)") {
		t.Error("missing version directive")
	}
	if !strings.Contains(policy, "(deny default)") {
		t.Error("missing deny default")
	}
	// Network is always allowed (agent needs LLM API)
	if !strings.Contains(policy, "(allow network*)") {
		t.Error("network should always be allowed for agent process")
	}
	if !strings.Contains(policy, "(allow file-read* (subpath \"/workspace\"))") {
		t.Error("should allow read of workspace")
	}
	if !strings.Contains(policy, "(allow file-write* (subpath \"/workspace\"))") {
		t.Error("should allow write of workspace")
	}
	if !strings.Contains(policy, "/dev/null") {
		t.Error("should allow /dev/null")
	}
}

func TestGenerateSeatbeltProfile_Strict(t *testing.T) {
	profile := &SandboxProfile{
		Name:            "strict",
		ReadOnlyPaths:   []string{"/custom/readonly"},
		ReadWritePaths:  []string{"/workspace", "/tmp"},
		DefaultRead:     false,
		RestrictNetwork: true,
	}
	policy := generateSeatbeltProfile(profile, "/workspace")

	// strict should NOT have blanket file-read*
	if strings.Contains(policy, "(allow file-read*)\n") {
		t.Error("strict should not have default file-read*")
	}
	// Network is always allowed even in strict mode (agent needs LLM API)
	if !strings.Contains(policy, "(allow network*)") {
		t.Error("network should always be allowed for agent process")
	}
	// Strict should still have system read paths
	if !strings.Contains(policy, `(subpath "/usr")`) {
		t.Error("should have /usr in read-only paths")
	}
	// Should have the custom readonly path
	if !strings.Contains(policy, `/custom/readonly`) {
		t.Error("should have custom readonly path")
	}
}

func TestGenerateSeatbeltProfile_AlwaysAllowsNetwork(t *testing.T) {
	// Even with RestrictNetwork=true, the agent process itself must have network
	// to reach LLM APIs. Child process restriction is handled separately.
	profile := &SandboxProfile{
		Name:            "read-only",
		ReadWritePaths:  []string{"/tmp"},
		DefaultRead:     true,
		RestrictNetwork: true,
	}
	policy := generateSeatbeltProfile(profile, "/workspace")

	if strings.Contains(policy, "(deny network*)") {
		t.Error("should NOT deny network — agent always needs LLM API access")
	}
	if !strings.Contains(policy, "(allow network*)") {
		t.Error("should always allow network for agent process")
	}
}

func TestGenerateSeatbeltProfile_DenyPaths(t *testing.T) {
	profile := &SandboxProfile{
		Name:            "custom",
		ReadWritePaths:  []string{"/workspace"},
		DenyPaths:       []string{"/workspace/.env"},
		DefaultRead:     true,
		RestrictNetwork: false,
	}
	policy := generateSeatbeltProfile(profile, "/workspace")

	if !strings.Contains(policy, `(deny file-read* (subpath "/workspace/.env"))`) {
		t.Error("should deny read of .env")
	}
	if !strings.Contains(policy, `(deny file-write* (subpath "/workspace/.env"))`) {
		t.Error("should deny write of .env")
	}
}

func TestGenerateSeatbeltProfile_MetadataAndThread(t *testing.T) {
	profile := &SandboxProfile{
		Name:            "workspace",
		ReadWritePaths:  []string{"/workspace"},
		DefaultRead:     true,
	}
	policy := generateSeatbeltProfile(profile, "/workspace")

	// These are essential for process operation
	if !strings.Contains(policy, "(allow file-read-metadata)") {
		t.Error("should allow file-read-metadata (needed for stat)")
	}
	if !strings.Contains(policy, "(allow thread*)") {
		t.Error("should allow thread* (needed for goroutines)")
	}
	if !strings.Contains(policy, "(allow process-info*)") {
		t.Error("should allow process-info*")
	}
}

func TestGenerateSeatbeltProfile_DNSInfra(t *testing.T) {
	profile := &SandboxProfile{
		Name:            "workspace",
		ReadWritePaths:  []string{"/workspace"},
		DefaultRead:     true,
	}
	policy := generateSeatbeltProfile(profile, "/workspace")

	// DNS infrastructure must be readable for network to work
	if !strings.Contains(policy, "/etc/resolv.conf") {
		t.Error("should allow reading /etc/resolv.conf for DNS")
	}
	if !strings.Contains(policy, "/etc/hosts") {
		t.Error("should allow reading /etc/hosts")
	}
}

func TestGenerateSeatbeltProfile_SSHKeysDenied(t *testing.T) {
	profile := &SandboxProfile{
		Name:            "workspace",
		ReadWritePaths:  []string{"/workspace"},
		DefaultRead:     true,
	}
	policy := generateSeatbeltProfile(profile, "/workspace")

	// SSH keys should be write-denied
	if !strings.Contains(policy, ".ssh") {
		t.Error("should deny writes to ~/.ssh")
	}
}

func TestResolvePath(t *testing.T) {
	workspace := "/home/user/project"

	tests := []struct {
		input string
		want  string
	}{
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "/home/user/project/relative/path"},
		{".env", "/home/user/project/.env"},
		{"../sibling", "/home/user/sibling"},
	}
	for _, tc := range tests {
		got := resolvePath(tc.input, workspace)
		if got != tc.want {
			t.Errorf("resolvePath(%q, %q) = %q, want %q", tc.input, workspace, got, tc.want)
		}
	}
}

func TestGenerateSeatbeltProfile_SystemPathsDenied(t *testing.T) {
	profile := &SandboxProfile{
		Name:            "workspace",
		ReadWritePaths:  []string{"/workspace"},
		DefaultRead:     true,
		RestrictNetwork: false,
	}
	policy := generateSeatbeltProfile(profile, "/workspace")

	if !strings.Contains(policy, `(deny file-write* (subpath "/System"))`) {
		t.Error("should deny write to /System")
	}
	if !strings.Contains(policy, `(deny file-write* (subpath "/usr"))`) {
		t.Error("should deny write to /usr")
	}
	if !strings.Contains(policy, `(deny file-write* (subpath "/bin"))`) {
		t.Error("should deny write to /bin")
	}
}
