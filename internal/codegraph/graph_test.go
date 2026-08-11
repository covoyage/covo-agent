package codegraph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0755)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestBuild_SimpleGraph(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/myapp\n\ngo 1.25\n")
	writeFile(t, dir, "main.go", `package main

import (
	"example.com/myapp/internal/api"
	"example.com/myapp/internal/db"
	"fmt"
)

func main() {
	fmt.Println("hello")
}`)
	writeFile(t, dir, "internal/api/server.go", `package api

import (
	"example.com/myapp/internal/db"
)

func Serve() {}`)
	writeFile(t, dir, "internal/db/db.go", `package db

func Open() {}`)

	g, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if len(g.Packages) < 3 {
		t.Fatalf("expected at least 3 packages, got %d", len(g.Packages))
	}

	// Check that main depends on api and db
	mainPath := "example.com/myapp"
	apiPath := "example.com/myapp/internal/api"
	dbPath := "example.com/myapp/internal/db"

	if !contains(g.Edges[mainPath], apiPath) {
		t.Error("expected main to depend on api")
	}
	if !contains(g.Edges[mainPath], dbPath) {
		t.Error("expected main to depend on db")
	}
	if !contains(g.Edges[apiPath], dbPath) {
		t.Error("expected api to depend on db")
	}
}

func TestDetectCycles_NoCycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module test\n\ngo 1.25\n")
	writeFile(t, dir, "a/a.go", `package a
import "test/b"`)
	writeFile(t, dir, "b/b.go", `package b`)

	g, _ := Build(dir)
	cycles := g.DetectCycles()
	if len(cycles) != 0 {
		t.Errorf("expected 0 cycles, got %d: %v", len(cycles), cycles)
	}
}

func TestDetectCycles_WithCycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module test\n\ngo 1.25\n")
	writeFile(t, dir, "a/a.go", `package a
import "test/b"`)
	writeFile(t, dir, "b/b.go", `package b
import "test/a"`)

	g, _ := Build(dir)
	cycles := g.DetectCycles()
	if len(cycles) == 0 {
		t.Error("expected at least 1 cycle")
	}
}

func TestToMermaid(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module test\n\ngo 1.25\n")
	writeFile(t, dir, "a/a.go", `package a
import "test/b"`)
	writeFile(t, dir, "b/b.go", `package b`)

	g, _ := Build(dir)
	output := g.ToMermaid()
	if !strings.Contains(output, "graph TD") {
		t.Error("expected 'graph TD' in output")
	}
	if !strings.Contains(output, "-->") {
		t.Error("expected '-->' in output")
	}
}

func TestToDOT(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module test\n\ngo 1.25\n")
	writeFile(t, dir, "a/a.go", `package a
import "test/b"`)
	writeFile(t, dir, "b/b.go", `package b`)

	g, _ := Build(dir)
	output := g.ToDOT()
	if !strings.Contains(output, "digraph") {
		t.Error("expected 'digraph' in output")
	}
	if !strings.Contains(output, "->") {
		t.Error("expected '->' in output")
	}
}

func TestToText(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module test\n\ngo 1.25\n")
	writeFile(t, dir, "a/a.go", `package a
import "test/b"`)
	writeFile(t, dir, "b/b.go", `package b`)

	g, _ := Build(dir)
	output := g.ToText()
	if !strings.Contains(output, "Codebase Dependency Graph") {
		t.Error("expected title in output")
	}
	if !strings.Contains(output, "Total packages:") {
		t.Error("expected package count in output")
	}
}

func TestBuild_SkipsVendor(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module test\n\ngo 1.25\n")
	writeFile(t, dir, "main.go", `package main`)
	writeFile(t, dir, "vendor/foo/bar.go", `package foo
import "test/b"`)

	g, _ := Build(dir)
	for path := range g.Packages {
		if strings.Contains(path, "vendor") {
			t.Errorf("vendor package should be skipped: %s", path)
		}
	}
}

func TestBuild_NoGoMod(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", `package main
import "fmt"`)

	g, err := Build(dir)
	if err != nil {
		t.Fatalf("Build without go.mod: %v", err)
	}
	// Should still work, just with empty module path
	if len(g.Packages) == 0 {
		t.Error("expected at least 1 package")
	}
}

func TestParseImports(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte(`package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {}`), 0644)

	imports, err := parseImports(path)
	if err != nil {
		t.Fatalf("parseImports: %v", err)
	}
	if len(imports) != 3 {
		t.Fatalf("expected 3 imports, got %d", len(imports))
	}
	if !contains(imports, "fmt") {
		t.Error("expected 'fmt' in imports")
	}
}
