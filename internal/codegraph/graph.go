// Package codegraph builds a dependency graph of Go packages in a workspace.
// It parses import statements from all .go files and constructs a directed graph
// showing which packages depend on which, detects cycles, and can output the
// graph in various formats (Mermaid, DOT, text).
package codegraph

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Graph represents a package dependency graph.
type Graph struct {
	// Packages maps package import path to its node.
	Packages map[string]*PackageNode
	// Edges: from -> [to1, to2, ...]
	// An edge from A to B means A imports B.
	Edges map[string][]string
}

// PackageNode represents a package in the dependency graph.
type PackageNode struct {
	ImportPath string   // full import path, e.g. "github.com/foo/bar/internal/api"
	Name       string   // short name, e.g. "api"
	Dir        string   // filesystem directory
	Files      []string // list of .go files in this package
	Imports    []string // list of imported package paths
}

// Build constructs a dependency graph from the Go source files in the given
// workspace directory. It follows the same directory-skipping rules as repomap
// (vendor, node_modules, .git, etc.).
func Build(workDir string) (*Graph, error) {
	g := &Graph{
		Packages: make(map[string]*PackageNode),
		Edges:    make(map[string][]string),
	}

	modulePath := detectModulePath(workDir)

	err := filepath.Walk(workDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() {
			name := fi.Name()
			if name == "vendor" || name == "node_modules" || name == ".git" ||
				name == ".next" || name == "dist" || name == "build" || name == "target" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		pkg, err := parseImports(path)
		if err != nil {
			return nil // skip unparseable files
		}

		relDir := filepath.Dir(path)
		relDir, _ = filepath.Rel(workDir, relDir)
		if relDir == "." {
			relDir = ""
		}

		importPath := joinModulePath(modulePath, relDir)
		node, exists := g.Packages[importPath]
		if !exists {
			node = &PackageNode{
				ImportPath: importPath,
				Name:       filepath.Base(relDir),
				Dir:        filepath.Dir(path),
				Imports:    []string{},
			}
			g.Packages[importPath] = node
		}
		node.Files = append(node.Files, filepath.Base(path))

		// Add imports (deduplicated)
		for _, imp := range pkg {
			if !contains(node.Imports, imp) {
				node.Imports = append(node.Imports, imp)
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("codegraph: walk: %w", err)
	}

	// Build edges: only for internal dependencies (packages in this workspace)
	internalPkgs := make(map[string]bool)
	for path := range g.Packages {
		internalPkgs[path] = true
	}

	for path, node := range g.Packages {
		for _, imp := range node.Imports {
			if internalPkgs[imp] {
				g.Edges[path] = append(g.Edges[path], imp)
			}
		}
		// Deduplicate edges
		g.Edges[path] = dedup(g.Edges[path])
	}

	return g, nil
}

// DetectCycles finds circular dependencies in the graph using DFS.
// Returns a list of cycles, each cycle being a list of package paths.
func (g *Graph) DetectCycles() [][]string {
	var cycles [][]string
	visited := make(map[string]int) // 0=unvisited, 1=in-progress, 2=done
	var path []string

	var dfs func(node string)
	dfs = func(node string) {
		if visited[node] == 1 {
			// Found a cycle — extract it from the path
			cycleStart := -1
			for i, n := range path {
				if n == node {
					cycleStart = i
					break
				}
			}
			if cycleStart >= 0 {
				cycle := make([]string, len(path)-cycleStart)
				copy(cycle, path[cycleStart:])
				cycle = append(cycle, node) // close the cycle
				cycles = append(cycles, cycle)
			}
			return
		}
		if visited[node] == 2 {
			return
		}

		visited[node] = 1
		path = append(path, node)

		for _, neighbor := range g.Edges[node] {
			dfs(neighbor)
		}

		path = path[:len(path)-1]
		visited[node] = 2
	}

	// Sort package paths for deterministic output
	var pkgPaths []string
	for p := range g.Packages {
		pkgPaths = append(pkgPaths, p)
	}
	sort.Strings(pkgPaths)

	for _, p := range pkgPaths {
		dfs(p)
	}

	return cycles
}

// ToMermaid returns the graph as a Mermaid flowchart string.
func (g *Graph) ToMermaid() string {
	var b strings.Builder
	b.WriteString("graph TD\n")

	// Sort packages for deterministic output
	var pkgPaths []string
	for p := range g.Packages {
		pkgPaths = append(pkgPaths, p)
	}
	sort.Strings(pkgPaths)

	// Node definitions
	shortNames := g.shortNames()
	for _, p := range pkgPaths {
		node := g.Packages[p]
		short := shortNames[p]
		b.WriteString(fmt.Sprintf("  %s[%s]\n", short, node.Name))
	}

	// Edges
	for _, from := range pkgPaths {
		shortFrom := shortNames[from]
		for _, to := range g.Edges[from] {
			shortTo := shortNames[to]
			b.WriteString(fmt.Sprintf("  %s --> %s\n", shortFrom, shortTo))
		}
	}

	// Mark cycles with different style
	cycles := g.DetectCycles()
	for i, cycle := range cycles {
		if len(cycle) < 2 {
			continue
		}
		b.WriteString(fmt.Sprintf("  linkStyle %d stroke:#f00\n", i))
	}

	return b.String()
}

// ToDOT returns the graph in Graphviz DOT format.
func (g *Graph) ToDOT() string {
	var b strings.Builder
	b.WriteString("digraph G {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=rounded];\n")

	shortNames := g.shortNames()
	var pkgPaths []string
	for p := range g.Packages {
		pkgPaths = append(pkgPaths, p)
	}
	sort.Strings(pkgPaths)

	for _, p := range pkgPaths {
		node := g.Packages[p]
		short := shortNames[p]
		b.WriteString(fmt.Sprintf("  %s [label=\"%s\"];\n", short, node.Name))
	}

	for _, from := range pkgPaths {
		shortFrom := shortNames[from]
		for _, to := range g.Edges[from] {
			shortTo := shortNames[to]
			b.WriteString(fmt.Sprintf("  %s -> %s;\n", shortFrom, shortTo))
		}
	}

	b.WriteString("}\n")
	return b.String()
}

// ToText returns a human-readable text summary of the graph.
func (g *Graph) ToText() string {
	var b strings.Builder
	b.WriteString("── Codebase Dependency Graph ──\n\n")

	// Sort packages
	var pkgPaths []string
	for p := range g.Packages {
		pkgPaths = append(pkgPaths, p)
	}
	sort.Strings(pkgPaths)

	b.WriteString(fmt.Sprintf("Total packages: %d\n", len(g.Packages)))
	totalEdges := 0
	for _, edges := range g.Edges {
		totalEdges += len(edges)
	}
	b.WriteString(fmt.Sprintf("Total dependencies: %d\n\n", totalEdges))

	// Cycles
	cycles := g.DetectCycles()
	if len(cycles) > 0 {
		b.WriteString(fmt.Sprintf("⚠ Circular dependencies: %d\n", len(cycles)))
		for i, cycle := range cycles {
			b.WriteString(fmt.Sprintf("  Cycle %d: %s\n", i+1, strings.Join(cycle, " → ")))
		}
		b.WriteString("\n")
	}

	// Package list with dependencies
	b.WriteString("── Packages ──\n")
	for _, p := range pkgPaths {
		node := g.Packages[p]
		b.WriteString(fmt.Sprintf("\n%s (%d files)\n", node.Name, len(node.Files)))
		if len(g.Edges[p]) > 0 {
			b.WriteString("  depends on:\n")
			for _, dep := range g.Edges[p] {
				depNode := g.Packages[dep]
				if depNode != nil {
					b.WriteString(fmt.Sprintf("    → %s\n", depNode.Name))
				}
			}
		}
	}

	// Most depended-upon packages
	depCount := make(map[string]int)
	for _, edges := range g.Edges {
		for _, to := range edges {
			depCount[to]++
		}
	}
	type kv struct {
		key string
		val int
	}
	var sorted []kv
	for k, v := range depCount {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].val > sorted[j].val })
	if len(sorted) > 0 {
		b.WriteString("\n── Most Depended Upon ──\n")
		limit := 10
		if len(sorted) < limit {
			limit = len(sorted)
		}
		for i := 0; i < limit; i++ {
			node := g.Packages[sorted[i].key]
			if node != nil {
				b.WriteString(fmt.Sprintf("  %s: %d dependents\n", node.Name, sorted[i].val))
			}
		}
	}

	return b.String()
}

// shortNames generates short unique identifiers for each package for use in
// graph output (e.g. "P0", "P1", ...).
func (g *Graph) shortNames() map[string]string {
	names := make(map[string]string)
	var pkgPaths []string
	for p := range g.Packages {
		pkgPaths = append(pkgPaths, p)
	}
	sort.Strings(pkgPaths)
	for i, p := range pkgPaths {
		names[p] = fmt.Sprintf("P%d", i)
	}
	return names
}

// --- Helper functions ---

// parseImports reads a Go file and returns its import paths.
func parseImports(path string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}

	var imports []string
	for _, imp := range f.Imports {
		// imp.Path.Value is a quoted string like `"fmt"`
		p := strings.Trim(imp.Path.Value, `"`)
		imports = append(imports, p)
	}
	return imports, nil
}

// detectModulePath reads go.mod to find the module path.
func detectModulePath(workDir string) string {
	modPath := filepath.Join(workDir, "go.mod")
	data, err := os.ReadFile(modPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

// joinModulePath joins a module path with a relative directory path.
func joinModulePath(module, relDir string) string {
	if module == "" {
		return relDir
	}
	if relDir == "" {
		return module
	}
	return module + "/" + filepath.ToSlash(relDir)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func dedup(s []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			result = append(result, x)
		}
	}
	return result
}
