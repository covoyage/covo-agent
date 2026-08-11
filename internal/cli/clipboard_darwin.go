//go:build darwin

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// clipboardImagePaste detects and saves any image from the system clipboard.
// Strategy: pngpaste (fast, more formats) → osascript (always available).
// All images are converted to PNG on save.
func ClipboardImagePaste() (string, error) {
	if !HasClipboardImage() {
		return "", fmt.Errorf("no image found on clipboard")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".covo-agent", "images")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("paste-%d.png", time.Now().UnixNano()))

	if pngpasteSave(path) {
		return path, nil
	}
	if osascriptSave(path) {
		return path, nil
	}
	return "", fmt.Errorf("failed to save clipboard image")
}

// hasClipboardImage checks whether the clipboard currently contains any image.
// Uses `osascript -e "clipboard info"` which covers ALL image types.
func HasClipboardImage() bool {
	cmd := exec.Command("osascript", "-e", "clipboard info")
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	info := string(out)
	// All common image types: PNGf, TIFF, JPEG, GIFf, BMP, hvc1 (HEIC), etc.
	return strings.Contains(info, "«class PNGf»") ||
		strings.Contains(info, "«class TIFF»") ||
		strings.Contains(info, "«class JPEG»") ||
		strings.Contains(info, "«class GIFf»") ||
		strings.Contains(info, "«class BMP »") ||
		strings.Contains(info, "«class hvc1»") ||
		strings.Contains(info, "«class heic»")
}

// pngpaste saves the clipboard as PNG using pngpaste (brew install pngpaste).
func pngpasteSave(path string) bool {
	cmd := exec.Command("pngpaste", path)
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

// osascriptSave extracts the clipboard image using AppleScript.
func osascriptSave(path string) bool {
	script := fmt.Sprintf(`
try
    set imgData to the clipboard as «class PNGf»
    set f to open for access POSIX file "%s" with write permission
    write imgData to f
    close access f
on error
    return "fail"
end try
`, path)

	cmd := exec.Command("osascript", "-e", script)
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	if strings.Contains(string(out), "fail") {
		return false
	}
	info, statErr := os.Stat(path)
	return statErr == nil && info.Size() > 0
}
