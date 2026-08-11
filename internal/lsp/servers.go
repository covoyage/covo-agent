package lsp

import (
	"os"
	"path/filepath"
	"strings"
)

type ServerDef struct {
	ID           string
	Extensions   []string
	Basenames    []string
	LanguageID   string
	Command      string
	Args         []string
	InitOptions  map[string]any
	RootPatterns []string
}

func (s ServerDef) MatchesFile(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	for _, e := range s.Extensions {
		if ext == "."+e {
			return true
		}
	}
	base := filepath.Base(filePath)
	for _, b := range s.Basenames {
		if base == b {
			return true
		}
	}
	return false
}

var languageByExt = map[string]string{
	".py":      "python",
	".pyi":     "python",
	".ts":      "typescript",
	".tsx":     "typescriptreact",
	".js":      "javascript",
	".jsx":     "javascriptreact",
	".mjs":     "javascript",
	".cjs":     "javascript",
	".mts":     "typescript",
	".cts":     "typescript",
	".vue":     "vue",
	".svelte":  "svelte",
	".astro":   "astro",
	".go":      "go",
	".rs":      "rust",
	".rb":      "ruby",
	".rake":    "ruby",
	".gemspec": "ruby",
	".c":       "c",
	".h":       "c",
	".cc":      "cpp",
	".cpp":     "cpp",
	".cxx":     "cpp",
	".hh":      "cpp",
	".hpp":     "cpp",
	".hxx":     "cpp",
	".cs":      "csharp",
	".csx":     "csharp",
	".fs":      "fsharp",
	".fsi":     "fsharp",
	".fsx":     "fsharp",
	".swift":   "swift",
	".java":    "java",
	".kt":      "kotlin",
	".kts":     "kotlin",
	".yaml":    "yaml",
	".yml":     "yaml",
	".json":    "json",
	".jsonc":   "jsonc",
	".lua":     "lua",
	".php":     "php",
	".prisma":  "prisma",
	".dart":    "dart",
	".ml":      "ocaml",
	".mli":     "ocaml",
	".scala":   "scala",
	".sc":      "scala",
	".zig":     "zig",
	".nix":     "nix",
	".sql":     "sql",
	".proto":   "proto",
	".graphql": "graphql",
	".gql":     "graphql",
	".md":      "markdown",
	".mdx":     "markdown",
	".toml":    "toml",
	".env":     "dotenv",
}

func LanguageIDFor(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if id, ok := languageByExt[ext]; ok {
		return id
	}
	return "plaintext"
}

