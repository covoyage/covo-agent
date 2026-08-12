package repomap

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Symbol describes a top-level declaration in a Go source file.
type Symbol struct {
	Name string // exported name
	Kind string // "func", "type", "const", "var"
	Desc string // short signature, e.g. "func NewServer()", "type Server struct"
}

// FileEntry holds the symbols extracted from one source file.
type FileEntry struct {
	RelPath string
	Symbols []Symbol
}

// RepoMap builds and caches a compact structural map of Go source files.
type RepoMap struct {
	workDir  string
	cached   string
	modTimes map[string]int64 // rel path → file modification time (nano)
}

// New creates a RepoMap for the given working directory.
func New(workDir string) *RepoMap {
	return &RepoMap{
		workDir:  workDir,
		modTimes: make(map[string]int64),
	}
}

// Invalidate forces the next Build call to re-parse all files.
func (rm *RepoMap) Invalidate() {
	rm.cached = ""
	rm.modTimes = make(map[string]int64)
}

// Build scans the workspace for Go source files and returns a compact
// structural map. Results are cached and only regenerated when a file's
// modification time changes.
func (rm *RepoMap) Build() string {
	if rm.cached != "" && !rm.filesChanged() {
		return rm.cached
	}
	entries := rm.scan()
	rm.cached = formatMap(rm.workDir, entries)
	return rm.cached
}

// filesChanged returns true if any .go file's mod time differs from our cache.
func (rm *RepoMap) filesChanged() bool {
	changed := false
	filepath.Walk(rm.workDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || changed {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(rm.workDir, path)
		prev, ok := rm.modTimes[rel]
		if !ok || fi.ModTime().UnixNano() != prev {
			changed = true
			return filepath.SkipAll
		}
		return nil
	})
	return changed
}

// scan walks Go files and extracts symbols.
func (rm *RepoMap) scan() []FileEntry {
	var entries []FileEntry
	seen := make(map[string]bool)

	filepath.Walk(rm.workDir, func(path string, fi os.FileInfo, err error) error {
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
		rel, _ := filepath.Rel(rm.workDir, path)
		if seen[rel] {
			return nil
		}
		seen[rel] = true

		// Check mod time for caching
		if cachedTime, ok := rm.modTimes[rel]; ok && fi.ModTime().UnixNano() == cachedTime {
			return nil
		}

		syms, ok := parseFile(path)
		if ok {
			entries = append(entries, FileEntry{RelPath: rel, Symbols: syms})
			rm.modTimes[rel] = fi.ModTime().UnixNano()
		} else {
			// Remember we've seen it to avoid re-parsing broken files
			rm.modTimes[rel] = fi.ModTime().UnixNano()
		}
		return nil
	})

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].RelPath < entries[j].RelPath
	})
	return entries
}

// parseFile reads a Go source file and extracts top-level exported symbols.
// Returns symbols and a boolean indicating whether the file was valid Go.
func parseFile(path string) ([]Symbol, bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, false
	}

	var syms []Symbol
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, spec := range d.Specs {
					ts := spec.(*ast.TypeSpec)
					name := ts.Name.Name
					if !ast.IsExported(name) {
						continue
					}
					desc := describeType(ts)
					syms = append(syms, Symbol{Name: name, Kind: "type", Desc: desc})
				}
			case token.CONST:
				for _, spec := range d.Specs {
					vs := spec.(*ast.ValueSpec)
					for _, name := range vs.Names {
						if ast.IsExported(name.Name) {
							syms = append(syms, Symbol{Name: name.Name, Kind: "const", Desc: fmt.Sprintf("const %s", name.Name)})
						}
					}
				}
			case token.VAR:
				for _, spec := range d.Specs {
					vs := spec.(*ast.ValueSpec)
					for _, name := range vs.Names {
						if ast.IsExported(name.Name) {
							syms = append(syms, Symbol{Name: name.Name, Kind: "var", Desc: fmt.Sprintf("var %s", name.Name)})
						}
					}
				}
			}
		case *ast.FuncDecl:
			name := d.Name.Name
			if !ast.IsExported(name) {
				continue
			}
			desc := describeFunc(d)
			syms = append(syms, Symbol{Name: name, Kind: "func", Desc: desc})
		}
	}
	return syms, true
}

// describeType produces a short description for a type spec.
func describeType(ts *ast.TypeSpec) string {
	name := ts.Name.Name
	switch t := ts.Type.(type) {
	case *ast.StructType:
		return fmt.Sprintf("type %s struct", name)
	case *ast.InterfaceType:
		return fmt.Sprintf("type %s interface", name)
	case *ast.Ident:
		return fmt.Sprintf("type %s = %s", name, t.Name)
	case *ast.SelectorExpr:
		return fmt.Sprintf("type %s = %s.%s", name, t.X.(*ast.Ident).Name, t.Sel.Name)
	case *ast.ArrayType:
		return fmt.Sprintf("type %s []%s", name, typeExprStr(t.Elt))
	case *ast.MapType:
		return fmt.Sprintf("type %s map[%s]%s", name, typeExprStr(t.Key), typeExprStr(t.Value))
	case *ast.StarExpr:
		return fmt.Sprintf("type %s *%s", name, typeExprStr(t.X))
	default:
		return fmt.Sprintf("type %s %s", name, typeExprStr(ts.Type))
	}
}

