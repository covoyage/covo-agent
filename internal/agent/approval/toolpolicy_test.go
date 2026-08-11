package approval

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/covoyage/covo-agent/internal/cli"
)

func writeTestPolicy(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	return path
}

func resetToolPolicy() {
	globalToolPolicy.mu.Lock()
	globalToolPolicy.rules = toolGroupRules{}
	globalToolPolicy.mu.Unlock()
}

// enableExecPolicy overrides the exec-policy feature flag for tests.
func enableExecPolicy(t *testing.T) {
	t.Helper()
	cli.Override("exec-policy", true)
	t.Cleanup(func() { cli.Override("exec-policy", false) })
}

func TestCheckToolPolicy_NoRules(t *testing.T) {
	enableExecPolicy(t)
	resetToolPolicy()
	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}

	d := sys.CheckToolPolicy("edit_block")
	if d != nil {
		t.Errorf("expected nil Decision when no tool policy configured, got: %+v", d)
	}
}

func TestCheckToolPolicy_DenyGroup(t *testing.T) {
	enableExecPolicy(t)
	resetToolPolicy()
	defer resetToolPolicy()

	// Load a policy that denies the "edit" group.
	path := writeTestPolicy(t, `
tools:
  deny:
    - edit
`)
	if err := LoadPolicy(path); err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	// LoadPolicy checks cli.IsEnabled("exec-policy") — we need to enable it
	// by calling the inner load directly. But LoadPolicy itself doesn't check
	// the flag (only LoadPolicyFromDirs does). So the policy should be loaded.

	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}

	// All file-mutation tools in the "edit" group should be denied.
	for _, tool := range []string{"edit_block", "write_file", "edit", "apply_patch", "move", "delete_file"} {
		d := sys.CheckToolPolicy(tool)
		if d == nil {
			t.Errorf("expected deny for tool %q", tool)
			continue
		}
		if d.Approved {
			t.Errorf("expected tool %q to be denied", tool)
		}
	}

	// Non-edit tools should not match (nil = no policy rule).
	for _, tool := range []string{"bash", "web_fetch", "read"} {
		d := sys.CheckToolPolicy(tool)
		if d != nil {
			t.Errorf("expected nil for non-edit tool %q, got: %+v", tool, d)
		}
	}
}

func TestCheckToolPolicy_AllowGroup(t *testing.T) {
	enableExecPolicy(t)
	resetToolPolicy()
	defer resetToolPolicy()

	path := writeTestPolicy(t, `
tools:
  allow:
    - read
`)
	if err := LoadPolicy(path); err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}

	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}

	// Read-group tools should be allowed.
	for _, tool := range []string{"read", "read_file", "glob", "grep", "ls"} {
		d := sys.CheckToolPolicy(tool)
		if d == nil {
			t.Errorf("expected allow for read-group tool %q", tool)
			continue
		}
		if !d.Approved {
			t.Errorf("expected read-group tool %q to be approved", tool)
		}
	}

	// Non-read tools should not match.
	d := sys.CheckToolPolicy("edit_block")
	if d != nil {
		t.Errorf("expected nil for non-read tool, got: %+v", d)
	}
}

func TestCheckToolPolicy_DenyOverridesAllow(t *testing.T) {
	enableExecPolicy(t)
	resetToolPolicy()
	defer resetToolPolicy()

	// "edit" is in both deny and allow — deny should win.
	path := writeTestPolicy(t, `
tools:
  allow:
    - edit
  deny:
    - edit
`)
	if err := LoadPolicy(path); err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}

	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}

	d := sys.CheckToolPolicy("edit_block")
	if d == nil {
		t.Fatalf("expected non-nil decision for tool in both lists")
	}
	if d.Approved {
		t.Errorf("expected deny to take precedence over allow")
	}
}

func TestCheckToolPolicy_IndividualToolName(t *testing.T) {
	enableExecPolicy(t)
	resetToolPolicy()
	defer resetToolPolicy()

	// Deny a single tool name, not a group alias.
	path := writeTestPolicy(t, `
tools:
  deny:
    - apply_patch
`)
	if err := LoadPolicy(path); err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}

	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}

	// Only apply_patch should be denied, not other edit-group tools.
	d := sys.CheckToolPolicy("apply_patch")
	if d == nil || d.Approved {
		t.Errorf("expected apply_patch to be denied")
	}

	d = sys.CheckToolPolicy("edit_block")
	if d != nil {
		t.Errorf("expected nil for edit_block (not in deny list), got: %+v", d)
	}
}

func TestExpandToolNames(t *testing.T) {
	result := expandToolNames([]string{"edit", "bash", "custom_tool"})
	expected := map[string]bool{
		// edit group
		"write_file": true, "write": true, "edit_block": true, "edit": true,
		"apply_patch": true, "patch": true, "move": true, "delete_file": true,
		"str_replace_editor": true,
		// bash group
		"bash": true, "process": true,
		// individual
		"custom_tool": true,
	}
	for tool := range expected {
		if !result[tool] {
			t.Errorf("expected tool %q to be in expanded set", tool)
		}
	}
	if len(result) != len(expected) {
		t.Errorf("expected %d tools, got %d", len(expected), len(result))
	}
}
