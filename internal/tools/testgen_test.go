package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToTitle(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"hello", "Hello"},
		{"hello_world", "HelloWorld"},
		{"my_test_file", "MyTestFile"},
		{"", ""},
		{"a", "A"},
	}
	for _, tt := range tests {
		got := toTitle(tt.in)
		if got != tt.want {
			t.Errorf("toTitle(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDetectGoPackage(t *testing.T) {
	dir := t.TempDir()

	// No .go files — fallback to dir name
	if pkg := detectGoPackage(dir); pkg != filepath.Base(dir) {
		t.Errorf("expected %q, got %q", filepath.Base(dir), pkg)
	}

	// With a .go file
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644)
	if pkg := detectGoPackage(dir); pkg != "main" {
		t.Errorf("expected 'main', got %q", pkg)
	}

	// Package declaration with spaces — uses its own dir to avoid order issues
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "util.go"), []byte("package  util\n"), 0644)
	if pkg := detectGoPackage(dir2); pkg != "util" {
		t.Errorf("expected 'util', got %q", pkg)
	}
}

func TestGenerateTestsForFileGoSkeleton(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "calculator.go")
	os.WriteFile(src, []byte("package calculator\n"), 0644)

	result, err := GenerateTestsForFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "suggested" && result.Status != "generated" {
		t.Fatalf("expected suggested or generated, got %q", result.Status)
	}
	if result.Language != "go" {
		t.Errorf("expected 'go', got %q", result.Language)
	}

	testFile := filepath.Join(dir, "calculator_test.go")
	if result.TestFile != testFile {
		t.Errorf("expected test file %q, got %q", testFile, result.TestFile)
	}
	if result.Status == "suggested" {
		if _, err := os.Stat(testFile); !os.IsNotExist(err) {
			t.Fatal("suggested path should not write a skeleton file")
		}
		if !strings.Contains(result.Error, testFile) {
			t.Errorf("expected path in error, got %q", result.Error)
		}
	}
}

func TestGenerateTestsForFileExists(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "math.go")
	testFile := filepath.Join(dir, "math_test.go")
	os.WriteFile(src, []byte("package math\n"), 0644)
	os.WriteFile(testFile, []byte("package math\n"), 0644)

	result, err := GenerateTestsForFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "exists" {
		t.Errorf("expected 'exists', got %q", result.Status)
	}
}

func TestGenerateTestsForFilePython(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "helper.py")
	os.WriteFile(src, []byte("# python file\n"), 0644)

	result, err := GenerateTestsForFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "suggested" {
		t.Fatalf("expected 'suggested', got %q", result.Status)
	}
	if result.Language != "python" {
		t.Errorf("expected 'python', got %q", result.Language)
	}

	testFile := filepath.Join(dir, "test_helper.py")
	if result.TestFile != testFile {
		t.Errorf("expected %q, got %q", testFile, result.TestFile)
	}
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Fatal("python path should not write a skeleton file")
	}
}

func TestGenerateTestsForFileJavaScript(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "util.ts")
	os.WriteFile(src, []byte("export const x = 1;\n"), 0644)

	result, err := GenerateTestsForFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "suggested" {
		t.Fatalf("expected 'suggested', got %q", result.Status)
	}
	if result.Language != "javascript" {
		t.Errorf("expected 'javascript', got %q", result.Language)
	}

	testFile := filepath.Join(dir, "util.test.ts")
	if result.TestFile != testFile {
		t.Errorf("expected %q, got %q", testFile, result.TestFile)
	}
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Fatal("javascript path should not write a skeleton file")
	}
}

func TestGenerateTestsForFileUnsupported(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "data.rs")
	os.WriteFile(src, []byte("fn main() {}\n"), 0644)

	result, err := GenerateTestsForFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "unsupported" {
		t.Errorf("expected 'unsupported', got %q", result.Status)
	}
	if result.Language != ".rs" {
		t.Errorf("expected '.rs', got %q", result.Language)
	}
}

func TestGenerateTestsForFileDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := GenerateTestsForFile(dir)
	if err == nil {
		t.Fatal("expected error for directory input")
	}
}

func TestGenerateTestsForFileNonexistent(t *testing.T) {
	_, err := GenerateTestsForFile("/nonexistent/path/file.go")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}
