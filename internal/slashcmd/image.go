package slashcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".bmp": true, ".ico": true, ".tiff": true, ".tif": true,
}

func parseImageArgs(input string) (path, message string, err error) {
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), "/image"))
	if rest == "" {
		return "", "", fmt.Errorf("Usage: /image <path> [message]")
	}
	if rest[0] == '"' || rest[0] == '\'' {
		q := rest[0]
		end := strings.IndexByte(rest[1:], q)
		if end < 0 {
			return "", "", fmt.Errorf("unclosed quote in image path")
		}
		path = rest[1 : 1+end]
		message = strings.TrimSpace(rest[2+end:])
		if strings.TrimSpace(path) == "" {
			return "", "", fmt.Errorf("Usage: /image <path> [message]")
		}
		return path, message, nil
	}
	path, msg, _ := strings.Cut(rest, " ")
	return path, strings.TrimSpace(msg), nil
}

func resolveImagePath(path, workingDir string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("image path is required")
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home directory: %w", err)
		}
		if path == "~" {
			path = home
		} else if strings.HasPrefix(path, "~/") {
			path = filepath.Join(home, path[2:])
		}
	}
	if !filepath.IsAbs(path) {
		base := workingDir
		if base == "" {
			base, _ = os.Getwd()
		}
		path = filepath.Join(base, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("image not found: %s", path)
	}
	if info.IsDir() {
		return "", fmt.Errorf("not an image file: %s", abs)
	}
	ext := strings.ToLower(filepath.Ext(abs))
	if !imageExts[ext] {
		return "", fmt.Errorf("unsupported image type %s", ext)
	}
	return abs, nil
}

func handleImage(sctx *SlashContext, parts []string) bool {
	path, message, err := parseImageArgs(sctx.Input)
	if err != nil {
		sctx.UI.App.PrintSystem(err.Error())
		return true
	}
	abs, err := resolveImagePath(path, sctx.Runtime.WorkingDir)
	if err != nil {
		sctx.UI.App.PrintSystem(err.Error())
		return true
	}
	if sctx.UI.AttachImage == nil {
		sctx.UI.App.PrintSystem("Image attachments are only available in the interactive TUI.")
		return true
	}
	if err := sctx.UI.AttachImage(abs, message); err != nil {
		sctx.UI.App.PrintSystem(err.Error())
	}
	return true
}
