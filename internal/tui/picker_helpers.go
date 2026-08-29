package tui

import (
	"strings"

	"github.com/covoyage/covonaut/tui/core"
)

// ---------------------------------------------------------------------------
// Shared picker rendering helpers.
//
// 这些函数最初定义在 panels/model_picker.go 中，提取到此处
// 供 picker.go 和 panels/ 包共享使用。
// panels/model_picker.go 中的同名函数将删除以避免冲突。
// ---------------------------------------------------------------------------

// renderPickerPanel 渲染带边框的选择面板。
func renderPickerPanel(body []string, width int64) []string {
	if width < 4 {
		width = 4
	}
	border := "+" + strings.Repeat("-", int(width-2)) + "+"
	lines := []string{border}
	for _, line := range body {
		lines = append(lines, pickerPanelLine(line, width))
	}
	lines = append(lines, border)
	return lines
}

// pickerPanelLine 渲染面板内的一行（居中截断/填充）。
func pickerPanelLine(content string, width int64) string {
	inner := width - 4
	content = core.TruncateToWidth(content, inner, "…")
	return "| " + core.PadToWidth(content, inner) + " |"
}

// centerPanelLines 将面板行水平居中。
func centerPanelLines(panel []string, width int64) []string {
	leftPad := int64(0)
	if width > 0 && len(panel) > 0 {
		panelWidth := core.VisibleWidth(panel[0])
		if width > panelWidth {
			leftPad = (width - panelWidth) / 2
		}
	}
	prefix := strings.Repeat(" ", int(leftPad))
	lines := make([]string, 0, len(panel)+2)
	lines = append(lines, core.PadToWidth("", width))
	for _, line := range panel {
		lines = append(lines, core.PadToWidth(prefix+line, width))
	}
	lines = append(lines, core.PadToWidth("", width))
	return lines
}

// wrapIndex 将索引 i 在范围 [0, n) 内做循环包裹。
func wrapIndex(i, n int) int {
	if n <= 0 {
		return 0
	}
	if i < 0 {
		return n - 1
	}
	if i >= n {
		return 0
	}
	return i
}
