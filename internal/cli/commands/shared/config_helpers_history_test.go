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

func TestDisplayConfigFromCLIThinkingModes(t *testing.T) {
	t.Setenv("COVO_SHOW_REASONING", "")
	t.Setenv("COVO_THINKING_MODE", "")
	show, mode, stream := DisplayConfigFromCLI(&cli.Config{Display: &cli.DisplayConfig{ThinkingMode: "collapsed"}})
	if mode != "summary" {
		t.Fatalf("collapsed alias: %q", mode)
	}
	if !stream {
		t.Fatal("thinking stream should default on")
	}
	off := false
	show, mode, stream = DisplayConfigFromCLI(&cli.Config{Display: &cli.DisplayConfig{ShowReasoning: true, ThinkingMode: "hide", ThinkingStream: &off}})
	if show || mode != "hide" {
		t.Fatalf("hide: show=%v mode=%q", show, mode)
	}
	if stream {
		t.Fatal("thinking stream should honor explicit false")
	}
}

func TestVerboseLevelFromCLI(t *testing.T) {
	t.Setenv("COVO_VERBOSE", "")
	if got := VerboseLevelFromCLI(nil); got != "on" {
		t.Fatalf("default=%q", got)
	}
	if got := VerboseLevelFromCLI(&cli.Config{Display: &cli.DisplayConfig{Verbose: "full"}}); got != "full" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("COVO_VERBOSE", "off")
	if got := VerboseLevelFromCLI(&cli.Config{Display: &cli.DisplayConfig{Verbose: "full"}}); got != "off" {
		t.Fatalf("env override=%q", got)
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
