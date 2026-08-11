package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSessionCommandRegistersSubcommands(t *testing.T) {
	cmd := newSessionCommand(&commandRuntime{homeDir: t.TempDir()})
	want := []string{"delete", "export", "import", "list", "prune", "rename", "search", "stats"}

	for _, name := range want {
		child, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("find session command %q: %v", name, err)
		}
		if child == cmd || child.Name() != name {
			t.Errorf("session command %q was not registered", name)
		}
	}
}

func TestSessionCommandValidatesArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "delete", args: []string{"delete"}},
		{name: "export", args: []string{"export"}},
		{name: "rename", args: []string{"rename", "session-id"}},
		{name: "search", args: []string{"search"}},
		{name: "import", args: []string{"import"}},
		{name: "stats extra", args: []string{"stats", "extra"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newSessionCommand(&commandRuntime{homeDir: t.TempDir()})
			cmd.SetArgs(tt.args)
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			if err := cmd.Execute(); err == nil {
				t.Fatalf("session %v unexpectedly accepted invalid arguments", tt.args)
			}
		})
	}
}

func TestSessionCommandListAndStatsEmptyStore(t *testing.T) {
	homeDir := t.TempDir()

	listOutput := executeSessionCommand(t, homeDir, "list")
	if !strings.Contains(listOutput, "No sessions.") {
		t.Fatalf("list output = %q, want empty-store message", listOutput)
	}

	statsOutput := executeSessionCommand(t, homeDir, "stats")
	for _, want := range []string{"Total sessions: 0", "Total messages: 0"} {
		if !strings.Contains(statsOutput, want) {
			t.Errorf("stats output missing %q: %q", want, statsOutput)
		}
	}
}

func TestParsePruneDays(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    int
		wantErr bool
	}{
		{name: "default", want: 30},
		{name: "explicit", args: []string{"14"}, want: 14},
		{name: "negative means all", args: []string{"-1"}, want: 0},
		{name: "invalid", args: []string{"soon"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePruneDays(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parsePruneDays(%v) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("parsePruneDays(%v) = %d, want %d", tt.args, got, tt.want)
			}
		})
	}
}

func TestShortSessionID(t *testing.T) {
	if got := shortSessionID("short"); got != "short" {
		t.Fatalf("shortSessionID(short) = %q", got)
	}
	if got := shortSessionID("1234567890"); got != "12345678" {
		t.Fatalf("shortSessionID(long) = %q", got)
	}
}

func executeSessionCommand(t *testing.T, homeDir string, args ...string) string {
	t.Helper()
	cmd := newSessionCommand(&commandRuntime{homeDir: homeDir})
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs(args)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute session %v: %v", args, err)
	}
	return output.String()
}
