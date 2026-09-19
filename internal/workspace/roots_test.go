package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddExtraRoot(t *testing.T) {
	work := t.TempDir()
	extra := t.TempDir()
	got, abs, err := AddExtraRoot(work, nil, extra)
	if err != nil {
		t.Fatal(err)
	}
	if abs != extra && abs != mustAbs(extra) {
		info, _ := filepath.EvalSymlinks(extra)
		if abs != info {
			t.Fatalf("abs = %s", abs)
		}
	}
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	if _, _, err := AddExtraRoot(work, got, extra); err == nil {
		t.Fatal("expected duplicate error")
	}
	if _, _, err := AddExtraRoot(work, nil, work); err == nil {
		t.Fatal("expected working dir error")
	}
}

func TestResolveExtraRootRejectsFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveExtraRoot(file, dir); err == nil {
		t.Fatal("expected not a directory")
	}
}

func TestContains(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "sub")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if !Contains(nested, root) {
		t.Fatal("nested should be inside root")
	}
	if Contains(t.TempDir(), root) {
		t.Fatal("other dir should not match")
	}
}

func TestWriteAgentsFile(t *testing.T) {
	dir := t.TempDir()
	path, created, err := WriteAgentsFile(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected create")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# Project guidance") {
		t.Fatalf("content = %q", data)
	}
	if _, created, err = WriteAgentsFile(dir, false); err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("second write should not overwrite")
	}
	if _, created, err = WriteAgentsFile(dir, true); err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("force should overwrite")
	}
}

func mustAbs(p string) string {
	abs, _ := filepath.Abs(p)
	return abs
}
