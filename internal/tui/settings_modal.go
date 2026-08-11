package tui

import (
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/component"
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// SettingsModal — 设置弹窗。
//
// 包装 component.SettingsList，添加标题栏和操作提示。
// 通过 F2 快捷键或 /settings 命令打开。
// ---------------------------------------------------------------------------

// SettingsModalMode 标识设置弹窗的交互模式。
type SettingsModalMode int

const (
	SettingsModeBrowse SettingsModalMode = iota
	SettingsModeFilter
)

// SettingsModal 是设置弹窗组件。
type SettingsModal struct {
	list        *component.SettingsList
	title       string
	width       int64
	mode        SettingsModalMode
	filterQuery string
	allEntries  []component.SettingEntry // 原始全量条目
}

// NewSettingsModal 创建设置弹窗。
// entries 是设置条目列表。
func NewSettingsModal(entries []component.SettingEntry) *SettingsModal {
	list := component.NewSettingsList(entries)
	list.SetMaxVisible(15)
	list.SetFocused(true)
	return &SettingsModal{
		list:       list,
		title:      "Settings",
		width:      60,
		mode:       SettingsModeBrowse,
		allEntries: entries,
	}
}

// Render 实现 core.Component。
func (sm *SettingsModal) Render(width int64) []string {
	if width > sm.width {
		width = sm.width
	}
	pal := theme.CurrentPalette()

	var lines []string
	// Title bar
	titleSuffix := ""
	if sm.mode == SettingsModeFilter {
		titleSuffix = pal.Accent.Render(fmt.Sprintf("  /%s", sm.filterQuery))
	}
	lines = append(lines, pal.Accent.Render(fmt.Sprintf("┃ %s", sm.title))+titleSuffix)
	lines = append(lines, pal.Dim.Render(strings.Repeat("─", int(width)-2)))

	// Settings list
	listLines := sm.list.Render(width)
	lines = append(lines, listLines...)

	// Footer
	lines = append(lines, pal.Dim.Render(strings.Repeat("─", int(width)-2)))
	if sm.mode == SettingsModeFilter {
		lines = append(lines, pal.Dim.Render("  type to filter · Esc exit filter"))
	} else {
		lines = append(lines, pal.Dim.Render("  ↑/↓ select · ←/→ change · Enter cycle · / filter · Esc close"))
	}

	return lines
}

// Invalidate 实现 core.Component。
func (sm *SettingsModal) Invalidate() {
	sm.list.Invalidate()
}

// Update 实现 core.Updatable，把键盘事件转发给内部 SettingsList。
// 支持 / 进入过滤模式，ESC 退出过滤模式。
func (sm *SettingsModal) Update(msg core.Msg) core.Cmd {
	if key, ok := msg.(core.KeyMsg); ok {
		data := key.Data

		// Filter 模式下的键盘处理
		if sm.mode == SettingsModeFilter {
			if isEscape(data) {
				sm.exitFilter()
				return nil
			}
			// 退格
			if data == "backspace" || data == "\x7f" {
				if len(sm.filterQuery) > 0 {
					sm.filterQuery = sm.filterQuery[:len(sm.filterQuery)-1]
					sm.applyFilter()
				}
				return nil
			}
			// 普通字符追加到过滤查询
			if isPrintableKey(data) {
				sm.filterQuery += data
				sm.applyFilter()
				return nil
			}
			return nil
		}

		// Browse 模式：/ 进入过滤
		if data == "/" {
				sm.mode = SettingsModeFilter
			sm.filterQuery = ""
			return nil
		}
	}
	return sm.list.Update(msg)
}

// List 返回内部 SettingsList（用于设置回调等）。
func (sm *SettingsModal) List() *component.SettingsList {
	return sm.list
}

// exitFilter 退出过滤模式，恢复全量列表。
func (sm *SettingsModal) exitFilter() {
	sm.mode = SettingsModeBrowse
	sm.filterQuery = ""
	sm.list = component.NewSettingsList(sm.allEntries)
	sm4 := sm.list
	sm4.SetMaxVisible(15)
	sm4.SetFocused(true)
}

// applyFilter 根据过滤查询更新设置列表。
func (sm *SettingsModal) applyFilter() {
	if sm.filterQuery == "" {
		newList := component.NewSettingsList(sm.allEntries)
		newList.SetMaxVisible(15)
		newList.SetFocused(true)
		sm.list = newList
		return
	}
	var filtered []component.SettingEntry
	for _, e := range sm.allEntries {
		if strings.Contains(strings.ToLower(e.Label), strings.ToLower(sm.filterQuery)) ||
			strings.Contains(strings.ToLower(e.Key), strings.ToLower(sm.filterQuery)) ||
			strings.Contains(strings.ToLower(e.Description), strings.ToLower(sm.filterQuery)) {
			filtered = append(filtered, e)
		}
	}
	newList := component.NewSettingsList(filtered)
	newList.SetMaxVisible(15)
	newList.SetFocused(true)
	sm.list = newList
}

// isPrintableKey 判断是否是可打印字符键。
func isPrintableKey(data string) bool {
	if len(data) == 0 {
		return false
	}
	// 排除控制字符和特殊键
	switch data {
	case "enter", "tab", "esc", "up", "down", "left", "right",
		"backspace", "delete", "home", "end", "pageup", "pagedown",
		"space", "ctrl+c", "ctrl+q", "ctrl+d":
		return false
	}
	// 单字符且非控制字符
	if len(data) == 1 && data[0] >= 0x20 && data[0] < 0x7f {
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// ShowSettingsModal — 通过 UIBus 显示设置弹窗。
// ---------------------------------------------------------------------------

// ShowSettingsModal 创建并显示设置弹窗。
// onChange 在设置项被修改时调用（可选）。
// 返回 overlay 引用，调用方可用 ClosePanel 关闭。
func ShowSettingsModal(bus *UIBus, entries []component.SettingEntry, onChange func(component.SettingEntry)) chat.OverlayRef {
	if bus == nil || bus.Host() == nil {
		return nil
	}
	modal := NewSettingsModal(entries)
	if onChange != nil {
		modal.list.OnChange(onChange)
	}
	// 使用 ModalWindow chrome 包装
	return ShowModalWindow(bus, modal, ModalWindowConfig{
		Title:     "Settings",
		Shortcuts: []ModalShortcut{
			{Keys: "↑/↓", Label: "select"},
			{Keys: "←/→", Label: "change"},
			{Keys: "/", Label: "filter"},
			{Keys: "Esc", Label: "close"},
		},
		WidthPct:  60,
		HeightPct: 70,
	})
}

// BuildDefaultSettingsEntries 构建默认设置条目列表。
// 调用方可根据实际配置填充 Current 值。
func BuildDefaultSettingsEntries() []component.SettingEntry {
	return []component.SettingEntry{
		{
			Key:   "theme",
			Label: "Theme",
			Options: []component.SettingOption{
				{Value: "dark", Label: "Dark"},
				{Value: "light", Label: "Light"},
				{Value: "system", Label: "System"},
			},
			Current:     0,
			Description: "color scheme",
		},
		{
			Key:   "mode",
			Label: "Agent Mode",
			Options: []component.SettingOption{
				{Value: "code", Label: "Code"},
				{Value: "general", Label: "General"},
			},
			Current:     0,
			Description: "agent behavior",
		},
		{
			Key:   "thinking",
			Label: "Show Thinking",
			Options: []component.SettingOption{
				{Value: "off", Label: "Off"},
				{Value: "compact", Label: "Compact"},
				{Value: "full", Label: "Full"},
			},
			Current:     0,
			Description: "reasoning display",
		},
		{
			Key:   "yolo",
			Label: "Auto-approve",
			Options: []component.SettingOption{
				{Value: "off", Label: "Off"},
				{Value: "on", Label: "On"},
			},
			Current:     0,
			Description: "skip approval prompts",
		},
		{
			Key:   "diff",
			Label: "Diff Preview",
			Options: []component.SettingOption{
				{Value: "off", Label: "Off"},
				{Value: "inline", Label: "Inline"},
				{Value: "overlay", Label: "Overlay"},
			},
			Current:     1,
			Description: "pre-edit diff display",
		},
	}
}

// Ensure SettingsModal implements core.Component and core.Updatable.
var _ core.Component = (*SettingsModal)(nil)
var _ core.Updatable = (*SettingsModal)(nil)
