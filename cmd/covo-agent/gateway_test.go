package main

import (
	"path/filepath"
	"testing"
)

func TestGatewayCommandRegistersSubcommands(t *testing.T) {
	cmd := newGatewayCommand(&commandRuntime{homeDir: t.TempDir()})
	want := []string{"setup", "start", "status", "stop"}

	for _, name := range want {
		child, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("find gateway command %q: %v", name, err)
		}
		if child == cmd || child.Name() != name {
			t.Errorf("gateway command %q was not registered", name)
		}
	}
}

func TestGatewayCommandRejectsExtraArguments(t *testing.T) {
	for _, name := range []string{"setup", "start", "status", "stop"} {
		cmd := newGatewayCommand(&commandRuntime{homeDir: t.TempDir()})
		cmd.SetArgs([]string{name, "extra"})
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.Execute(); err == nil {
			t.Fatalf("gateway %s unexpectedly accepted an extra argument", name)
		}
	}
}

func TestGatewayPIDFile(t *testing.T) {
	homeDir := t.TempDir()
	want := filepath.Join(homeDir, "gateway.pid")
	if got := gatewayPIDFile(homeDir); got != want {
		t.Fatalf("gatewayPIDFile() = %q, want %q", got, want)
	}
}
