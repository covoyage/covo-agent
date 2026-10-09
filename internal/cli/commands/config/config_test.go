package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli"
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

func TestSetConfigValueRejectsInvalidMode(t *testing.T) {
	err := setConfigValue(&bytes.Buffer{}, &cli.Config{}, "mode", "pentest")
	if err == nil || !strings.Contains(err.Error(), "invalid mode") {
		t.Fatalf("setConfigValue error = %v, want invalid mode", err)
	}
}

func TestCheckConfigFileRejectsInvalidMode(t *testing.T) {
	t.Setenv("AGENT_MODE", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("mode: pentest\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := checkConfigFile(&bytes.Buffer{}, &cli.Config{Mode: "pentest"}, path)
	if err == nil || !strings.Contains(err.Error(), "invalid mode") {
		t.Fatalf("checkConfigFile error = %v, want invalid mode", err)
	}
}

func TestCompleteModeNames(t *testing.T) {
	names := completeModeNames("cy")
	found := false
	for _, name := range names {
		if name == "cyber" {
			found = true
		}
	}
	if !found {
		t.Fatalf("completeModeNames(cy)=%v, want cyber", names)
	}
}

func TestCompleteModeNamesIncludesCustom(t *testing.T) {
	t.Cleanup(agent.ClearCustomModes)
	agent.ClearCustomModes()
	agent.RegisterCustomMode(&agent.CustomModeDefinition{Name: "reviewer"})
	found := false
	for _, name := range completeModeNames("rev") {
		if name == "reviewer" {
			found = true
		}
	}
	if !found {
		t.Fatal("completeModeNames should include registered custom modes")
	}
}
