package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlyDefaults(t *testing.T) {
	m := NewReadOnlyManager("")
	tests := []struct {
		path  string
		want  bool
		label string
	}{
		{"/project/vendor/github.com/foo/bar.go", true, "vendor file"},
		{"/project/node_modules/foo/index.js", true, "node_modules file"},
		{"/project/go.sum", true, "go.sum"},
		{"/project/pkg/pb/message.pb.go", true, "protobuf generated"},
		{"/project/pkg/gen/config_gen.go", true, "_gen generated"},
		{"/project/pkg/generated/api_generated.go", true, "_generated file"},
		{"/project/static/bundle.min.js", true, "minified JS"},
		{"/project/static/style.min.css", true, "minified CSS"},
		{"/project/package-lock.json", true, "package-lock"},
		{"/project/yarn.lock", true, "yarn.lock"},
		{"/project/Cargo.lock", true, "Cargo.lock"},
		{"/project/main.go", false, "normal source file"},
		{"/project/internal/util.go", false, "normal util file"},
		{"/project/web/src/app.ts", false, "normal TS file"},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			got := m.IsReadOnly(tt.path)
			if got != tt.want {
				t.Errorf("IsReadOnly(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestReadOnlyCheckReadOnly(t *testing.T) {
	dir := t.TempDir()
	m := NewReadOnlyManager(dir)

	// A normal file should pass
	vendorFile := filepath.Join(dir, "vendor", "foo", "bar.go")
	os.MkdirAll(filepath.Dir(vendorFile), 0755)
	os.WriteFile(vendorFile, []byte("package foo\n"), 0644)

	if err := m.CheckReadOnly(vendorFile); err == nil {
		t.Error("expected error for vendor file, got nil")
	}

	normalFile := filepath.Join(dir, "main.go")
	os.WriteFile(normalFile, []byte("package main\n"), 0644)
	if err := m.CheckReadOnly(normalFile); err != nil {
		t.Errorf("unexpected error for normal file: %v", err)
	}
}

func TestReadOnlyEnvVar(t *testing.T) {
	t.Setenv("COVO_READ_ONLY", "*.generated.*,secret/**")
	m := NewReadOnlyManager("")
	tests := []struct {
		path string
		want bool
	}{
		{"/project/code.generated.ts", true},
		{"/project/secret/keys.yaml", true},
		{"/project/secret/deep/file.txt", true},
		{"/project/main.go", false},
	}
	for _, tt := range tests {
		if got := m.IsReadOnly(tt.path); got != tt.want {
			t.Errorf("IsReadOnly(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestReadOnlyCovoignore(t *testing.T) {
	dir := t.TempDir()
	ignoreContent := `# comments are ignored
*.tf
gen/**
`
	os.WriteFile(filepath.Join(dir, ".covoignore"), []byte(ignoreContent), 0644)

	m := NewReadOnlyManager(dir)
	tests := []struct {
		path string
		want bool
	}{
		{filepath.Join(dir, "main.tf"), true},
		{filepath.Join(dir, "gen/output.go"), true},
		{filepath.Join(dir, "gen/sub/pkg/code.go"), true},
		{filepath.Join(dir, "main.go"), false},
	}
	for _, tt := range tests {
		if got := m.IsReadOnly(tt.path); got != tt.want {
			t.Errorf("IsReadOnly(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchByGlob(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"vendor/**", "vendor/github.com/foo/bar.go", true},
		{"vendor/**", "vendor/bar.go", true},
		{"vendor/**", "src/vendor/bar.go", true},
		{"*.pb.go", "/project/message.pb.go", true},
		{"*_gen.go", "/project/config_gen.go", true},
		{"secret/**", "secret/keys.yaml", true},
		{"secret/**", "secret/deep/nested/file.yaml", true},
		{"gen/**", "gen/output/file.go", true},
		{"gen/**", "gen/file.go", true},
		{"gen/**", "src/gen/file.go", true},
		{"**/testdata/**", "pkg/testdata/fixtures/input.json", true},
		{"**/testdata/**", "testdata/input.json", true},
		{"**/testdata/**", "pkg/input.json", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.path, func(t *testing.T) {
			got := MatchByGlob(tt.pattern, tt.path)
			if got != tt.want {
				t.Errorf("MatchByGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

func TestReadOnlyPatterns(t *testing.T) {
	m := NewReadOnlyManager("")
	patterns := m.Patterns()
	if len(patterns) == 0 {
		t.Fatal("expected default patterns")
	}
	found := false
	for _, p := range patterns {
		if p == "vendor/**" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'vendor/**' in default patterns")
	}
}

func TestAutoFormatFormatterForFile(t *testing.T) {
	tests := []struct {
		path    string
		wantCmd string
	}{
		{"main.go", "gofmt"},
		{"server.rs", "rustfmt"},
		{"app.dart", "dart"},
		{"build.zig", "zig"},
		{"program.cs", "dotnet"},
		{"main.py", ""},
		{"index.js", ""},
		{"data.json", ""},
		{"file.txt", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			f := formatterForFile(tt.path)
			if tt.wantCmd == "" {
				if f != nil {
					t.Errorf("expected nil for %q, got %q", tt.path, f.cmd)
				}
				return
			}
			if f == nil {
				t.Errorf("expected formatter %q for %q, got nil", tt.wantCmd, tt.path)
				return
			}
			if f.cmd != tt.wantCmd {
				t.Errorf("for %q: expected cmd %q, got %q", tt.path, tt.wantCmd, f.cmd)
			}
		})
	}
}
