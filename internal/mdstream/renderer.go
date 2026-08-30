// Package mdstream provides a streaming-aware Markdown renderer with
// LaTeX math formula rendering and syntax highlighting adaptation.
//
// The renderer is designed to handle partial Markdown input that grows
// incrementally (streaming from an LLM). It buffers incomplete blocks
// (code fences, math delimiters, etc.) and only renders complete blocks.
package mdstream

import (
	"fmt"
	"strings"
	"sync"
	"unicode"

	"github.com/covoyage/covo-agent/internal/diffrender"
)

// BlockType represents the type of a parsed Markdown block.
type BlockType int

const (
	BlockText       BlockType = iota // plain text / inline markdown
	BlockCodeFence                   // ``` code block
	BlockCodeInline                  // `inline code`
	BlockMathBlock                   // $$ block math $$
	BlockMathInline                  // $ inline math $
	BlockHeading                     // # heading
	BlockList                        // - item / 1. item
	BlockTable                       // | table |
	BlockQuote                       // > quote
	BlockBlank                       // empty line
)

// Block represents a parsed Markdown block.
type Block struct {
	Type     BlockType
	Content  string
	Lang     string // for code blocks
	Complete bool   // true if the block is fully formed
}

// Renderer is a streaming Markdown renderer.
type Renderer struct {
	mu           sync.Mutex
	buffer       strings.Builder
	blocks       []Block
	renderedText string // full rendered output of all complete blocks so far

	// Configuration
	enableLaTeX     bool
	enableSyntaxHL  bool
	colorCodeFence  string
	colorInlineCode string
	colorMath       string
	colorHeading    string
	colorReset      string
}

// NewRenderer creates a new streaming Markdown renderer.
func NewRenderer() *Renderer {
	return &Renderer{
		enableLaTeX:    true,
		enableSyntaxHL: diffrender.SyntaxEnabled(),
		// ANSI color codes (empty = no color)
		colorCodeFence:  "\033[36m", // cyan
		colorInlineCode: "\033[33m", // yellow
		colorMath:       "\033[35m", // magenta
		colorHeading:    "\033[1m",  // bold
		colorReset:      "\033[0m",
	}
}

// SetColors configures the ANSI color codes. Pass empty strings to disable.
func (r *Renderer) SetColors(codeFence, inlineCode, math, heading, reset string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.colorCodeFence = codeFence
	r.colorInlineCode = inlineCode
	r.colorMath = math
	r.colorHeading = heading
	r.colorReset = reset
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

// Feed appends new text to the renderer and returns any newly renderable output.
func (r *Renderer) Feed(text string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buffer.WriteString(text)
	r.parseBlocks()
	return r.renderNew()
}

// Flush renders any remaining buffered content and returns it.
func (r *Renderer) Flush() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Mark all incomplete blocks as complete for final render
	for i := range r.blocks {
		r.blocks[i].Complete = true
	}
	return r.renderNew()
}

// Reset clears the renderer state.
func (r *Renderer) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buffer.Reset()
	r.blocks = nil
	r.renderedText = ""
}

// renderComplete renders all complete blocks to a single string.
// This is called on each Feed() and the result is diffed against
// the previously rendered text to produce incremental output.
func (r *Renderer) renderComplete() string {
	var sb strings.Builder
	for _, block := range r.blocks {
		if !block.Complete {
			break
		}
		sb.WriteString(r.renderBlock(block))
	}
	return sb.String()
}

