// Package diffrender colorizes unified diffs and fenced code blocks for
// terminal display. Diffs get diff-semantic line colors (headers, @@ hunks,
// +/- lines, dim context) plus — when enabled and the language is recognized
// — token-level syntax highlighting of content lines via chroma. Enabled
// state follows COVO_SYNTAX_HIGHLIGHT (on by default; "0"/"false"/"off"/"no"
// disables).
package diffrender

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// ANSI SGR codes used for the diff-semantic coloring. Kept independent of
// the theme palette so the package stays leaf-level; the enclosing renderer
// wraps the result in its own styles and core.WrapAnsi preserves inner codes.
const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiGreen = "\x1b[32m"
	ansiRed   = "\x1b[31m"
	ansiCyan  = "\x1b[36m"
)

// maxSyntaxLines caps token-level highlighting: beyond this many content
// lines the diff falls back to diff-semantic colors only, so a pathological
// multi-thousand-line diff can't stall the render loop.
const maxSyntaxLines = 2000

// SyntaxEnabled reports the COVO_SYNTAX_HIGHLIGHT gate. Unset or any value
// other than the disable list means enabled.
func SyntaxEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COVO_SYNTAX_HIGHLIGHT"))) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}

// Colorize colorizes a unified diff. When syntax is true and the language is
// recognized from the diff's +++/--- filename header, content lines are
// token-highlighted; otherwise only diff-semantic colors are applied.
func Colorize(diffText string, syntax bool) string {
	if diffText == "" {
		return ""
	}
	lines := strings.Split(diffText, "\n")

	filename := diffFilename(lines)
	lexer := lexers.Match(filename)
	if lexer == nil {
		syntax = false
	}
	if syntax {
		if n := countContentLines(lines); n > maxSyntaxLines {
			syntax = false
		}
	}

	var b strings.Builder
	for _, line := range lines {
		b.WriteString(colorizeLine(line, lexer, syntax))
		b.WriteByte('\n')
	}
	// The input's trailing newline produced an extra empty last element;
	// drop the terminator we added for it.
	out := b.String()
	return strings.TrimSuffix(out, "\n")
}

