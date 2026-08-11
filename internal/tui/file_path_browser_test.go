package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilePathBrowserListsFilesAndDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "alpha.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	browser := NewFilePathBrowser("@file", root, func() bool { return false })
	got := browser.Complete("a")
	if len(got) != 2 {
		t.Fatalf("Complete(a) returned %d suggestions: %+v", len(got), got)
	}
	if got[0].InsertText != "assets/" || got[1].InsertText != "alpha.txt" {
		t.Fatalf("Complete(a) = %+v", got)
	}
}

func TestFilePathBrowserFolderModeExcludesFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "alpha.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}

	browser := NewFilePathBrowser("@folder", root, nil)
	got := browser.Complete("a")
	if len(got) != 1 || got[0].InsertText != "assets/" {
		t.Fatalf("folder suggestions = %+v", got)
	}
}

func TestFilePathBrowserMissingPathReturnsCreateHint(t *testing.T) {
	browser := NewFilePathBrowser("@file", t.TempDir(), nil)
	got := browser.Complete("missing/path")
	if len(got) != 1 || got[0].Description != "create/open" {
		t.Fatalf("missing path suggestions = %+v", got)
	}
}
