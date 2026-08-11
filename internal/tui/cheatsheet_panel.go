package tui

import (
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/theme"
)

// CheatsheetPanel 显示 ActionRegistry.RenderCheatsheet 生成的快捷键帮助文本。
type CheatsheetPanel struct {
	lines []string
}

// NewCheatsheetPanel 从预渲染的行列表创建面板。
func NewCheatsheetPanel(lines []string) *CheatsheetPanel {
	return &CheatsheetPanel{lines: lines}
}

func (c *CheatsheetPanel) Invalidate() {}

func (c *CheatsheetPanel) Render(width int64) []string {
	pal := theme.CurrentPalette()
	out := make([]string, 0, len(c.lines)+2)
	out = append(out, pal.Accent.Render("  ── Keyboard Shortcuts ──"))
	for _, line := range c.lines {
		out = append(out, line)
	}
	out = append(out, pal.Dim.Render("  Esc to close"))
	return out
}

var _ core.Component = (*CheatsheetPanel)(nil)
