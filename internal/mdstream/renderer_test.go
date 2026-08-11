package mdstream

import (
	"strings"
	"testing"
)

func TestRenderer_BasicText(t *testing.T) {
	r := NewRenderer()
	r.SetColors("", "", "", "", "") // disable colors for testing
	out := r.Feed("Hello world\n")
	if !strings.Contains(out, "Hello world") {
		t.Errorf("expected 'Hello world', got %q", out)
	}
}

func TestRenderer_Heading(t *testing.T) {
	r := NewRenderer()
	r.SetColors("", "", "", "", "")
	out := r.Feed("# My Heading\n")
	if !strings.Contains(out, "My Heading") {
		t.Errorf("expected heading text, got %q", out)
	}
}

func TestRenderer_CodeFence(t *testing.T) {
	r := NewRenderer()
	r.SetColors("", "", "", "", "")
	out := r.Feed("```go\npackage main\n```\n")
	if !strings.Contains(out, "package main") {
		t.Errorf("expected code content, got %q", out)
	}
	if !strings.Contains(out, "[go]") {
		t.Errorf("expected language tag, got %q", out)
	}
}

func TestRenderer_CodeFence_Incomplete(t *testing.T) {
	r := NewRenderer()
	r.SetColors("", "", "", "", "")
	// Feed incomplete code block
	out := r.Feed("```go\npackage main\n")
	// Should not render incomplete block
	if strings.Contains(out, "package main") {
		t.Errorf("should not render incomplete block, got %q", out)
	}
	// Complete it
	out = r.Feed("```\n")
	if !strings.Contains(out, "package main") {
		t.Errorf("should render after completion, got %q", out)
	}
}

func TestRenderer_MathBlock(t *testing.T) {
	r := NewRenderer()
	r.SetColors("", "", "", "", "")
	out := r.Feed("$$E = mc^2$$\n")
	if !strings.Contains(out, "E = mc^2") {
		t.Errorf("expected math content, got %q", out)
	}
}

func TestRenderer_MathBlock_Multiline(t *testing.T) {
	r := NewRenderer()
	r.SetColors("", "", "", "", "")
	out := r.Feed("$$\nx^2 + y^2 = z^2\n$$\n")
	if !strings.Contains(out, "x^2 + y^2 = z^2") {
		t.Errorf("expected multiline math, got %q", out)
	}
}

func TestRenderer_MathBlock_Incomplete(t *testing.T) {
	r := NewRenderer()
	r.SetColors("", "", "", "", "")
	out := r.Feed("$$\nx^2 + y^2\n")
	if strings.Contains(out, "x^2") {
		t.Errorf("should not render incomplete math, got %q", out)
	}
	out = r.Feed("$$\n")
	if !strings.Contains(out, "x^2") {
		t.Errorf("should render after completion, got %q", out)
	}
}

func TestRenderer_InlineCode(t *testing.T) {
	r := NewRenderer()
	r.SetColors("", "", "", "", "")
	out := r.Feed("Use `fmt.Println` to print.\n")
	if !strings.Contains(out, "fmt.Println") {
		t.Errorf("expected inline code, got %q", out)
	}
}

func TestRenderer_InlineMath(t *testing.T) {
	r := NewRenderer()
	r.SetColors("", "", "", "", "")
	out := r.Feed("The formula $E = mc^2$ is famous.\n")
	if !strings.Contains(out, "E = mc^2") {
		t.Errorf("expected inline math, got %q", out)
	}
}

func TestRenderer_InlineMath_NotCurrency(t *testing.T) {
	r := NewRenderer()
	r.SetColors("", "", "", "", "")
	out := r.Feed("The price is $5 and $10.\n")
	// Should not treat $5 as math
	if strings.Contains(out, "⟨5⟩") {
		t.Errorf("should not treat currency as math: %q", out)
	}
}

func TestRenderer_Flush(t *testing.T) {
	r := NewRenderer()
	r.SetColors("", "", "", "", "")
	r.Feed("```go\npackage main\n") // incomplete
	out := r.Flush()
	// Flush should render incomplete blocks
	if !strings.Contains(out, "package main") {
		t.Errorf("flush should render incomplete blocks, got %q", out)
	}
}

func TestRenderer_Reset(t *testing.T) {
	r := NewRenderer()
	r.Feed("Hello\n")
	r.Reset()
	if r.buffer.Len() != 0 {
		t.Error("buffer should be empty after reset")
	}
}

func TestRenderer_MultipleBlocks(t *testing.T) {
	r := NewRenderer()
	r.SetColors("", "", "", "", "")
	out := r.Feed("# Title\n\nSome text.\n\n```go\ncode\n```\n")
	if !strings.Contains(out, "Title") {
		t.Error("missing heading")
	}
	if !strings.Contains(out, "Some text") {
		t.Error("missing text")
	}
	if !strings.Contains(out, "code") {
		t.Error("missing code")
	}
}

func TestRenderer_IsInCodeBlock(t *testing.T) {
	r := NewRenderer()
	r.SetColors("", "", "", "", "")
	r.Feed("```go\n")
	if !r.IsInCodeBlock() {
		t.Error("expected to be in code block")
	}
	r.Feed("```\n")
	if r.IsInCodeBlock() {
		t.Error("expected not in code block after closing")
	}
}

func TestRenderer_IsInMathBlock(t *testing.T) {
	r := NewRenderer()
	r.SetColors("", "", "", "", "")
	r.Feed("$$\n")
	if !r.IsInMathBlock() {
		t.Error("expected to be in math block")
	}
	r.Feed("$$\n")
	if r.IsInMathBlock() {
		t.Error("expected not in math block after closing")
	}
}

func TestRenderer_LaTeXDisabled(t *testing.T) {
	r := NewRenderer()
	r.SetColors("", "", "", "", "")
	r.SetLaTeX(false)
	out := r.Feed("$$E = mc^2$$\n")
	if !strings.Contains(out, "$$") {
		t.Errorf("with LaTeX disabled, should show raw $$: %q", out)
	}
}

func TestRenderer_Streaming(t *testing.T) {
	r := NewRenderer()
	r.SetColors("", "", "", "", "")

	// Simulate streaming text line by line (realistic streaming pattern)
	text := "# Hello\n\nWorld\n"
	var output strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		if line == "" {
			continue
		}
		output.WriteString(r.Feed(line))
	}

	final := output.String() + r.Flush()
	if !strings.Contains(final, "Hello") {
		t.Error("missing heading in streamed output")
	}
	if !strings.Contains(final, "World") {
		t.Error("missing text in streamed output")
	}
}

func TestRenderer_Colors(t *testing.T) {
	r := NewRenderer()
	r.SetColors("\033[36m", "\033[33m", "\033[35m", "\033[1m", "\033[0m")
	out := r.Feed("```go\ncode\n```\n")
	if !strings.Contains(out, "\033[36m") {
		t.Error("expected color code in output")
	}
	if !strings.Contains(out, "\033[0m") {
		t.Error("expected reset code in output")
	}
}
