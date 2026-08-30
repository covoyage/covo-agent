package diffrender

import (
	"strings"
	"testing"
)

const pyDiff = `--- a/weather_sh.py
+++ b/weather_sh.py
@@ -1,0 +1,3 @@
+import json, subprocess, sys
-old = "gone"
 d = json.loads(result.stdout)`

func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		if r == '\x1b' {
			inEscape = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestColorize_KeepsDiffContent(t *testing.T) {
	out := Colorize(pyDiff, true)
	if got := stripANSI(out); got != pyDiff {
		t.Errorf("colorize changed the diff content:\nwant %q\ngot  %q", pyDiff, got)
	}
}

func TestColorize_DiffSemanticColors(t *testing.T) {
	out := Colorize(pyDiff, false)
	if !strings.Contains(out, ansiGreen) {
		t.Error("expected green for + lines")
	}
	if !strings.Contains(out, ansiRed) {
		t.Error("expected red for - lines")
	}
	if !strings.Contains(out, ansiCyan) {
		t.Error("expected cyan for @@ hunk header")
	}
	if !strings.Contains(out, ansiBold) {
		t.Error("expected bold for file headers")
	}
}

func TestColorize_SyntaxHighlighting(t *testing.T) {
	out := Colorize(pyDiff, true)
	stripped := stripANSI(out)
	if stripped != pyDiff {
		t.Fatalf("content changed: %q", stripped)
	}
	// Token-level highlighting must add more color codes than the plain
	// diff coloring would (import/keywords/strings get their own SGR runs).
	if strings.Count(out, "\x1b[") <= strings.Count(Colorize(pyDiff, false), "\x1b[") {
		t.Error("expected syntax highlighting to add ANSI color runs")
	}
}

func TestColorize_SyntaxDisabledNoExtraColors(t *testing.T) {
	withSyntax := Colorize(pyDiff, true)
	without := Colorize(pyDiff, false)
	if strings.Count(withSyntax, "\x1b[38;5;") == 0 && strings.Count(withSyntax, "\x1b[38;2;") == 0 {
		t.Skip("formatter did not emit extended-color codes; terminal256/16m not active")
	}
	if strings.Count(without, "\x1b[38;5;") != 0 || strings.Count(without, "\x1b[38;2;") != 0 {
		t.Error("syntax-disabled output must not contain token colors")
	}
}

func TestColorize_UnknownLanguageDiffColorsOnly(t *testing.T) {
	diff := "--- a/readme.unknownext\n+++ b/readme.unknownext\n@@ -1 +1 @@\n+hello"
	out := Colorize(diff, true)
	if got := stripANSI(out); got != diff {
		t.Errorf("content changed: %q", got)
	}
	if strings.Contains(out, "\x1b[38;5;") || strings.Contains(out, "\x1b[38;2;") {
		t.Error("unknown language must not produce token colors")
	}
}

func TestColorize_Empty(t *testing.T) {
	if got := Colorize("", true); got != "" {
		t.Errorf("Colorize(\"\") = %q, want empty", got)
	}
}

func TestSyntaxEnabled_Env(t *testing.T) {
	t.Setenv("COVO_SYNTAX_HIGHLIGHT", "")
	if !SyntaxEnabled() {
		t.Error("default should be enabled")
	}
	for _, v := range []string{"0", "false", "off", "no", "FALSE"} {
		t.Setenv("COVO_SYNTAX_HIGHLIGHT", v)
		if SyntaxEnabled() {
			t.Errorf("COVO_SYNTAX_HIGHLIGHT=%q should disable", v)
		}
	}
	t.Setenv("COVO_SYNTAX_HIGHLIGHT", "1")
	if !SyntaxEnabled() {
		t.Error("COVO_SYNTAX_HIGHLIGHT=1 should enable")
	}
}

func TestHighlightCode(t *testing.T) {
	if got := HighlightCode("x := 1", "", true); got != "x := 1" {
		t.Errorf("empty lang should return source unchanged, got %q", got)
	}
	if got := HighlightCode("x := 1", "python", false); got != "x := 1" {
		t.Errorf("syntax off should return source unchanged, got %q", got)
	}
	out := HighlightCode("x = \"hi\"  # comment", "python", true)
	if out == "x = \"hi\"  # comment" {
		t.Error("expected python highlighting to change output")
	}
	if got := stripANSI(out); got != "x = \"hi\"  # comment" {
		t.Errorf("highlighting changed content: %q", got)
	}
}

func TestDiffFilename(t *testing.T) {
	lines := strings.Split(pyDiff, "\n")
	if got := diffFilename(lines); got != "weather_sh.py" {
		t.Errorf("diffFilename = %q, want weather_sh.py", got)
	}
	if got := diffFilename([]string{"+++ /dev/null", "--- /dev/null"}); got != "" {
		t.Errorf("dev/null should yield empty, got %q", got)
	}
}
