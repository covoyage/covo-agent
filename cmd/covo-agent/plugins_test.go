package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/plugin"
)

func TestPluginCommandRegistersSubcommands(t *testing.T) {
	cmd := newPluginCommand(&commandRuntime{homeDir: t.TempDir()})
	want := []string{"disable", "enable", "info", "list", "marketplace"}

	for _, name := range want {
		child, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("find plugin command %q: %v", name, err)
		}
		if child == cmd || child.Name() != name {
			t.Errorf("plugin command %q was not registered", name)
		}
	}
}

func TestPluginCommandValidatesArguments(t *testing.T) {
	for _, args := range [][]string{{"info"}, {"enable"}, {"disable"}, {"list", "extra"}} {
		cmd := newPluginCommand(&commandRuntime{homeDir: t.TempDir()})
		cmd.SetArgs(args)
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.Execute(); err == nil {
			t.Fatalf("plugin %v unexpectedly accepted invalid arguments", args)
		}
	}
}

func TestSetPluginEnabledPersistsConfig(t *testing.T) {
	homeDir := t.TempDir()
	system, err := plugin.NewSystem(context.Background(), plugin.SystemConfig{
		HomeDir: homeDir,
		Logger:  slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	})
	if err != nil {
		t.Fatalf("new plugin system: %v", err)
	}
	if err := system.Registry.Register(&plugin.Entry{
		ID:       "test-platform",
		Name:     "Test Platform",
		Category: plugin.CategoryPlatform,
	}); err != nil {
		t.Fatalf("register plugin: %v", err)
	}

	var output bytes.Buffer
	if err := setPluginEnabled(&output, system, "test-platform", true); err != nil {
		t.Fatalf("enable plugin: %v", err)
	}
	if entry := system.Registry.Get("test-platform"); entry == nil || !entry.Enabled {
		t.Fatal("plugin was not enabled")
	}
	if !strings.Contains(output.String(), "enabled") {
		t.Fatalf("enable output = %q", output.String())
	}
	if _, err := os.Stat(filepath.Join(homeDir, "plugins.yaml")); err != nil {
		t.Fatalf("plugin config was not persisted: %v", err)
	}
	if err := setPluginEnabled(&output, system, "missing", true); err == nil {
		t.Fatal("enabling an unknown plugin unexpectedly succeeded")
	}
}

func TestPluginOutputHelpers(t *testing.T) {
	registry := plugin.NewRegistry()
	if err := registry.Register(&plugin.Entry{
		ID:       "telegram",
		Name:     "Telegram",
		Category: plugin.CategoryPlatform,
		Enabled:  true,
	}); err != nil {
		t.Fatalf("register plugin: %v", err)
	}

	var listOutput bytes.Buffer
	writeEnabledPlugins(&listOutput, registry)
	if !strings.Contains(listOutput.String(), "Telegram") {
		t.Fatalf("list output = %q", listOutput.String())
	}

	var infoOutput bytes.Buffer
	if err := writePluginInfo(&infoOutput, registry, "telegram"); err != nil {
		t.Fatalf("plugin info: %v", err)
	}
	for _, want := range []string{"ID:       telegram", "Status:   enabled"} {
		if !strings.Contains(infoOutput.String(), want) {
			t.Errorf("info output missing %q: %q", want, infoOutput.String())
		}
	}

	var marketplaceOutput bytes.Buffer
	writePluginMarketplace(&marketplaceOutput, registry)
	if !strings.Contains(marketplaceOutput.String(), "telegram      enabled") {
		t.Fatalf("marketplace output missing enabled telegram: %q", marketplaceOutput.String())
	}
}