// parseBlocks scans the buffer and extracts complete Markdown blocks.
func (r *Renderer) parseBlocks() {
	content := r.buffer.String()
	r.blocks = nil

	lines := strings.Split(content, "\n")
	i := 0
	for i < len(lines) {
		line := lines[i]

		// Code fence
		if strings.HasPrefix(strings.TrimLeft(line, " "), "```") {
			lang := strings.TrimPrefix(strings.TrimLeft(line, " "), "```")
			lang = strings.TrimSpace(lang)

			// Find closing fence
			closed := false
			var codeLines []string
			for j := i + 1; j < len(lines); j++ {
				if strings.HasPrefix(strings.TrimLeft(lines[j], " "), "```") {
					closed = true
					r.blocks = append(r.blocks, Block{
						Type:     BlockCodeFence,
						Content:  strings.Join(codeLines, "\n"),
						Lang:     lang,
						Complete: true,
					})
					i = j + 1
					break
				}
				codeLines = append(codeLines, lines[j])
			}
			if !closed {
				// Incomplete code block
				r.blocks = append(r.blocks, Block{
					Type:     BlockCodeFence,
					Content:  strings.Join(codeLines, "\n"),
					Lang:     lang,
					Complete: false,
				})
				i = len(lines)
			}
			continue
		}

		// Block math $$...$$
		if strings.Contains(line, "$$") {
			// Find if this line contains both opening and closing $$
			parts := strings.SplitN(line, "$$", 3)
			if len(parts) >= 3 {
				// Complete on one line: text $$math$$ text
				r.blocks = append(r.blocks, Block{
					Type:     BlockMathBlock,
					Content:  parts[1],
					Complete: true,
				})
				i++
				continue
			}

			// Multi-line: find closing $$
			closed := false
			var mathLines []string
			for j := i + 1; j < len(lines); j++ {
				if strings.Contains(lines[j], "$$") {
					closed = true
					beforeClose := strings.SplitN(lines[j], "$$", 2)
					mathLines = append(mathLines, beforeClose[0])
					r.blocks = append(r.blocks, Block{
						Type:     BlockMathBlock,
						Content:  strings.Join(mathLines, "\n"),
						Complete: true,
					})
					i = j + 1
					break
				}
				mathLines = append(mathLines, lines[j])
			}
			if !closed {
				r.blocks = append(r.blocks, Block{
					Type:     BlockMathBlock,
					Content:  strings.Join(mathLines, "\n"),
					Complete: false,
				})
				i = len(lines)
			}
			continue
		}

		// Heading
		trimmed := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trimmed, "#") {
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			if level <= 6 && level < len(trimmed) && trimmed[level] == ' ' {
				r.blocks = append(r.blocks, Block{
					Type:     BlockHeading,
					Content:  strings.TrimSpace(trimmed[level:]),
					Complete: true,
				})
				i++
				continue
			}
		}

		// Empty line
		if strings.TrimSpace(line) == "" {
			r.blocks = append(r.blocks, Block{Type: BlockBlank, Content: "", Complete: true})
			i++
			continue
		}

		// Regular text line (collect consecutive non-empty lines).
		// The first line is always collected (even if it looks like a
		// heading/code/math marker) to avoid infinite loops during
		// streaming when partial markers arrive character by character.
		var textLines []string
		for i < len(lines) {
			l := lines[i]
			if strings.TrimSpace(l) == "" {
				break
			}
			// After the first line, check if this line starts a new block type
			if len(textLines) > 0 {
				trimmed := strings.TrimLeft(l, " ")
				if strings.HasPrefix(trimmed, "```") ||
					strings.HasPrefix(trimmed, "#") ||
					strings.Contains(l, "$$") {
					break
				}
			}
			textLines = append(textLines, l)
			i++
		}
		if len(textLines) > 0 {
			r.blocks = append(r.blocks, Block{
				Type:     BlockText,
				Content:  strings.Join(textLines, "\n"),
				Complete: true,
			})
		}
	}
}

// renderNew renders all complete blocks and returns only the new
// portion (the suffix that wasn't in the previous render).
func (r *Renderer) renderNew() string {
	full := r.renderComplete()
	// Find the common prefix length
	old := r.renderedText
	if strings.HasPrefix(full, old) {
		delta := full[len(old):]
		r.renderedText = full
		return delta
	}
	// Fallback: if the new render doesn't start with the old one
	// (e.g. a block type changed during streaming), re-render everything
	r.renderedText = full
	return full
}

