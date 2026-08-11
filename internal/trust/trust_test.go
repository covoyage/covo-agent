package trust

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrustStore_GrantAndCheck(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "trusted.toml")
	store, err := NewTrustStore(storePath)
	if err != nil {
		t.Fatalf("NewTrustStore: %v", err)
	}

	workspace := filepath.Join(dir, "project")
	os.MkdirAll(workspace, 0o755)

	if store.IsTrusted(workspace) {
		t.Fatal("expected untrusted before grant")
	}

	if err := store.Grant(workspace); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	if !store.IsTrusted(workspace) {
		t.Fatal("expected trusted after grant")
	}
}

func TestTrustStore_AncestorTrusted(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "trusted.toml")
	store, _ := NewTrustStore(storePath)

	parent := filepath.Join(dir, "parent")
	child := filepath.Join(parent, "child", "grandchild")
	os.MkdirAll(child, 0o755)

	if err := store.Grant(parent); err != nil {
		t.Fatalf("Grant parent: %v", err)
	}

	if !store.IsTrusted(child) {
		t.Fatal("expected child trusted when parent is trusted")
	}
}

func TestTrustStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "trusted.toml")
	store, _ := NewTrustStore(storePath)

	workspace := filepath.Join(dir, "project")
	os.MkdirAll(workspace, 0o755)

	store.Grant(workspace)

	// Create a new store from the same path
	store2, err := NewTrustStore(storePath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if !store2.IsTrusted(workspace) {
		t.Fatal("expected trusted after reload")
	}
}

func TestTrustStore_Revoke(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "trusted.toml")
	store, _ := NewTrustStore(storePath)

	workspace := filepath.Join(dir, "project")
	os.MkdirAll(workspace, 0o755)

	store.Grant(workspace)
	if !store.IsTrusted(workspace) {
		t.Fatal("expected trusted")
	}

	store.Revoke(workspace)
	if store.IsTrusted(workspace) {
		t.Fatal("expected untrusted after revoke")
	}
}

func TestScanCodeExecConfigs_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	configs := ScanCodeExecConfigs(dir)
	if len(configs) != 0 {
		t.Fatalf("expected 0 configs in empty dir, got %d", len(configs))
	}
}

func TestScanCodeExecConfigs_DetectsHooks(t *testing.T) {
	dir := t.TempDir()

	// Create covo-agent hooks directory
	hooksDir := filepath.Join(dir, ".covo-agent", "hooks")
	os.MkdirAll(hooksDir, 0o755)

	// Create .envrc
	os.WriteFile(filepath.Join(dir, ".envrc"), []byte("export FOO=bar"), 0o644)

	configs := ScanCodeExecConfigs(dir)
	if len(configs) < 2 {
		t.Fatalf("expected at least 2 configs, got %d", len(configs))
	}

	types := map[string]bool{}
	for _, c := range configs {
		types[c.Type] = true
	}
	if !types["hooks"] {
		t.Error("expected hooks config detected")
	}
	if !types["envrc"] {
		t.Error("expected envrc config detected")
	}
}

func TestScanCodeExecConfigs_DetectsClaude(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0o755)
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte("{}"), 0o644)

	configs := ScanCodeExecConfigs(dir)
	found := false
	for _, c := range configs {
		if c.Type == "hooks" {
			found = true
		}
	}
	if !found {
		t.Error("expected Claude settings detected as hooks")
	}
}

func TestScanCodeExecConfigs_DetectsGrok(t *testing.T) {
	dir := t.TempDir()
	grokDir := filepath.Join(dir, ".grok")
	os.MkdirAll(grokDir, 0o755)
	os.WriteFile(filepath.Join(grokDir, "config.toml"), []byte("test"), 0o644)

	configs := ScanCodeExecConfigs(dir)
	found := false
	for _, c := range configs {
		if c.Type == "config" {
			found = true
		}
	}
	if !found {
		t.Error("expected Grok config detected")
	}
}

func TestIsUnsafeRoot(t *testing.T) {
	if !IsUnsafeRoot("/") {
		t.Error("root should be unsafe")
	}
	home, _ := os.UserHomeDir()
	if home != "" && !IsUnsafeRoot(home) {
		t.Error("home dir should be unsafe")
	}
	temp := t.TempDir()
	if IsUnsafeRoot(temp) {
		t.Error("temp dir should be safe")
	}
}

