package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/covoyage/covonaut/tui/component"

	"github.com/covoyage/covo-agent/internal/i18n"
)

// openExternalEditor opens the current editor content in $EDITOR (or vi/nvim
// as fallback). After the editor exits, the modified content is read back and
// set on the editor.
func openExternalEditor(editor *component.Editor) {
	if editor == nil {
		return
	}

	f, err := os.CreateTemp("", "covo-edit-*.md")
	if err != nil {
		loadUIBus().PrintError(fmt.Errorf("create temp file: %w", err))
		return
	}
	tmpPath := f.Name()

	current := editor.GetValue()
	if _, err := f.WriteString(current); err != nil {
		f.Close()
		os.Remove(tmpPath)
		loadUIBus().PrintError(fmt.Errorf("write temp file: %w", err))
		return
	}
	f.Close()
	defer os.Remove(tmpPath)

	editorCmd := os.Getenv("EDITOR")
	if editorCmd == "" {
		editorCmd = os.Getenv("VISUAL")
	}
	if editorCmd == "" {
		for _, candidate := range []string{"nvim", "vim", "vi", "nano"} {
			if _, err := exec.LookPath(candidate); err == nil {
				editorCmd = candidate
				break
			}
		}
	}
	if editorCmd == "" {
		loadUIBus().PrintSystem(i18n.T("external_editor.not_found"))
		return
	}

	loadUIBus().PrintSystem(i18n.T("external_editor.opening", "editor", editorCmd))

	cmd := exec.Command(editorCmd, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		loadUIBus().PrintError(fmt.Errorf("editor %s: %w", editorCmd, err))
		return
	}

	content, err := os.ReadFile(tmpPath)
	if err != nil {
		loadUIBus().PrintError(fmt.Errorf("read temp file: %w", err))
		return
	}

	newText := string(content)
	if newText != current {
		editor.SetValue(newText)
		loadUIBus().PrintSystem(i18n.T("external_editor.updated"))
	} else {
		loadUIBus().PrintSystem(i18n.T("external_editor.unchanged"))
	}
}
