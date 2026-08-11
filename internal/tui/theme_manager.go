package tui

// ---------------------------------------------------------------------------
// 多主题系统。
//
// covo-agent 已有 internal/theme/palette.go 定义了 Palette 结构。
// 此模块在 TUI 层提供主题注册、切换、列表功能，
// 让用户可以通过 /theme 命令切换预设主题。
//
// 架构：
//   - ThemePreset：主题预设（名称 + Palette 构建）
//   - ThemeManager：注册预设，切换活跃主题
// ---------------------------------------------------------------------------

import (
	"sync"

	covotheme "github.com/covoyage/covonaut/tui/theme"
)

// ThemePreset 描述一个可切换的主题预设。
type ThemePreset struct {
	Name        string
	Description string
	Dark        bool
	// Semantic 返回该预设的语义主题（颜色定义）。
	Semantic func() *covotheme.SemanticTheme
	// Mode 返回颜色模式。
	Mode func() covotheme.ColorMode
}

// ThemeManager 管理可切换的主题列表。
type ThemeManager struct {
	mu      sync.RWMutex
	presets []ThemePreset
	active  string
}

// NewThemeManager 创建带内置预设的管理器。
func NewThemeManager() *ThemeManager {
	tm := &ThemeManager{
		active: "dark",
	}
	// 注册内置预设
	tm.Register(ThemePreset{
		Name:        "dark",
		Description: "Default dark theme",
		Dark:        true,
		Semantic:   covotheme.DefaultSemanticDark,
		Mode:        func() covotheme.ColorMode { return covotheme.DetectColorMode() },
	})
	tm.Register(ThemePreset{
		Name:        "light",
		Description: "Light theme",
		Dark:        false,
		Semantic:   covotheme.DefaultSemanticLight,
		Mode:        func() covotheme.ColorMode { return covotheme.DetectColorMode() },
	})
	return tm
}

// Register 注册一个主题预设。
func (tm *ThemeManager) Register(preset ThemePreset) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	// 避免重复
	for i, p := range tm.presets {
		if p.Name == preset.Name {
			tm.presets[i] = preset
			return
		}
	}
	tm.presets = append(tm.presets, preset)
}

// List 返回所有已注册的主题预设。
func (tm *ThemeManager) List() []ThemePreset {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	out := make([]ThemePreset, len(tm.presets))
	copy(out, tm.presets)
	return out
}

// Active 返回当前活跃主题名。
func (tm *ThemeManager) Active() string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.active
}

// Apply 切换到指定主题。返回是否成功。
func (tm *ThemeManager) Apply(name string) bool {
	tm.mu.Lock()
	for _, p := range tm.presets {
		if p.Name == name {
			tm.active = name
			preset := p
			tm.mu.Unlock()
			if preset.Semantic != nil && preset.Mode != nil {
				covotheme.SyncPaletteGlobals(preset.Semantic(), preset.Mode())
			}
			return true
		}
	}
	tm.mu.Unlock()
	return false
}

// ApplyNext 切换到下一个主题（循环）。返回主题名。
func (tm *ThemeManager) ApplyNext() string {
	tm.mu.Lock()
	if len(tm.presets) == 0 {
		tm.mu.Unlock()
		return ""
	}
	idx := 0
	for i, p := range tm.presets {
		if p.Name == tm.active {
			idx = i
			break
		}
	}
	next := tm.presets[(idx+1)%len(tm.presets)]
	tm.active = next.Name
	preset := next
	tm.mu.Unlock()
	if preset.Semantic != nil && preset.Mode != nil {
		covotheme.SyncPaletteGlobals(preset.Semantic(), preset.Mode())
	}
	return next.Name
}
