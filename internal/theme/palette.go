package theme

import (
	"fmt"

	covotheme "github.com/covoyage/covonaut/tui/theme"
)

// Palette defines the core colors for a theme preset.
type Palette struct {
	Name string
	Dark bool

	Accent  string
	Success string
	Error   string
	Warning string

	Text  string
	Muted string
	Dim   string

	Border      string
	BorderMuted string

	UserMessage   string
	AssistantText string
	System        string
	ThinkingText  string

	SelectedBg    string
	UserMessageBg string
	ToolSuccessBg string
	ToolErrorBg   string

	MdHeading string
	MdLink    string
	MdCode    string

	SyntaxComment  string
	SyntaxKeyword  string
	SyntaxFunction string
	SyntaxString   string
	SyntaxNumber   string
	SyntaxType     string
}

// ToSemantic converts a Palette to a full SemanticTheme,
// deriving missing fields from core colors.
func (p *Palette) ToSemantic() *covotheme.SemanticTheme {
	accent := p.Accent
	border := p.Border
	if border == "" {
		border = accent
	}

	return &covotheme.SemanticTheme{
		Name: p.Name,

		Accent:       accent,
		Border:       border,
		BorderAccent: orFallback(accent, p.Border, "#606060"),
		BorderMuted:  orFallback(p.BorderMuted, p.Muted, "#606070"),
		Success:      p.Success,
		Error:        p.Error,
		Warning:      p.Warning,
		Muted:        p.Muted,
		Dim:          orFallback(p.Dim, p.Muted),
		Text:         p.Text,
		System:       orFallback(p.System, accent),
		ThinkingText: orFallback(p.ThinkingText, p.Muted),

		UserMessage:   orFallback(p.UserMessage, accent),
		AssistantText: orFallback(p.AssistantText, p.Text),

		SelectedBg:    orFallback(p.SelectedBg, lighten(p.Text, 0.85)),
		UserMessageBg: orFallback(p.UserMessageBg, lighten(accent, 0.85)),
		ToolPendingBg: orFallback(p.UserMessageBg, lighten(p.Text, 0.85)),
		ToolSuccessBg: orFallback(p.ToolSuccessBg, lighten(p.Success, 0.85)),
		ToolErrorBg:   orFallback(p.ToolErrorBg, lighten(p.Error, 0.85)),

		MdHeading:         orFallback(p.MdHeading, accent),
		MdLink:            orFallback(p.MdLink, p.SyntaxFunction),
		MdLinkUrl:         p.Muted,
		MdCode:            orFallback(p.MdCode, p.SyntaxString),
		MdCodeBlock:       orFallback(p.MdCode, p.SyntaxString),
		MdCodeBlockBorder: p.BorderMuted,
		MdQuote:           p.Muted,
		MdQuoteBorder:     accent,
		MdHr:              p.BorderMuted,
		MdListBullet:      accent,

		SyntaxComment:     orFallback(p.SyntaxComment, p.Muted),
		SyntaxKeyword:     orFallback(p.SyntaxKeyword, accent),
		SyntaxFunction:    orFallback(p.SyntaxFunction, p.Accent),
		SyntaxVariable:    p.SyntaxString,
		SyntaxString:      orFallback(p.SyntaxString, p.Success),
		SyntaxNumber:      orFallback(p.SyntaxNumber, p.Warning),
		SyntaxType:        orFallback(p.SyntaxType, p.SyntaxKeyword),
		SyntaxOperator:    p.Text,
		SyntaxPunctuation: p.Muted,

		LoaderSpinner: accent,
		ProgressBar:   accent,
	}
}

func orFallback(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func lighten(hex string, factor float64) string {
	if hex == "" || len(hex) < 7 {
		return ""
	}
	r := hexPair(hex[1:3])
	g := hexPair(hex[3:5])
	b := hexPair(hex[5:7])
	r = int(float64(r) + (255-float64(r))*factor)
	g = int(float64(g) + (255-float64(g))*factor)
	b = int(float64(b) + (255-float64(b))*factor)
	return fmtHex(r, g, b)
}

func hexPair(s string) int {
	var v int
	fmt.Sscanf(s, "%x", &v)
	return v
}

func fmtHex(r, g, b int) string {
	return fmt.Sprintf("#%02x%02x%02x", clamp(r), clamp(g), clamp(b))
}

func clamp(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}