// colorizeLine renders one diff line with the right combination of
// diff-semantic color and (optional) syntax highlighting.
func colorizeLine(line string, lexer chroma.Lexer, syntax bool) string {
	switch {
	case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		return ansiBold + ansiCyan + line + ansiReset
	case strings.HasPrefix(line, "@@"):
		return ansiCyan + line + ansiReset
	case strings.HasPrefix(line, "+"):
		return colorContent(ansiGreen, "+", strings.TrimPrefix(line, "+"), lexer, syntax)
	case strings.HasPrefix(line, "-"):
		return colorContent(ansiRed, "-", strings.TrimPrefix(line, "-"), lexer, syntax)
	case strings.HasPrefix(line, `\`): // "\ No newline at end of file"
		return ansiDim + line + ansiReset
	case line == "" || strings.HasPrefix(line, " "):
		content := strings.TrimPrefix(line, " ")
		if syntax && lexer != nil && content != "" {
			return " " + highlight(content, lexer)
		}
		if line == "" {
			return ""
		}
		return ansiDim + line + ansiReset
	default:
		return line
	}
}

// colorContent combines the diff marker color with optional syntax
// highlighting of the line's code content.
func colorContent(markerColor, marker, content string, lexer chroma.Lexer, syntax bool) string {
	body := content
	if syntax && lexer != nil && content != "" {
		body = highlight(content, lexer)
	}
	if body == "" {
		return markerColor + marker + ansiReset
	}
	return markerColor + marker + body + ansiReset
}

// diffFilename extracts the filename from the diff's ---/+++ headers so the
// language can be inferred from its extension. Returns "" when absent.
func diffFilename(lines []string) string {
	for i, line := range lines {
		var name string
		if strings.HasPrefix(line, "--- ") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "--- "))
		} else if strings.HasPrefix(line, "+++ ") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
		} else {
			continue
		}
		name = strings.TrimPrefix(name, "a/")
		name = strings.TrimPrefix(name, "b/")
		if name != "" && name != "/dev/null" {
			return name
		}
		if i > 20 {
			break
		}
	}
	return ""
}

func countContentLines(lines []string) int {
	n := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, " ") {
			n++
		}
	}
	return n
}

// --- fenced code block highlighting (used by the mdstream renderer) ---

// HighlightCode syntax-highlights a fenced code block's source. langName is
// the fence info string (e.g. "python"); empty or unknown languages return
// the source unchanged. When syntax is false the source is returned as-is.
func HighlightCode(source, langName string, syntax bool) string {
	if !syntax || strings.TrimSpace(langName) == "" {
		return source
	}
	lexer := lexers.Get(strings.TrimSpace(langName))
	if lexer == nil {
		return source
	}
	return highlight(source, lexer)
}

// highlight runs chroma over source with the process-wide formatter/style
// pair. Returns the source unchanged when tokenization fails.
func highlight(source string, lexer chroma.Lexer) string {
	// nil options: chroma substitutes defaults (State "root"); an explicit
	// zero-value TokeniseOptions would push an empty initial state and panic.
	it, err := lexer.Tokenise(nil, source)
	if err != nil {
		return source
	}
	var b strings.Builder
	if err := formatter().Format(&b, style(), it); err != nil {
		return source
	}
	// Multi-line sources keep their newlines; trim a single trailing one so
	// callers can manage line breaks themselves.
	return strings.TrimSuffix(b.String(), "\n")
}

var (
	fmtOnce       sync.Once
	fmtInst       chroma.Formatter
	styleOnce     sync.Once
	styleInst     *chroma.Style
	styleOverride atomic.Pointer[chroma.Style]
)

// themeChromaStyles maps covo-agent theme names to chroma style names.
// Themes without a close chroma equivalent fall back by background at
// registration time; unmapped names here use the light/dark defaults.
var themeChromaStyles = map[string]string{
	"ayu-dark":         "onedark",
	"ayu-light":        "friendly",
	"catppuccin-latte": "catppuccin-latte",
	"catppuccin-mocha": "catppuccin-mocha",
	"dracula":          "dracula",
	"everforest-dark":  "evergarden",
	"everforest-light": "friendly",
	"gruvbox-dark":     "gruvbox",
	"gruvbox-light":    "gruvbox-light",
	"material-darker":  "onedark",
	"material-light":   "friendly",
	"monokai":          "monokai",
	"nord":             "nord",
	"one-dark":         "onedark",
	"one-light":        "friendly",
	"rose-pine":        "rose-pine",
	"rose-pine-dawn":   "rose-pine-dawn",
	"solarized-dark":   "solarized-dark",
	"solarized-light":  "solarized-light",
	"synthwave-84":     "monokai",
	"tokyo-night":      "tokyonight-night",
}

// SetThemeStyle makes syntax highlighting follow the active covo-agent theme.
// Call whenever the theme changes (startup skin resolution, /theme set).
// Unknown or empty names clear the override so the background-appropriate
// default applies.
func SetThemeStyle(themeName string) {
	name := themeChromaStyles[strings.ToLower(strings.TrimSpace(themeName))]
	if name == "" {
		styleOverride.Store(nil)
		return
	}
	if s := styles.Get(name); s != nil {
		styleOverride.Store(s)
	} else {
		styleOverride.Store(nil)
	}
}

// formatter picks the chroma terminal formatter matching the terminal's
// color capability (truecolor > 256-color), defaulting to 256.
func formatter() chroma.Formatter {
	fmtOnce.Do(func() {
		name := "terminal256"
		switch strings.ToLower(os.Getenv("COLORTERM")) {
		case "truecolor", "24bit":
			name = "terminal16m"
		}
		f := formatters.Get(name)
		if f == nil {
			f = formatters.Fallback
		}
		fmtInst = f
	})
	return fmtInst
}

// style picks the chroma style: the theme-following override when set,
// otherwise a background-appropriate default.
func style() *chroma.Style {
	if s := styleOverride.Load(); s != nil {
		return s
	}
	styleOnce.Do(func() {
		name := "monokai"
		if isLightBackground() {
			name = "friendly"
		}
		s := styles.Get(name)
		if s == nil {
			s = styles.Fallback
		}
		styleInst = s
	})
	return styleInst
}

// isLightBackground mirrors covonaut's COLORFGBG heuristic without importing
// the TUI package (kept here so diffrender stays leaf-level).
func isLightBackground() bool {
	fgbg := os.Getenv("COLORFGBG")
	parts := strings.Split(fgbg, ";")
	if len(parts) < 2 {
		return false
	}
	bg, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	return bg >= 8
}
