package tui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// Shortcuts Bar — 上下文感知的动态快捷键提示栏。
//
// 根据当前上下文（prompt focused / scrollback focused / search active /
// modal open）动态构建快捷键提示。
// ---------------------------------------------------------------------------

// ShortcutContext 标识当前 UI 上下文。
type ShortcutContext int

const (
	ShortcutCtxPrompt ShortcutContext = iota
	ShortcutCtxScrollback
	ShortcutCtxSearch
	ShortcutCtxModal
	ShortcutCtxBusy
)

// ShortcutHint 是一个快捷键提示项。
type ShortcutHint struct {
	Keys   string // 显示的按键（如 "j/k", "Ctrl+P"）
	Label  string // 简短标签（如 "nav", "model"）
	Pinned bool   // 是否固定显示（不被截断）
}

// ShortcutsBar 管理动态快捷键提示。
type ShortcutsBar struct {
	mu      sync.Mutex
	context ShortcutContext
	custom  []ShortcutHint // 自定义提示（覆盖默认）
}

// NewShortcutsBar 创建提示栏。
func NewShortcutsBar() *ShortcutsBar {
	return &ShortcutsBar{}
}

// SetContext 设置当前上下文。
func (sb *ShortcutsBar) SetContext(ctx ShortcutContext) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.context = ctx
}

// SetCustom 设置自定义提示（覆盖默认）。
func (sb *ShortcutsBar) SetCustom(hints []ShortcutHint) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.custom = hints
}

// Hints 返回当前上下文的快捷键提示列表。
func (sb *ShortcutsBar) Hints() []ShortcutHint {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	if len(sb.custom) > 0 {
		return sb.custom
	}
	return defaultHints(sb.context)
}

// Render 渲染快捷键提示栏。
func (sb *ShortcutsBar) Render(width int, pal *theme.Palette) string {
	hints := sb.Hints()
	if len(hints) == 0 {
		return ""
	}

	// 构建提示段
	var segments []string
	totalWidth := 0
	for _, h := range hints {
		seg := fmt.Sprintf("%s %s", pal.Accent.Render(h.Keys), pal.Dim.Render(h.Label))
		segments = append(segments, seg)
		// 粗略计算宽度
		totalWidth += len(h.Keys) + len(h.Label) + 3
	}

	line := strings.Join(segments, pal.Dim.Render(" · "))

	// 如果超出宽度，优先保留 pinned 项
	if totalWidth > width {
		line = truncateHints(hints, width, pal)
	}

	return line
}

// defaultHints 返回给定上下文的默认快捷键提示。
func defaultHints(ctx ShortcutContext) []ShortcutHint {
	switch ctx {
	case ShortcutCtxPrompt:
		return []ShortcutHint{
			{Keys: "Enter", Label: "send", Pinned: true},
			{Keys: "↑/↓", Label: "history", Pinned: false},
			{Keys: "Ctrl+P", Label: "model", Pinned: true},
			{Keys: "F2", Label: "settings", Pinned: false},
			{Keys: "Ctrl+Q", Label: "quit", Pinned: true},
		}
	case ShortcutCtxScrollback:
		return []ShortcutHint{
			{Keys: "j/k", Label: "nav", Pinned: true},
			{Keys: "Space", Label: "prompt", Pinned: true},
			{Keys: "/", Label: "search", Pinned: false},
			{Keys: "y", Label: "copy", Pinned: false},
			{Keys: "Ctrl+Q", Label: "quit", Pinned: true},
		}
	case ShortcutCtxSearch:
		return []ShortcutHint{
			{Keys: "n/N", Label: "next/prev", Pinned: true},
			{Keys: "Enter", Label: "jump", Pinned: true},
			{Keys: "Esc", Label: "close", Pinned: true},
		}
	case ShortcutCtxModal:
		return []ShortcutHint{
			{Keys: "↑/↓", Label: "select", Pinned: true},
			{Keys: "Enter", Label: "confirm", Pinned: true},
			{Keys: "Esc", Label: "close", Pinned: true},
		}
	case ShortcutCtxBusy:
		return []ShortcutHint{
			{Keys: "Ctrl+C", Label: "cancel", Pinned: true},
			{Keys: "Ctrl+Q", Label: "quit", Pinned: true},
		}
	}
	return nil
}

// truncateHints 截断提示列表以适应宽度，优先保留 pinned 项。
func truncateHints(hints []ShortcutHint, width int, pal *theme.Palette) string {
	var pinned, rest []ShortcutHint
	for _, h := range hints {
		if h.Pinned {
			pinned = append(pinned, h)
		} else {
			rest = append(rest, h)
		}
	}

	// 先放 pinned，再尽可能放 rest
	var segments []string
	for _, h := range pinned {
		seg := fmt.Sprintf("%s %s", pal.Accent.Render(h.Keys), pal.Dim.Render(h.Label))
		segments = append(segments, seg)
	}
	for _, h := range rest {
		seg := fmt.Sprintf("%s %s", pal.Accent.Render(h.Keys), pal.Dim.Render(h.Label))
		segments = append(segments, seg)
	}

	return strings.Join(segments, pal.Dim.Render(" · "))
}
