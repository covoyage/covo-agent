package tui

import (
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/tui/core"

	"github.com/covoyage/covo-agent/internal/i18n"
)

// WelcomeInfo contains the presentation data shown in the initial session card.
type WelcomeInfo struct {
	Provider   string
	Model      string
	Mode       string
	WorkingDir string
	ToolCount  int
	SkillCount int
}

// BuildWelcomeMessage renders the initial assistant welcome message.
func BuildWelcomeMessage(info WelcomeInfo) string {
	lines := []string{
		i18n.T("app.welcome_intro"),
		"",
		"```",
		"   ______ ____ _    _ ____        _    ____ _____ _   _ _____",
		"  / ____/ __ \\ |  / / __ \\      / \\  / ___| ____| \\ | |_   _|",
		" | |   | |  | | | / / |  | |____/ _ \\| |  _|  _| |  \\| | | |",
		" | |___| |__| | |/ /| |__| |___/ ___ \\ |_| | |___| |\\  | | |",
		"  \\_____\\____/|___/  \\____/   /_/   \\_\\____|_____|_| \\_| |_|",
		"",
	}
	lines = append(lines, sessionCardLines(info)...)
	lines = append(lines,
		"```",
		"",
		i18n.T("app.welcome_type"),
		i18n.T("app.welcome_model"),
	)
	return strings.Join(lines, "\n")
}

func sessionCardLines(info WelcomeInfo) []string {
	rows := [][2]string{
		{"Provider", info.Provider},
		{"Model", info.Model},
		{"Mode", info.Mode},
		{"Workspace", info.WorkingDir},
		{"Capabilities", fmt.Sprintf("%d tools · %d skills", info.ToolCount, info.SkillCount)},
	}

	const width = 74
	lines := []string{
		"+" + strings.Repeat("-", width-2) + "+",
		padBoxLine(centerText(i18n.T("app.welcome_session"), width-4), width),
		"+" + strings.Repeat("-", width-2) + "+",
	}
	for _, row := range rows {
		value := core.TruncateToWidth(row[1], int64(width-18), "…")
		lines = append(lines, padBoxLine(fmt.Sprintf("%-12s %s", row[0]+":", value), width))
	}
	return append(lines,
		"+"+strings.Repeat("-", width-2)+"+",
		padBoxLine(i18n.T("app.welcome_shortcuts"), width),
		"+"+strings.Repeat("-", width-2)+"+",
	)
}

func padBoxLine(content string, width int) string {
	inner := width - 4
	content = core.TruncateToWidth(content, int64(inner), "…")
	return "| " + core.PadToWidth(content, int64(inner)) + " |"
}

func centerText(text string, width int) string {
	textWidth := int(core.VisibleWidth(text))
	if textWidth >= width {
		return text
	}
	left := (width - textWidth) / 2
	right := width - textWidth - left
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
}