var allServers = []ServerDef{
	{
		ID:           "gopls",
		Extensions:   []string{"go"},
		LanguageID:   "go",
		Command:      "gopls",
		Args:         []string{"serve"},
		RootPatterns: []string{"go.mod", "go.work"},
		InitOptions: map[string]any{
			"usePlaceholders":    true,
			"completeUnimported": true,
			"staticcheck":        true,
			"directoryFilters":   []string{"-node_modules", "-vendor"},
			"symbolMatcher":      "FastFuzzy",
			"symbolStyle":        "Dynamic",
			"analyses":           map[string]any{"unusedparams": true, "shadow": true},
		},
	},
	{
		ID:           "typescript-language-server",
		Extensions:   []string{"ts", "tsx", "js", "jsx", "mjs", "cjs", "mts", "cts"},
		LanguageID:   "typescript",
		Command:      "typescript-language-server",
		Args:         []string{"--stdio"},
		RootPatterns: []string{"package.json", "tsconfig.json", "jsconfig.json", ".git"},
		InitOptions: map[string]any{
			"preferences": map[string]any{"includeInlayParameterNameHints": "all"},
		},
	},
	{
		ID:           "pyright",
		Extensions:   []string{"py", "pyi"},
		LanguageID:   "python",
		Command:      "pyright-langserver",
		Args:         []string{"--stdio"},
		RootPatterns: []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", ".git"},
		InitOptions: map[string]any{
			"typeCheckingMode": "basic",
		},
	},
	{
		ID:           "rust-analyzer",
		Extensions:   []string{"rs"},
		LanguageID:   "rust",
		Command:      "rust-analyzer",
		RootPatterns: []string{"Cargo.toml", "Cargo.lock", ".git"},
	},
	{
		ID:           "lua-language-server",
		Extensions:   []string{"lua"},
		LanguageID:   "lua",
		Command:      "lua-language-server",
		RootPatterns: []string{".luarc.json", ".git"},
	},
	{
		ID:           "vscode-json-language-server",
		Extensions:   []string{"json", "jsonc"},
		LanguageID:   "json",
		Command:      "vscode-json-languageserver",
		Args:         []string{"--stdio"},
		RootPatterns: []string{"package.json", ".git"},
	},
	{
		ID:           "yaml-language-server",
		Extensions:   []string{"yaml", "yml"},
		LanguageID:   "yaml",
		Command:      "yaml-language-server",
		Args:         []string{"--stdio"},
		RootPatterns: []string{".git"},
		InitOptions: map[string]any{
			"yaml": map[string]any{
				"schemas":    map[string]any{},
				"validate":   true,
				"hover":      true,
				"completion": true,
			},
		},
	},
	{
		ID:           "marksman",
		Extensions:   []string{"md", "mdx"},
		LanguageID:   "markdown",
		Command:      "marksman",
		Args:         []string{"server"},
		RootPatterns: []string{".git", ".marksman.toml"},
	},
	{
		ID:           "sql-language-server",
		Extensions:   []string{"sql"},
		LanguageID:   "sql",
		Command:      "sql-language-server",
		Args:         []string{"up", "--method", "stdio"},
		RootPatterns: []string{".git"},
	},
	{
		ID:           "dart-language-server",
		Extensions:   []string{"dart"},
		LanguageID:   "dart",
		Command:      "dart",
		Args:         []string{"language-server", "--protocol=lsp"},
		RootPatterns: []string{"pubspec.yaml", ".git"},
	},
	{
		ID:           "jdtls",
		Extensions:   []string{"java"},
		LanguageID:   "java",
		Command:      "jdtls",
		RootPatterns: []string{"pom.xml", "build.gradle", "build.gradle.kts", ".git"},
	},
	{
		ID:           "kotlin-language-server",
		Extensions:   []string{"kt", "kts"},
		LanguageID:   "kotlin",
		Command:      "kotlin-language-server",
		RootPatterns: []string{"build.gradle.kts", "build.gradle", "settings.gradle.kts", ".git"},
	},
	{
		ID:           "clangd",
		Extensions:   []string{"c", "h", "cc", "cpp", "cxx", "hh", "hpp", "hxx"},
		LanguageID:   "cpp",
		Command:      "clangd",
		RootPatterns: []string{"compile_commands.json", "CMakeLists.txt", "Makefile", ".git"},
		InitOptions: map[string]any{
			"clangdFileStatus": true,
		},
	},
	{
		ID:           "omnisharp",
		Extensions:   []string{"cs", "csx"},
		LanguageID:   "csharp",
		Command:      "omnisharp",
		Args:         []string{"-lsp"},
		RootPatterns: []string{"*.sln", "*.csproj", ".git"},
	},
	{
		ID:           "ruby-lsp",
		Extensions:   []string{"rb", "rake", "gemspec"},
		LanguageID:   "ruby",
		Command:      "ruby-lsp",
		RootPatterns: []string{"Gemfile", ".git"},
	},
	{
		ID:           "zls",
		Extensions:   []string{"zig"},
		LanguageID:   "zig",
		Command:      "zls",
		RootPatterns: []string{"build.zig", "build.zig.zon", ".git"},
	},
	{
		ID:           "nil",
		Extensions:   []string{"nix"},
		LanguageID:   "nix",
		Command:      "nil",
		RootPatterns: []string{"flake.nix", "default.nix", ".git"},
	},
	{
		ID:           "swift",
		Extensions:   []string{"swift"},
		LanguageID:   "swift",
		Command:      "sourcekit-lsp",
		RootPatterns: []string{"Package.swift", ".git"},
	},
}

func FindServerForFile(filePath string) *ServerDef {
	ext := strings.ToLower(filepath.Ext(filePath))
	base := filepath.Base(filePath)
	for i := range allServers {
		s := &allServers[i]
		for _, e := range s.Extensions {
			if ext == "."+e {
				cmd := findCommand(s.Command)
				if cmd == "" {
					return nil
				}
				return s
			}
		}
		for _, b := range s.Basenames {
			if base == b {
				cmd := findCommand(s.Command)
				if cmd == "" {
					return nil
				}
				return s
			}
		}
	}
	return nil
}

func findCommand(name string) string {
	if path, err := lookPath(name); err == nil {
		return path
	}
	return ""
}

func lookPath(name string) (string, error) {
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		path := filepath.Join(dir, name)
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			if fi.Mode()&0111 != 0 {
				return path, nil
			}
		}
	}
	return "", os.ErrNotExist
}

func AllServers() []ServerDef {
	return allServers
}

func AvailableServers() []ServerDef {
	var available []ServerDef
	for _, s := range allServers {
		if findCommand(s.Command) != "" {
			available = append(available, s)
		}
	}
	return available
}
