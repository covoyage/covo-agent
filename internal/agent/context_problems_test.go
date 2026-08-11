package agent

import (
	"strings"
	"testing"
)

func TestExpandProblemsReference_NilProvider(t *testing.T) {
	// Save and restore.
	saved := problemsProvider
	problemsProvider = nil
	defer func() { problemsProvider = saved }()

	ref := ContextReference{Raw: "@problems", Kind: "problems"}
	msg, injected := expandProblemsReference(ref, "/tmp")

	if injected != "" {
		t.Errorf("expected empty injected content, got %q", injected)
	}
	if !strings.Contains(msg, "not available") {
		t.Errorf("expected 'not available' message, got %q", msg)
	}
}

func TestExpandProblemsReference_WithProvider(t *testing.T) {
	saved := problemsProvider
	problemsProvider = func(cwd string) string {
		return "main.go:10:5: undefined: foo\nmain.go:15:3: unused variable"
	}
	defer func() { problemsProvider = saved }()

	ref := ContextReference{Raw: "@problems", Kind: "problems"}
	msg, injected := expandProblemsReference(ref, "/tmp")

	if msg != "" {
		t.Errorf("expected empty message, got %q", msg)
	}
	if !strings.Contains(injected, "main.go:10:5") {
		t.Errorf("injected should contain diagnostic, got %q", injected)
	}
	if !strings.Contains(injected, "@problems") {
		t.Errorf("injected should contain raw reference, got %q", injected)
	}
}

func TestExpandProblemsReference_EmptyDiagnostics(t *testing.T) {
	saved := problemsProvider
	problemsProvider = func(cwd string) string {
		return "" // no problems
	}
	defer func() { problemsProvider = saved }()

	ref := ContextReference{Raw: "@problems", Kind: "problems"}
	msg, injected := expandProblemsReference(ref, "/tmp")

	if msg != "" {
		t.Errorf("expected empty message, got %q", msg)
	}
	if !strings.Contains(injected, "no problems detected") {
		t.Errorf("injected should say 'no problems detected', got %q", injected)
	}
}

func TestContextReferenceRegex_MatchesProblems(t *testing.T) {
	// The @problems reference should be parsed by the regex.
	refs := ParseContextReferences("@problems please check")
	if len(refs) != 1 {
		t.Fatalf("expected 1 reference, got %d: %+v", len(refs), refs)
	}
	if refs[0].Kind != "problems" {
		t.Errorf("expected kind 'problems', got %q", refs[0].Kind)
	}
	if refs[0].Raw != "@problems" {
		t.Errorf("expected raw '@problems', got %q", refs[0].Raw)
	}
}

func TestContextReferenceRegex_MatchesProblemsInSentence(t *testing.T) {
	refs := ParseContextReferences("Fix the issues shown in @problems and report back")
	if len(refs) != 1 {
		t.Fatalf("expected 1 reference, got %d", len(refs))
	}
	if refs[0].Kind != "problems" {
		t.Errorf("expected kind 'problems', got %q", refs[0].Kind)
	}
}
