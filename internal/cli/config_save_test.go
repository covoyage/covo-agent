package cli

import (
	"github.com/covoyage/covo-agent/internal/fslock"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigSaveRejectsExternalEditsAndLockedWriters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("model: old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfigFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	release, err := fslock.Acquire(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveConfigTo(cfg, dir); err == nil {
		release()
		t.Fatal("profile save bypassed lock")
	}
	release()
	if err := os.WriteFile(path, []byte("model: operator\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Model = "stale"
	if err := SaveConfigTo(cfg, dir); err == nil {
		t.Fatal("external edit overwritten")
	}
	if _, err := configDocument([]byte("model: old\n---\nmodel: hidden\n")); err == nil {
		t.Fatal("multiple documents accepted")
	}
}

func TestConfigSavePreservesUnknownAndRejectsStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := "# operator comment\nprovider: openai\nmodel: old\nenv:\n  TOKEN: ${COVO_TEST_TOKEN}\nfuture_setting: retained\n"
	t.Setenv("COVO_TEST_TOKEN", "expanded-secret")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := LoadConfigFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadConfigFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	first.Model = "new"
	if err := SaveConfigToDir(first, dir); err != nil {
		t.Fatal(err)
	}
	second.Model = "stale"
	if err := SaveConfigToDir(second, dir); err == nil {
		t.Fatal("stale config overwrote update")
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"operator comment", "future_setting: retained", "${COVO_TEST_TOKEN}", "model: new"} {
		if !strings.Contains(string(updated), part) {
			t.Fatalf("lost %s: %s", part, updated)
		}
	}
	if strings.Contains(string(updated), "expanded-secret") {
		t.Fatal("unchanged environment secret persisted")
	}
	first.Model = "newer"
	if err := SaveConfigToDir(first, dir); err != nil {
		t.Fatal("same instance cannot save again", err)
	}
}