// describeFunc produces a short signature for a function declaration.
func describeFunc(d *ast.FuncDecl) string {
	var b strings.Builder
	if d.Recv != nil && len(d.Recv.List) > 0 {
		recv := typeExprStr(d.Recv.List[0].Type)
		b.WriteString(fmt.Sprintf("func (%s) %s", recv, d.Name.Name))
	} else {
		b.WriteString(fmt.Sprintf("func %s", d.Name.Name))
	}
	// Parameters
	b.WriteString("(")
	params := d.Type.Params
	if params != nil {
		for i, p := range params.List {
			if i > 0 {
				b.WriteString(", ")
			}
			typ := typeExprStr(p.Type)
			if len(p.Names) == 0 {
				b.WriteString(typ)
			} else {
				for j, n := range p.Names {
					if j > 0 {
						b.WriteString(", ")
					}
					b.WriteString(n.Name)
				}
				b.WriteString(" ")
				b.WriteString(typ)
			}
		}
	}
	b.WriteString(")")
	// Results
	if d.Type.Results != nil && len(d.Type.Results.List) > 0 {
		b.WriteString(" ")
		if len(d.Type.Results.List) == 1 && d.Type.Results.List[0].Names == nil {
			b.WriteString(typeExprStr(d.Type.Results.List[0].Type))
		} else {
			b.WriteString("(")
			for i, r := range d.Type.Results.List {
				if i > 0 {
					b.WriteString(", ")
				}
				typ := typeExprStr(r.Type)
				if len(r.Names) == 0 {
					b.WriteString(typ)
				} else {
					for j, n := range r.Names {
						if j > 0 {
							b.WriteString(", ")
						}
						b.WriteString(n.Name)
						b.WriteString(" ")
					}
					b.WriteString(typ)
				}
			}
			b.WriteString(")")
		}
	}
	return b.String()
}

// typeExprStr converts an AST expression to a short string.
func typeExprStr(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeExprStr(t.X)
	case *ast.SelectorExpr:
		return typeExprStr(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + typeExprStr(t.Elt)
		}
		return fmt.Sprintf("[%s]%s", typeExprStr(t.Len), typeExprStr(t.Elt))
	case *ast.MapType:
		return fmt.Sprintf("map[%s]%s", typeExprStr(t.Key), typeExprStr(t.Value))
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.Ellipsis:
		return "..." + typeExprStr(t.Elt)
	case *ast.FuncType:
		return "func(...)"
	case *ast.BasicLit:
		return t.Value
	default:
		return fmt.Sprintf("%T", e)
	}
}

// formatMap renders the entries as a compact tree-like string, capped at 4000 bytes.
func formatMap(workDir string, entries []FileEntry) string {
	if len(entries) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<repo_map>\n")

	// Build a tree: dir path -> list of entries in that dir
	type dirNode struct {
		dir   string
		files []FileEntry
	}
	dirs := make(map[string]*dirNode)
	var dirKeys []string
	for _, e := range entries {
		dir := filepath.ToSlash(filepath.Dir(e.RelPath))
		if dir == "." {
			dir = ""
		}
		if _, ok := dirs[dir]; !ok {
			dirs[dir] = &dirNode{dir: dir}
			dirKeys = append(dirKeys, dir)
		}
		dirs[dir].files = append(dirs[dir].files, e)
	}
	sort.Strings(dirKeys)

	for _, dk := range dirKeys {
		nd := dirs[dk]
		prefix := ""
		if nd.dir != "" {
			prefix = "  "
		}

		// Check remaining capacity
		if b.Len() >= 3900 {
			b.WriteString("  … (truncated)\n")
			break
		}

		if nd.dir != "" {
			b.WriteString(nd.dir + "/\n")
		}

		for _, f := range nd.files {
			line := prefix + "  " + filepath.Base(f.RelPath) + "\n"
			if b.Len()+len(line) > 3900 {
				b.WriteString(prefix + "  … (truncated)\n")
				break
			}
			b.WriteString(line)
			for _, sym := range f.Symbols {
				pad := prefix + "    "
				symLine := pad + sym.Desc + "\n"
				if b.Len()+len(symLine) > 3900 {
					b.WriteString(pad + "…\n")
					break
				}
				b.WriteString(symLine)
			}
		}
	}
	b.WriteString("</repo_map>")
	return b.String()
}

// String returns the cached map or an empty string.
func (rm *RepoMap) String() string {
	if rm.cached == "" {
		return ""
	}
	return rm.cached
}