// renderBlock renders a single block to terminal-friendly output.
func (r *Renderer) renderBlock(b Block) string {
	switch b.Type {
	case BlockCodeFence:
		return r.renderCodeFence(b)
	case BlockMathBlock:
		return r.renderMathBlock(b)
	case BlockHeading:
		return r.renderHeading(b)
	case BlockBlank:
		return "\n"
	case BlockText:
		return r.renderText(b.Content) + "\n"
	default:
		return b.Content + "\n"
	}
}

func (r *Renderer) renderCodeFence(b Block) string {
	var sb strings.Builder
	if r.colorCodeFence != "" {
		sb.WriteString(r.colorCodeFence)
	}
	if b.Lang != "" {
		sb.WriteString(fmt.Sprintf("[%s]\n", b.Lang))
	}
	// Token-level syntax highlighting is gated by SetSyntaxHighlighting
	// (NewRenderer defaults it from COVO_SYNTAX_HIGHLIGHT). Unrecognized
	// languages pass through unchanged.
	if content := diffrender.HighlightCode(b.Content, b.Lang, r.enableSyntaxHL); content != b.Content {
		sb.WriteString(content)
	} else {
		sb.WriteString(b.Content)
	}
	sb.WriteString("\n")
	if r.colorReset != "" {
		sb.WriteString(r.colorReset)
	}
	return sb.String()
}

func (r *Renderer) renderMathBlock(b Block) string {
	if !r.enableLaTeX {
		return "$$" + b.Content + "$$\n"
	}
	var sb strings.Builder
	if r.colorMath != "" {
		sb.WriteString(r.colorMath)
	}
	sb.WriteString("⟨ ")
	sb.WriteString(strings.TrimSpace(b.Content))
	sb.WriteString(" ⟩")
	if r.colorReset != "" {
		sb.WriteString(r.colorReset)
	}
	return sb.String() + "\n"
}

func (r *Renderer) renderHeading(b Block) string {
	var sb strings.Builder
	if r.colorHeading != "" {
		sb.WriteString(r.colorHeading)
	}
	sb.WriteString(b.Content)
	if r.colorReset != "" {
		sb.WriteString(r.colorReset)
	}
	return sb.String() + "\n"
}

func (r *Renderer) renderText(text string) string {
	// Inline processing: code spans and inline math
	var sb strings.Builder
	i := 0
	for i < len(text) {
		// Inline code: `code`
		if text[i] == '`' {
			end := strings.Index(text[i+1:], "`")
			if end >= 0 {
				code := text[i+1 : i+1+end]
				if r.colorInlineCode != "" {
					sb.WriteString(r.colorInlineCode)
				}
				sb.WriteString(code)
				if r.colorReset != "" {
					sb.WriteString(r.colorReset)
				}
				i += end + 2
				continue
			}
		}

		// Inline math: $math$
		if r.enableLaTeX && text[i] == '$' {
			// Must not be preceded by a non-space char (to avoid currency)
			if i == 0 || unicode.IsSpace(rune(text[i-1])) {
				end := strings.Index(text[i+1:], "$")
				if end >= 0 {
					math := text[i+1 : i+1+end]
					// Ensure closing $ is followed by space or end
					afterIdx := i + 1 + end + 1
					if afterIdx >= len(text) || unicode.IsSpace(rune(text[afterIdx])) || text[afterIdx] == '.' || text[afterIdx] == ',' {
						if r.colorMath != "" {
							sb.WriteString(r.colorMath)
						}
						sb.WriteString("⟨")
						sb.WriteString(math)
						sb.WriteString("⟩")
						if r.colorReset != "" {
							sb.WriteString(r.colorReset)
						}
						i += end + 2
						continue
					}
				}
			}
		}

		sb.WriteByte(text[i])
		i++
	}
	return sb.String()
}

// IsInCodeBlock returns true if the current buffer is inside an unclosed code block.
func (r *Renderer) IsInCodeBlock() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range r.blocks {
		if b.Type == BlockCodeFence && !b.Complete {
			return true
		}
	}
	return false
}

// IsInMathBlock returns true if the current buffer is inside an unclosed math block.
func (r *Renderer) IsInMathBlock() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range r.blocks {
		if b.Type == BlockMathBlock && !b.Complete {
			return true
		}
	}
	return false
}