func TestDecide_FeatureDisabled(t *testing.T) {
	dir := t.TempDir()
	outcome, configs := Decide(dir, nil, false, false)
	if outcome != OutcomeTrusted {
		t.Fatalf("expected trusted when feature disabled, got %s", outcome)
	}
	if len(configs) != 0 {
		t.Fatal("expected no configs")
	}
}

func TestDecide_AlreadyTrusted(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "trusted.toml")
	store, _ := NewTrustStore(storePath)

	workspace := filepath.Join(dir, "project")
	os.MkdirAll(workspace, 0o755)
	store.Grant(workspace)

	// Even with code-exec configs, trusted in store → trusted
	hooksDir := filepath.Join(workspace, ".covo-agent", "hooks")
	os.MkdirAll(hooksDir, 0o755)

	outcome, _ := Decide(workspace, store, false, true)
	if outcome != OutcomeTrusted {
		t.Fatalf("expected trusted from store, got %s", outcome)
	}
}

func TestDecide_NoConfigs(t *testing.T) {
	dir := t.TempDir()
	outcome, configs := Decide(dir, nil, false, true)
	if outcome != OutcomeTrusted {
		t.Fatalf("expected trusted with no configs, got %s", outcome)
	}
	if len(configs) != 0 {
		t.Fatalf("expected 0 configs, got %d", len(configs))
	}
}

func TestDecide_HeadlessUntrusted(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, ".covo-agent", "hooks")
	os.MkdirAll(hooksDir, 0o755)

	outcome, configs := Decide(dir, nil, false, true)
	if outcome != OutcomeUntrusted {
		t.Fatalf("expected untrusted in headless with configs, got %s", outcome)
	}
	if len(configs) == 0 {
		t.Fatal("expected configs")
	}
}

func TestDecide_InteractivePrompt(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, ".covo-agent", "hooks")
	os.MkdirAll(hooksDir, 0o755)

	outcome, configs := Decide(dir, nil, true, true)
	if outcome != OutcomePrompt {
		t.Fatalf("expected prompt in interactive with configs, got %s", outcome)
	}
	if len(configs) == 0 {
		t.Fatal("expected configs")
	}
}

func TestDecide_UnsafeRoot(t *testing.T) {
	home, _ := os.UserHomeDir()
	if home != "" {
		outcome, _ := Decide(home, nil, false, true)
		if outcome != OutcomeTrusted {
			t.Fatalf("expected trusted for home dir, got %s", outcome)
		}
	}
}

func TestCheckAndPrompt_HeadlessUntrusted(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, ".covo-agent", "hooks")
	os.MkdirAll(hooksDir, 0o755)

	trusted, configs := CheckAndPrompt(dir, nil, false, true)
	if trusted {
		t.Error("expected untrusted in headless")
	}
	if len(configs) == 0 {
		t.Error("expected configs")
	}
}

func TestCheckAndPrompt_NoConfigsTrusted(t *testing.T) {
	dir := t.TempDir()
	trusted, _ := CheckAndPrompt(dir, nil, false, true)
	if !trusted {
		t.Error("expected trusted with no configs")
	}
}

func TestCheckAndPrompt_Disabled(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, ".covo-agent", "hooks")
	os.MkdirAll(hooksDir, 0o755)

	trusted, _ := CheckAndPrompt(dir, nil, false, false)
	if !trusted {
		t.Error("expected trusted when disabled")
	}
}

func TestTrustStore_All(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "trusted.toml")
	store, _ := NewTrustStore(storePath)

	w1 := filepath.Join(dir, "project1")
	w2 := filepath.Join(dir, "project2")
	os.MkdirAll(w1, 0o755)
	os.MkdirAll(w2, 0o755)

	store.Grant(w1)
	store.Grant(w2)

	all := store.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}
}

func TestOutcome_String(t *testing.T) {
	if OutcomeTrusted.String() != "trusted" {
		t.Error("bad string")
	}
	if OutcomeUntrusted.String() != "untrusted" {
		t.Error("bad string")
	}
	if OutcomePrompt.String() != "prompt" {
		t.Error("bad string")
	}
}
