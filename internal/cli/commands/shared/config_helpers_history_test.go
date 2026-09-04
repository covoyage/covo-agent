package shared

import (
	"testing"

	"github.com/covoyage/covo-agent/internal/cli"
)

func TestHistoryModeFromCLIDefaultsVirtualized(t *testing.T) {
	t.Setenv("COVO_HISTORY_MODE", "")
	if got := HistoryModeFromCLI(nil); got != "virtualized" {
		t.Fatalf("nil config: got %q", got)
	}
	if got := HistoryModeFromCLI(&cli.Config{}); got != "virtualized" {
		t.Fatalf("empty config: got %q", got)
	}
}

func TestHistoryModeFromCLIScrollback(t *testing.T) {
	t.Setenv("COVO_HISTORY_MODE", "")
	cfg := &cli.Config{Display: &cli.DisplayConfig{HistoryMode: "scrollback"}}
	if got := HistoryModeFromCLI(cfg); got != "scrollback" {
		t.Fatalf("got %q, want scrollback", got)
	}
	cfg.Display.HistoryMode = "native"
	if got := HistoryModeFromCLI(cfg); got != "scrollback" {
		t.Fatalf("native alias: got %q, want scrollback", got)
	}
}
