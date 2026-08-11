package agent

import (
	"testing"
)

func TestIsNonCodeFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"README.md", true},
		{"docs/guide.md", true},
		{"config.yaml", true},
		{"package.json", true},
		{"style.css", true},
		{"image.png", true},
		{"LICENSE", true},
		{"CHANGELOG.md", true},
		{"main.go", false},
		{"src/app.py", false},
		{"test/foo_test.go", false},
		{"internal/handler.rs", false},
		{"Makefile", false},
		{"Dockerfile", false},
		{".env", true},
		{"go.sum", true},
		{"go.mod", true},
		{"Cargo.lock", true},
	}
	for _, tt := range tests {
		if got := isNonCodeFile(tt.path); got != tt.want {
			t.Errorf("isNonCodeFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestExtractFilePath(t *testing.T) {
	tests := []struct {
		args string
		want string
	}{
		{`{"path":"/tmp/foo.go"}`, "/tmp/foo.go"},
		{`{"file_path":"main.py"}`, "main.py"},
		{`{"filePath":"src/app.ts"}`, "src/app.ts"},
		{`{"target":"output.txt"}`, "output.txt"},
		{`{"command":"go test"}`, ""},
		{`not json`, ""},
		{`{}`, ""},
	}
	for _, tt := range tests {
		if got := extractFilePath(tt.args); got != tt.want {
			t.Errorf("extractFilePath(%q) = %q, want %q", tt.args, got, tt.want)
		}
	}
}

func TestContainsTestPattern(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"go test ./...", true},
		{"go vet ./...", true},
		{"pytest tests/", true},
		{"cargo test", true},
		{"npm test", true},
		{"make test", true},
		{"echo hello", false},
		{"cat file.txt", false},
		{"ls -la", false},
		{"go run main.go", false},
	}
	for _, tt := range tests {
		if got := containsTestPattern(tt.cmd); got != tt.want {
			t.Errorf("containsTestPattern(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestBuildVerificationNudge(t *testing.T) {
	files := []string{"main.go", "handler.go"}
	nudge := buildVerificationNudge(files)
	if nudge == "" {
		t.Fatal("expected non-empty nudge")
	}
	if !containsString(nudge, "main.go") {
		t.Error("nudge should mention main.go")
	}
	if !containsString(nudge, "handler.go") {
		t.Error("nudge should mention handler.go")
	}
}

func TestVerificationGuardHookReset(t *testing.T) {
	h := &verificationGuardHook{}
	h.filesModified = []string{"a.go", "b.go"}
	h.testsRan = true
	h.reset()
	if len(h.filesModified) != 0 {
		t.Error("expected filesModified to be cleared")
	}
	if h.testsRan {
		t.Error("expected testsRan to be false")
	}
}

func TestVerificationGuardNonDocFiles(t *testing.T) {
	h := &verificationGuardHook{}
	h.filesModified = []string{"main.go", "README.md", "handler.py", "config.yaml", "test/foo_test.go"}
	got := h.nonDocModifiedFiles()
	if len(got) != 3 {
		t.Errorf("expected 3 code files, got %d: %v", len(got), got)
	}
}

func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
