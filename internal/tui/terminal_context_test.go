package tui

import (
	"testing"
)

func TestDetectTerminalContext_Defaults(t *testing.T) {
	ctx := DetectTerminalContext()
	if ctx == nil {
		t.Fatal("expected context")
	}
	if ctx.OS == "" {
		t.Error("expected non-empty OS")
	}
}

func TestDetectTerminalContext_AppleTerminal(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TMUX", "")

	ctx := DetectTerminalContext()
	if ctx.Brand != BrandAppleTerminal {
		t.Errorf("expected AppleTerminal, got %s", ctx.Brand)
	}
	if ctx.Color != ColorTrueColor {
		t.Errorf("expected truecolor, got %s", ctx.Color)
	}
}

func TestDetectTerminalContext_iTerm2(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	t.Setenv("TMUX", "")
	ctx := DetectTerminalContext()
	if ctx.Brand != BrandITerm2 {
		t.Errorf("expected iTerm2, got %s", ctx.Brand)
	}
	if !ctx.KittyKeyboardLikely {
		t.Error("iTerm2 should likely support kitty keyboard")
	}
}

func TestDetectTerminalContext_Kitty(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "kitty")
	t.Setenv("TMUX", "")
	ctx := DetectTerminalContext()
	if ctx.Brand != BrandKitty {
		t.Errorf("expected Kitty, got %s", ctx.Brand)
	}
	if !ctx.KittyKeyboardLikely {
		t.Error("Kitty should support kitty keyboard")
	}
}

func TestDetectTerminalContext_VSCode(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "vscode")
	t.Setenv("TMUX", "")
	ctx := DetectTerminalContext()
	if !ctx.IsVSCodeFamily() {
		t.Error("vscode should be VS Code family")
	}
	if ctx.QuitKey() != "Ctrl+D" {
		t.Errorf("VS Code quit key should be Ctrl+D, got %s", ctx.QuitKey())
	}
}

func TestDetectTerminalContext_tmux(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	t.Setenv("STY", "")
	ctx := DetectTerminalContext()
	if ctx.Multiplexer != MuxTmux {
		t.Errorf("expected tmux, got %s", ctx.Multiplexer)
	}
	// Kitty in tmux should NOT report kitty keyboard likely (tmux passthrough uncertain)
	t.Setenv("TERM_PROGRAM", "kitty")
	ctx2 := DetectTerminalContext()
	if ctx2.KittyKeyboardLikely {
		t.Error("Kitty in tmux should not report kitty keyboard likely")
	}
}

func TestDetectTerminalContext_screen(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("STY", "12345.pts-0.host")
	ctx := DetectTerminalContext()
	if ctx.Multiplexer != MuxScreen {
		t.Errorf("expected screen, got %s", ctx.Multiplexer)
	}
	if !ctx.CtrlDotUnreliable() {
		t.Error("screen should report Ctrl+. as unreliable")
	}
}

func TestDetectTerminalContext_SSH(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "1.2.3.4 5678 5.6.7.8 22")
	t.Setenv("TMUX", "")
	ctx := DetectTerminalContext()
	if !ctx.OverSSH {
		t.Error("expected over SSH")
	}
}

func TestDetectTerminalContext_DumbTerminal(t *testing.T) {
	t.Setenv("TERM", "dumb")
	t.Setenv("COLORTERM", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TMUX", "")
	ctx := DetectTerminalContext()
	if ctx.Color != ColorNone {
		t.Errorf("expected ColorNone, got %s", ctx.Color)
	}
	if !ctx.IsMinimal() {
		t.Error("dumb terminal should be minimal")
	}
}

func TestTerminalContext_QuitKey(t *testing.T) {
	ctx := &TerminalContext{Brand: BrandITerm2}
	if ctx.QuitKey() != "Ctrl+Q" {
		t.Errorf("non-VSCode should use Ctrl+Q, got %s", ctx.QuitKey())
	}

	ctx = &TerminalContext{Brand: BrandVSCode}
	if ctx.QuitKey() != "Ctrl+D" {
		t.Errorf("VS Code should use Ctrl+D, got %s", ctx.QuitKey())
	}
}

func TestTerminalContext_SupportsTrueColor(t *testing.T) {
	ctx := &TerminalContext{Color: ColorTrueColor}
	if !ctx.SupportsTrueColor() {
		t.Error("truecolor should be supported")
	}
	if !ctx.Supports256Color() {
		t.Error("truecolor implies 256color")
	}

	ctx = &TerminalContext{Color: ColorNone}
	if ctx.SupportsTrueColor() {
		t.Error("no color should not support truecolor")
	}
}
