package diag

import (
	"strings"
	"testing"
)

func TestRunDiagnostics(t *testing.T) {
	r := RunDiagnostics()
	if r == nil {
		t.Fatal("expected report")
	}
	if len(r.Checks) == 0 {
		t.Fatal("expected at least some checks")
	}
}

func TestReport_Print(t *testing.T) {
	r := &Report{
		Checks: []Check{
			{Name: "test", Status: "✓", Detail: "ok", Category: "terminal"},
		},
	}
	var sb strings.Builder
	r.Print(&sb)
	if !strings.Contains(sb.String(), "test") {
		t.Error("expected output to contain check name")
	}
}

func TestReport_HasIssues(t *testing.T) {
	r := &Report{
		Checks: []Check{
			{Name: "ok", Status: "✓", Detail: "fine", Category: "test"},
		},
	}
	if r.HasIssues() {
		t.Error("expected no issues")
	}

	r.Checks = append(r.Checks, Check{Name: "bad", Status: "⚠", Detail: "problem", Category: "test"})
	if !r.HasIssues() {
		t.Error("expected issues")
	}
}

func TestReport_PrintFixes(t *testing.T) {
	r := &Report{
		Checks: []Check{
			{Name: "tmux_clipboard", Status: "⚠", Detail: "set-clipboard=off — clipboard may not work, run: tmux set -g set-clipboard on", Category: "tmux"},
		},
	}
	var sb strings.Builder
	r.PrintFixes(&sb)
	if !strings.Contains(sb.String(), "tmux set -g set-clipboard on") {
		t.Error("expected fix suggestion")
	}
}

func TestPlatform(t *testing.T) {
	p := Platform()
	if p == "" {
		t.Error("expected non-empty platform")
	}
	if !strings.Contains(p, "/") {
		t.Error("expected format os/arch")
	}
}
