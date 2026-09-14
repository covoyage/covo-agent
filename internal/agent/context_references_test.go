package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExpandFolderReferenceAbsolutePath 回归：@folder: 后跟绝对路径时
// 不得把 cwd 拼在前面（filepath.Join 只拼接不重置）。
func TestExpandFolderReferenceAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	ref := ContextReference{Raw: "@folder:" + dir, Kind: "folder", Target: dir}
	warning, _ := expandFolderReference(ref, "/some/other/cwd")
	if warning != "" {
		t.Fatalf("absolute folder ref should resolve, got warning: %q", warning)
	}
}

// TestExpandFileReferenceAbsolutePath 同上，@file 的绝对路径版本。
func TestExpandFileReferenceAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	ref := ContextReference{Raw: "@file:" + file, Kind: "file", Target: file}
	warning, block := expandFileReference(ref, "/some/other/cwd")
	if warning != "" {
		t.Fatalf("absolute file ref should resolve, got warning: %q", warning)
	}
	if !strings.Contains(block, "hello") {
		t.Fatalf("block should contain file content, got: %q", block)
	}
}

// TestExpandFolderReferenceRelativePath 相对路径仍基于 cwd 解析。
func TestExpandFolderReferenceRelativePath(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	ref := ContextReference{Raw: "@folder:sub", Kind: "folder", Target: "sub"}
	if warning, _ := expandFolderReference(ref, cwd); warning != "" {
		t.Fatalf("relative folder ref should resolve against cwd, got: %q", warning)
	}
	if warning, _ := expandFolderReference(ref, t.TempDir()); !strings.Contains(warning, "folder not found") {
		t.Fatalf("relative ref against unrelated cwd should not resolve, got: %q", warning)
	}
}
