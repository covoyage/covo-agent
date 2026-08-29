package shared

import (
	"os"
	"runtime"
)

func DefaultMouseMode() string {
	if mode := os.Getenv("COVO_MOUSE_MODE"); mode != "" {
		return mode
	}
	return "sgr"
}

func DefaultKeyboardMode() string {
	if mode := os.Getenv("COVO_KITTY_KEYBOARD_MODE"); mode != "" {
		return mode
	}
	if runtime.GOOS == "darwin" {
		return "on"
	}
	return "auto"
}

func DefaultKeyboardFlags() int64 {
	// flag 1 = disambiguate escape codes. This alone suffices for modifier-
	// rich keys (Cmd+C, Cmd+A, …) since combos without a legacy encoding —
	// and the Super modifier has none — are reported as CSI u regardless.
	// Avoid flag 8 (report ALL keys as CSI u): it forces printable characters
	// (including IME-committed CJK text) into CSI u encoding, which breaks
	// macOS Chinese input methods that expect raw UTF-8 text delivery.
	return 1
}
