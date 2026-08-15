package tui

import (
	"strings"

	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/component"
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/theme"

	"github.com/covoyage/covo-agent/internal/i18n"
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
		width:      60,
		mode:       SettingsModeBrowse,
		allEntries: entries,
	}
}

// Render 实现 core.Component。
// 只渲染内容区（列表 + 过滤提示）；标题栏与快捷键提示由外层
// ModalWindow chrome 提供，避免重复展示。
func (sm *SettingsModal) Render(width int64) []string {
	if width > sm.width {
		width = sm.width
	}
	pal := theme.CurrentPalette()

	var lines []string
	listLines := sm.list.Render(width)
	lines = append(lines, listLines...)

	if sm.mode == SettingsModeFilter {
		lines = append(lines, pal.Dim.Render("  "+i18n.T("settings.filter_hint")))
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
					sm.filterQuery = trimLastRune(sm.filterQuery)
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

// trimLastRune 删除最后一个 Unicode 字符（按 rune 而非字节），
// 用于支持中文等多字节输入。
func trimLastRune(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return string(r[:len(r)-1])
}

// isPrintableKey 判断是否是可打印字符键。
// 支持多字节 UTF-8（如中文输入法提交的字符）。
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
	// 接受可打印字符，包括多字节 UTF-8
	for _, r := range data {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
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
		Title:     i18n.T("settings.title"),
		Shortcuts: []ModalShortcut{
			{Keys: "↑/↓", Label: i18n.T("settings.shortcut_select")},
			{Keys: "←/→", Label: i18n.T("settings.shortcut_change")},
			{Keys: "/", Label: i18n.T("settings.shortcut_filter")},
			{Keys: "Esc", Label: i18n.T("settings.shortcut_close")},
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
			Label: i18n.T("settings.label_theme"),
			Options: []component.SettingOption{
				{Value: "dark", Label: i18n.T("settings.opt_dark")},
				{Value: "light", Label: i18n.T("settings.opt_light")},
				{Value: "system", Label: i18n.T("settings.opt_system")},
			},
			Current:     0,
			Description: i18n.T("settings.desc_theme"),
		},
		{
			Key:   "mode",
			Label: i18n.T("settings.label_mode"),
			Options: []component.SettingOption{
				{Value: "code", Label: i18n.T("settings.opt_code")},
				{Value: "general", Label: i18n.T("settings.opt_general")},
			},
			Current:     0,
			Description: i18n.T("settings.desc_mode"),
		},
		{
			Key:   "thinking",
			Label: i18n.T("settings.label_thinking"),
			Options: []component.SettingOption{
				{Value: "off", Label: i18n.T("settings.opt_off")},
				{Value: "compact", Label: i18n.T("settings.opt_compact")},
				{Value: "full", Label: i18n.T("settings.opt_full")},
			},
			Current:     0,
			Description: i18n.T("settings.desc_thinking"),
		},
		{
			Key:   "yolo",
			Label: i18n.T("settings.label_yolo"),
			Options: []component.SettingOption{
				{Value: "off", Label: i18n.T("settings.opt_off")},
				{Value: "on", Label: i18n.T("settings.opt_on")},
			},
			Current:     0,
			Description: i18n.T("settings.desc_yolo"),
		},
		{
			Key:   "diff",
			Label: i18n.T("settings.label_diff"),
			Options: []component.SettingOption{
				{Value: "off", Label: i18n.T("settings.opt_off")},
				{Value: "inline", Label: i18n.T("settings.opt_inline")},
				{Value: "overlay", Label: i18n.T("settings.opt_overlay")},
			},
			Current:     1,
			Description: i18n.T("settings.desc_diff"),
		},
	}
}

// Ensure SettingsModal implements core.Component and core.Updatable.
var _ core.Component = (*SettingsModal)(nil)
var _ core.Updatable = (*SettingsModal)(nil)
