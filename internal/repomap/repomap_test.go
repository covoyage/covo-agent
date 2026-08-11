package repomap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0755)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "foo.go", `package foo

const MaxRetries = 3

var DefaultTimeout = 30

// Exported function
func Run(cfg string) (int, error) {
	return 0, nil
}

// Unexported should be skipped
func helper() {}

type Config struct {
	Name string
}

type Store interface {
	Get(id string) (string, error)
}
`)

	syms, _ := parseFile(filepath.Join(dir, "foo.go"))
	if len(syms) == 0 {
		t.Fatal("expected symbols, got none")
	}

	got := make(map[string]string)
	for _, s := range syms {
		got[s.Name] = s.Kind
	}

	expected := map[string]string{
		"MaxRetries":    "const",
		"DefaultTimeout": "var",
		"Run":           "func",
		"Config":        "type",
		"Store":         "type",
	}
	for name, kind := range expected {
		if g, ok := got[name]; !ok {
			t.Errorf("missing symbol %q (%s)", name, kind)
		} else if g != kind {
			t.Errorf("symbol %q: expected kind %q, got %q", name, kind, g)
		}
	}

	// Unexported should not appear
	if _, ok := got["helper"]; ok {
		t.Error("unexported symbol 'helper' should not appear in repo map")
	}
}

func TestParseStruct(t *testing.T) {
	syms := parseFileFromStr(t, `package main

type Person struct {
	Name string
	Age  int
}
`)
	if len(syms) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(syms))
	}
	if syms[0].Desc != "type Person struct" {
		t.Errorf("expected 'type Person struct', got %q", syms[0].Desc)
	}
}

func TestParseInterface(t *testing.T) {
	syms := parseFileFromStr(t, `package main

type Reader interface {
	Read(p []byte) (n int, err error)
}
`)
	if len(syms) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(syms))
	}
	if syms[0].Desc != "type Reader interface" {
		t.Errorf("expected 'type Reader interface', got %q", syms[0].Desc)
	}
}

func TestParseFuncSignature(t *testing.T) {
	syms := parseFileFromStr(t, `package main

func NewHandler(db *sql.DB, cfg Config) (*Handler, error) {
	return nil, nil
}
`)
	if len(syms) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(syms))
	}
	if !strings.Contains(syms[0].Desc, "func NewHandler") {
		t.Errorf("desc should contain 'func NewHandler', got %q", syms[0].Desc)
	}
	if !strings.Contains(syms[0].Desc, "*sql.DB") {
		t.Errorf("desc should contain '*sql.DB', got %q", syms[0].Desc)
	}
	if !strings.Contains(syms[0].Desc, "*Handler") {
		t.Errorf("desc should contain '*Handler', got %q", syms[0].Desc)
	}
}

func TestParseMethod(t *testing.T) {
	syms := parseFileFromStr(t, `package main

type Server struct{}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	return nil
}
`)
	if len(syms) != 2 {
		t.Fatalf("expected 2 symbols (type + method), got %d", len(syms))
	}
	found := false
	for _, s := range syms {
		if s.Kind == "func" && strings.Contains(s.Desc, "(*Server)") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected method with receiver (*Server), got %v", syms)
	}
}

func TestRepoMapBuild(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", `package main

func main() {}
`)
	writeFile(t, dir, "util.go", `package util

func Help() string { return "" }
`)

	rm := New(dir)
	result := rm.Build()
	if result == "" {
		t.Fatal("expected non-empty repo map")
	}
	if !strings.Contains(result, "<repo_map>") {
		t.Error("expected <repo_map> tag")
	}
	if !strings.Contains(result, "main.go") {
		t.Error("expected main.go in map")
	}
	if !strings.Contains(result, "util.go") {
		t.Error("expected util.go in map")
	}
	if !strings.Contains(result, "</repo_map>") {
		t.Error("expected </repo_map> tag")
	}
}

func TestRepoMapSkipUnexported(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "helper.go", `package helper

func helper() {}
func PublicFunc() {}
type private struct{}
type Public struct{}
`)

	rm := New(dir)
	result := rm.Build()
	if !strings.Contains(result, "helper.go") {
		t.Error("should contain helper.go (valid Go file)")
	}
	if strings.Contains(result, "func helper") {
		t.Error("should not contain unexported function 'helper'")
	}
	if !strings.Contains(result, "PublicFunc") {
		t.Error("should contain exported function 'PublicFunc'")
	}
	if !strings.Contains(result, "Public") {
		t.Error("should contain exported type 'Public'")
	}
}

func TestRepoMapSubdirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", `package main
func main() {}
`)
	writeFile(t, dir, "api/server.go", `package api
func Serve() {}
`)
	writeFile(t, dir, "internal/db/db.go", `package db
func Open() {}
`)

	rm := New(dir)
	result := rm.Build()
	if !strings.Contains(result, "api/") {
		t.Error("expected api/ directory in map")
	}
	if !strings.Contains(result, "internal/db/") {
		t.Error("expected internal/db/ directory in map")
	}
}

func TestRepoMapSkipVendor(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "vendor/github.com/foo/bar.go", `package foo
func Foo() {}
`)

	rm := New(dir)
	result := rm.Build()
	if result != "" {
		t.Errorf("expected empty map (vendor skipped), got %q", result)
	}
}

func TestRepoMapCaching(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", `package a
func A() {}
`)

	rm := New(dir)
	result1 := rm.Build()

	// Same content, should use cache
	result2 := rm.Build()
	if result1 != result2 {
		t.Error("expected cached result to be identical")
	}

	// Modify a file — use Invalidate to force rebuild regardless of mod time
	// (filesystem mod time granularity varies across platforms)
	writeFile(t, dir, "a.go", `package a
func A() {}
func B() {}
`)
	rm.Invalidate()
	result3 := rm.Build()
	if result1 == result3 {
		t.Error("expected different result after file change + invalidation")
	}
}

func TestRepoMapInvalidate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", `package a
func A() {}
`)

	rm := New(dir)
	result1 := rm.Build()
	rm.Invalidate()
	// Should rebuild
	result2 := rm.Build()
	if result1 == "" || result2 == "" {
		t.Fatal("expected non-empty results")
	}
}

func TestEmptyDir(t *testing.T) {
	dir := t.TempDir()
	rm := New(dir)
	result := rm.Build()
	if result != "" {
		t.Errorf("expected empty map for empty dir, got %q", result)
	}
}

func TestNonGoFilesIgnored(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "readme.md", `# Readme`)
	writeFile(t, dir, "config.json", `{}`)

	rm := New(dir)
	result := rm.Build()
	if result != "" {
		t.Errorf("expected empty map for non-Go files, got %q", result)
	}
}

func TestParseFile_TypeAlias(t *testing.T) {
	syms := parseFileFromStr(t, `package pkg
type HandlerFunc = func()
type Name = string
`)
	// type aliases may or may not be exported; just ensure no crash
	if len(syms) == 0 {
		t.Fatal("expected at least one symbol")
	}
}

// parseFileFromStr parses Go source from a string for testing.
func parseFileFromStr(t *testing.T, src string) []Symbol {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "test.go")
	if err := os.WriteFile(p, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	syms, _ := parseFile(p)
	return syms
}
