package tools

import (
	"os"
	"testing"
)

func TestExportSecretDoesNotReturnValue(t *testing.T) {
	dir := t.TempDir()
	store := NewSecretsStore(dir)
	if err := store.Set("github-token", "super-secret"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "")

	out, err := exportSecret(store, "github-token")
	if err != nil {
		t.Fatal(err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("unexpected type %T", out)
	}
	if _, has := m["value"]; has {
		t.Fatal("secret value must not appear in the tool result")
	}
	if m["env_var"] != "GITHUB_TOKEN" {
		t.Fatalf("env_var = %v", m["env_var"])
	}
	if os.Getenv("GITHUB_TOKEN") != "super-secret" {
		t.Fatal("expected secret to be exported to the process environment")
	}
}
