// Package mdstream provides a streaming-aware Markdown renderer.
//
// Feed() buffers incomplete fences / math blocks and only emits complete
// output. Flush() renders whatever is left. Parsing and styling are the
// same AST used by the interactive TUI.
package mdstream

import (
	"strings"
	"sync"

	"github.com/covoyage/covo-agent/internal/diffrender"
	"github.com/covoyage/covo-agent/internal/tui"
	"github.com/covoyage/covonaut/tui/component"
)

// Renderer is a streaming Markdown renderer.
type Renderer struct {
	mu     sync.Mutex
	buffer strings.Builder
	last   string

	enableLaTeX    bool
	enableSyntaxHL bool
	noColor        bool
	width          int64

	colorFence   string
	colorInline  string
	colorMath    string
	colorHeading string
	colorReset   string
}

// NewRenderer creates a new streaming Markdown renderer.
func NewRenderer() *Renderer {
	return &Renderer{
		enableLaTeX:    true,
		enableSyntaxHL: diffrender.SyntaxEnabled(),
		width:          0,
	}
}

// SetColors configures ANSI color codes. Empty strings disable markdown
// chrome (headings, quotes, fences) so tests can assert on plain text.
// Syntax highlighting is controlled separately via SetSyntaxHighlighting.
func (r *Renderer) SetColors(codeFence, inlineCode, math, heading, reset string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.colorFence = codeFence
	r.colorInline = inlineCode
	r.colorMath = math
	r.colorHeading = heading
	r.colorReset = reset
	r.noColor = codeFence == "" && inlineCode == "" && math == "" && heading == "" && reset == ""
}

// SetLaTeX enables/disables LaTeX rendering.
func (r *Renderer) SetLaTeX(enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enableLaTeX = enabled
}

// SetSyntaxHighlighting enables/disables syntax highlighting.
func (r *Renderer) SetSyntaxHighlighting(enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enableSyntaxHL = enabled
}

// SetWidth sets the wrap width in terminal cells. Zero means no wrapping.
func (r *Renderer) SetWidth(width int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.width = width
}

// Feed appends new text and returns any newly renderable output.
func (r *Renderer) Feed(text string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buffer.WriteString(text)
	return r.emit(true)
}

// Flush renders remaining buffered content, including incomplete blocks.
func (r *Renderer) Flush() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.emit(false)
}

// Reset clears the renderer state.
func (r *Renderer) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buffer.Reset()
	r.last = ""
}

func (r *Renderer) emit(skipIncomplete bool) string {
	full := r.render(r.buffer.String(), skipIncomplete)
	old := r.last
	if strings.HasPrefix(full, old) {
		delta := full[len(old):]
		r.last = full
		return delta
	}
	r.last = full
	return full
}

func (r *Renderer) render(src string, skipIncomplete bool) string {
	th := r.theme(skipIncomplete)
	lines := component.RenderMarkdown(src, r.width, th)
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func (r *Renderer) theme(skipIncomplete bool) component.MarkdownTheme {
	th := component.DefaultMarkdownTheme()
	th.DisableSyntax = !r.enableSyntaxHL
	th.SkipIncomplete = skipIncomplete
	th.HighlightFence = func(source, lang string) string {
		return diffrender.HighlightCode(source, lang, r.enableSyntaxHL)
	}
	th.FenceRenderer = func(lang, source string, width int64) []string {
		if lang != "mermaid" {
			return nil
		}
		rendered := tui.RenderMermaid(source)
		return strings.Split(rendered, "\n")
	}
	if !r.enableLaTeX {
		th.MathFn = func(s string) string { return "$$" + s + "$$" }
	}
	if r.noColor {
		id := func(s string) string { return s }
		th.HeadingFn = [6]func(string) string{id, id, id, id, id, id}
		th.EmphasisFn = id
		th.StrongFn = id
		th.StrikeFn = id
		th.MarkFn = id
		th.CodeInlineFn = id
		th.CodeBlockFn = id
		th.CodeFenceFn = id
		th.QuoteFn = id
		th.LinkLabelFn = id
		th.LinkURLFn = id
		th.LinkRendererFn = nil
		th.HRFn = id
		th.ListBulletFn = id
		th.TableBorderFn = id
		th.TableHeaderFn = id
		if r.enableLaTeX {
			th.MathFn = id
		}
	} else {
		reset := r.colorReset
		wrap := func(prefix string) func(string) string {
			return func(s string) string {
				if prefix == "" {
					return s
				}
				return prefix + s + reset
			}
		}
		if r.colorFence != "" {
			fn := wrap(r.colorFence)
			th.CodeFenceFn = fn
			th.CodeBlockFn = fn
		}
		if r.colorInline != "" {
			th.CodeInlineFn = wrap(r.colorInline)
		}
		if r.colorMath != "" && r.enableLaTeX {
			th.MathFn = wrap(r.colorMath)
		}
		if r.colorHeading != "" {
			reset := r.colorReset
			if reset == "" {
				reset = "\033[0m"
			}
			mk := func(extra string) func(string) string {
				return func(s string) string {
					return r.colorHeading + extra + s + reset
				}
			}
			th.HeadingFn = [6]func(string) string{
				mk("\033[1m"),
				mk(""),
				mk("\033[2m"),
				mk("\033[2m"),
				mk("\033[2m\033[3m"),
				mk("\033[2m\033[3m"),
			}
		}
	}
	return th
}

// IsInCodeBlock reports an unclosed fenced code block in the buffer.
func (r *Renderer) IsInCodeBlock() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	fence, _ := component.IncompleteMarkdown(r.buffer.String())
	return fence
}

// IsInMathBlock reports an unclosed $$ math block in the buffer.
func (r *Renderer) IsInMathBlock() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, math := component.IncompleteMarkdown(r.buffer.String())
	return math
}
