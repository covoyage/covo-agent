package tui

import (
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// Context Bar — token 用量可视化进度条。
//
// 功能包括：
//   - 进度条渲染（█████░░░░░）
//   - 颜色渐变（绿→黄→红）
//   - hover 切换文本/进度条
//   - 紧凑 token 格式化（1.2K / 12K / 1.2M）
//
// 本模块用于状态栏 context-used 段的可视化。
// ---------------------------------------------------------------------------

// ContextBarConfig 控制进度条渲染参数。
type ContextBarConfig struct {
	BarWidth int  // 进度条宽度（字符数）
	ShowBar  bool // 是否显示进度条（false=纯文本）
}

// DefaultContextBarConfig 默认配置。
func DefaultContextBarConfig() ContextBarConfig {
	return ContextBarConfig{BarWidth: 10, ShowBar: true}
}

// FormatTokensCompact 将 token 数格式化为紧凑字符串（≤4 字符）。
//
//	0–999:   "0", "12", "999"
//	1K–9.9K: "1.2K"
//	10K+:    "12K", "999K"
//	1M+:     "1.2M", "12M"
func FormatTokensCompact(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 10000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000.0)
	}
	if n < 1000000 {
		return fmt.Sprintf("%dK", n/1000)
	}
	if n < 10000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000.0)
	}
	return fmt.Sprintf("%dM", n/1000000)
}

// FormatPercent5 将百分比格式化为固定 5 字符宽度。
func FormatPercent5(pct float64) string {
	if pct >= 100.0 {
		return "MAX %"
	}
	if pct < 10.0 {
		return fmt.Sprintf("%.2f%%", pct)
	}
	return fmt.Sprintf("%.1f%%", pct)
}

// RenderContextBar 渲染 context 用量进度条。
//
// used/total 是 token 用量，config 控制渲染选项。
// 返回带颜色的字符串，可直接嵌入状态栏。
func RenderContextBar(used, total int64, config ContextBarConfig, pal *theme.Palette) string {
	if total <= 0 {
		return ""
	}
	pct := float64(used) * 100.0 / float64(total)
	if pct > 100.0 {
		pct = 100.0
	}

	// 颜色根据用量选择
	style := chooseContextStyle(pct, pal)

	if !config.ShowBar {
		// 纯文本模式
		return style.Render(fmt.Sprintf("ctx: %s/%s %s",
			FormatTokensCompact(used), FormatTokensCompact(total), FormatPercent5(pct)))
	}

	// 进度条模式
	bar := renderProgressBar(pct, config.BarWidth, style, pal)
	return fmt.Sprintf("%s %s", bar, style.Render(FormatPercent5(pct)))
}

// renderProgressBar 渲染进度条字符串。
func renderProgressBar(pct float64, width int, style theme.Style, pal *theme.Palette) string {
	if width <= 0 {
		width = 10
	}
	filled := int(pct * float64(width) / 100.0)
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return style.Render(bar)
}

// chooseContextStyle 根据百分比选择颜色样式。
func chooseContextStyle(pct float64, pal *theme.Palette) theme.Style {
	switch {
	case pct < 50:
		return pal.Dim
	case pct < 80:
		return pal.Accent
	case pct < 95:
		return pal.Accent // 80-95%: accent（无 Warning 样式，用 Accent 代替）
	default:
		return pal.Error
	}
}

// ContextBarHover 切换 hover 状态，返回新的渲染。
// hover 时显示进度条，非 hover 时显示纯文本。
func ContextBarHover(used, total int64, hover bool, pal *theme.Palette) string {
	cfg := DefaultContextBarConfig()
	cfg.ShowBar = hover
	return RenderContextBar(used, total, cfg, pal)
}
