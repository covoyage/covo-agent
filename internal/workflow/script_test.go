package workflow

import "testing"

func TestAnalyzeWorkflowScript(t *testing.T) {
	src := `
name("demo")
phase("map", "list the failing tests")
ask("confirm target")
spawn("fix", "coder")
phase("verify", "run the tests")
`
	a := Analyze(src)
	if len(a.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %+v", a.Diagnostics)
	}
	if len(a.Sites) != 4 {
		t.Fatalf("sites=%d want 4", len(a.Sites))
	}
	if a.AskCount != 1 || a.SpawnCount != 1 {
		t.Fatalf("ask=%d spawn=%d", a.AskCount, a.SpawnCount)
	}
	if a.Mermaid == "" || len(a.Phases) != 3 {
		t.Fatalf("graph/phases missing: mermaid=%q phases=%d", a.Mermaid, len(a.Phases))
	}
}

func TestAnalyzeRejectsUnknownAndDuplicates(t *testing.T) {
	src := "phase(\"a\")\nfoo()\nphase(\"a\")\n"
	a := Analyze(src)
	if len(a.Diagnostics) < 2 {
		t.Fatalf("want diagnostics, got %+v", a.Diagnostics)
	}
}
