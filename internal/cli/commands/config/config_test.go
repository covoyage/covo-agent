package config

import (
	"bytes"
	"github.com/covoyage/covo-agent/internal/cli"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCommandRegistersSubcommands(t *testing.T) {
	runtime := &cli.CommandRuntime{Cfg: &cli.Config{}, HomeDir: t.TempDir()}
	cmd := NewConfigCommand(runtime)

	for _, name := range []string{"show", "path", "edit", "check", "set", "schema"} {
		subcommand, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("find config %s: %v", name, err)
		}
		if subcommand == cmd || subcommand.Name() != name {
			t.Errorf("config subcommand %q was not registered", name)
		}
	}
}

func TestConfigCommandSetRequiresKeyAndValue(t *testing.T) {
	runtime := &cli.CommandRuntime{Cfg: &cli.Config{}, HomeDir: t.TempDir()}
	cmd := NewConfigCommand(runtime)
	cmd.SetArgs([]string{"set", "model"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "accepts 2 arg(s)") {
		t.Fatalf("config set error = %v, want exact-args error", err)
	}
}

func TestWriteConfigSummary(t *testing.T) {
	cfg := &cli.Config{Provider: "openai", Model: "gpt-test", Mode: "code"}
	var output bytes.Buffer

	writeConfigSummary(&output, cfg, "/tmp/covo-home")

	for _, want := range []string{"/tmp/covo-home", "openai", "gpt-test", "code"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("summary missing %q: %s", want, output.String())
		}
	}
}

func TestCheckConfigFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	err := checkConfigFile(&bytes.Buffer{}, &cli.Config{}, path)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("checkConfigFile error = %v, want not found", err)
	}
}

func TestSetConfigValueRejectsUnknownKey(t *testing.T) {
	err := setConfigValue(&bytes.Buffer{}, &cli.Config{}, "unknown", "value")
	if err == nil || !strings.Contains(err.Error(), "unknown config key") {
		t.Fatalf("setConfigValue error = %v, want unknown key", err)
	}
}
