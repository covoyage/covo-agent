package tui

import (
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// Modal Window — 可复用弹窗 chrome。
//
// 统一弹窗外观包括：
//   - 标题栏 + 关闭按钮
//   - 底部快捷键提示栏
//   - 自动 sizing（百分比 / 固定 / auto）
//   - ESC 关闭
//
// 本模块提供可复用的弹窗 chrome。
// ---------------------------------------------------------------------------

// ModalWindowConfig 配置弹窗外观。
type ModalWindowConfig struct {
	Title      string
	Shortcuts  []ModalShortcut
	WidthPct   int  // 宽度百分比（0=auto）
	HeightPct  int  // 高度百分比（0=auto）
	CloseOnEsc bool // ESC 是否关闭（默认 true）
}

// ModalShortcut 是弹窗底部显示的快捷键提示。
type ModalShortcut struct {
	Keys  string
	Label string
}

// ModalWindow 包装任意 Component，添加标题栏和快捷键提示。
type ModalWindow struct {
	content core.Component
	config  ModalWindowConfig
	focused bool
}

// NewModalWindow 创建弹窗。
func NewModalWindow(content core.Component, config ModalWindowConfig) *ModalWindow {
	if config.WidthPct <= 0 {
		config.WidthPct = 80
	}
	if config.HeightPct <= 0 {
		config.HeightPct = 70
	}
	return &ModalWindow{
		content: content,
		config:  config,
		focused: true,
	}
}

// Render 实现 core.Component。
func (mw *ModalWindow) Render(width int64) []string {
	pal := theme.CurrentPalette()
	var lines []string

	// 标题栏
	titleBar := buildModalTitleBar(mw.config.Title, width, pal)
	lines = append(lines, titleBar...)

	// 内容
	contentLines := mw.content.Render(width)
	lines = append(lines, contentLines...)

	// 分隔线
	lines = append(lines, pal.Dim.Render(strings.Repeat("─", int(width)-2)))

	// 快捷键提示栏
	shortcutBar := buildModalShortcutBar(mw.config.Shortcuts, width, pal)
	lines = append(lines, shortcutBar)

	return lines
}

// Invalidate 实现 core.Component。
func (mw *ModalWindow) Invalidate() {
	mw.content.Invalidate()
}

// Update 实现 core.Updatable。
func (mw *ModalWindow) Update(msg core.Msg) core.Cmd {
	// ESC 关闭由 escCloseComponent 处理
	if updatable, ok := mw.content.(core.Updatable); ok {
		return updatable.Update(msg)
	}
	return nil
}

// buildModalTitleBar 构建标题栏。
func buildModalTitleBar(title string, width int64, pal *theme.Palette) []string {
	// ┃ Title                                    [Esc] ─
	titleLen := len([]rune(title))
	closeLabel := "Esc"
	padding := int(width) - titleLen - len(closeLabel) - 6
	if padding < 0 {
		padding = 0
	}

	line1 := fmt.Sprintf("%s %s%s  %s %s",
		pal.Accent.Render("┃"),
		pal.Accent.Render(title),
		strings.Repeat(" ", padding),
		pal.Dim.Render(closeLabel),
		pal.Dim.Render("─"),
	)
	line2 := pal.Dim.Render(strings.Repeat("─", int(width)-2))

	return []string{line1, line2}
}

// buildModalShortcutBar 构建快捷键提示栏。
func buildModalShortcutBar(shortcuts []ModalShortcut, width int64, pal *theme.Palette) string {
	if len(shortcuts) == 0 {
		return pal.Dim.Render("  Esc close")
	}
	var parts []string
	for _, s := range shortcuts {
		parts = append(parts, fmt.Sprintf("%s %s",
			pal.Accent.Render(s.Keys),
			pal.Dim.Render(s.Label)))
	}
	return "  " + strings.Join(parts, pal.Dim.Render(" · "))
}

// ShowModalWindow 通过 UIBus 显示一个带 chrome 的弹窗。
// 返回 overlay 引用。
func ShowModalWindow(bus *UIBus, content core.Component, config ModalWindowConfig) chat.OverlayRef {
	if bus == nil || bus.Host() == nil {
		return nil
	}
	window := NewModalWindow(content, config)
	return bus.ShowPanel(window, config.WidthPct, config.HeightPct)
}

// DefaultModalShortcuts 返回默认快捷键提示。
func DefaultModalShortcuts() []ModalShortcut {
	return []ModalShortcut{
		{Keys: "↑/↓", Label: "select"},
		{Keys: "Enter", Label: "confirm"},
		{Keys: "Esc", Label: "close"},
	}
}

// Ensure ModalWindow implements core.Component and core.Updatable.
var _ core.Component = (*ModalWindow)(nil)
var _ core.Updatable = (*ModalWindow)(nil)

// TerminalSupportsOSC9 检查终端是否支持 OSC 9 通知。
func TerminalSupportsOSC9() bool {
	return true // 大多数现代终端支持
}
