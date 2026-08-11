package tui

import (
	"sync"
)

// ---------------------------------------------------------------------------
// Block 生命周期高级特性。
//
// 扩展 covo-agent 的 Block 接口，增加：
//   - FoldableBlock：可折叠/展开的 block（3 态循环：Collapsed → Truncated → Expanded）
//   - RawModeBlock：支持 raw Markdown / 渲染后切换
//   - AccentStyle：accent 颜色 + 动画标志（运行中波浪动画）
//
// 不修改现有 Block 接口（向后兼容），而是提供可选扩展接口。
// ---------------------------------------------------------------------------

// FoldableBlock 是可选接口，支持折叠/展开。
type FoldableBlock interface {
	// IsFoldable 返回是否可折叠。
	IsFoldable() bool
	// NextFoldMode 返回下一个显示模式（3 态循环）。
	NextFoldMode(current DisplayMode, isRunning bool) DisplayMode
	// CollapseMode 返回显式折叠时的最小模式。
	CollapseMode(isRunning bool) DisplayMode
}

// RawModeBlock 是可选接口，支持 raw / 渲染后切换。
type RawModeBlock interface {
	HasRawMode() bool
	// CopyText 返回可复制的文本（raw=true 时返回原始 Markdown）。
	CopyText(raw bool) string
}

// AccentStyle 描述 accent 颜色和动画。
type AccentStyle struct {
	Color    string // ANSI 颜色码
	Animated bool   // 运行中波浪动画
}

// AccentBlock 是可选接口，返回自定义 accent 样式。
type AccentBlock interface {
	Accent() AccentStyle
	Bullet() AccentStyle
}

// SearchableBlock 是可选接口，返回搜索文本。
type SearchableBlock interface {
	SearchableText() string
}

// --- 默认折叠行为 ---

// DefaultFoldMode 返回 block 的默认折叠模式。
// 可折叠块默认展开；不可折叠块也返回 Normal。
func DefaultFoldMode(block Block) DisplayMode {
	if fb, ok := block.(FoldableBlock); ok && fb.IsFoldable() {
		// 可折叠块：询问其折叠时使用的最小模式（如运行中可能不同）
		return fb.CollapseMode(false)
	}
	return DisplayNormal
}

// NextDisplayMode 实现标准的 3 态折叠循环。
// Collapsed → Expanded → Truncated → Collapsed → ...
func NextDisplayMode(current DisplayMode) DisplayMode {
	switch current {
	case DisplayNormal:
		return DisplayCollapsed
	case DisplayCollapsed:
		return DisplayExpanded
	case DisplayExpanded:
		return DisplayNormal
	default:
		return DisplayNormal
	}
}

// --- Block 折叠管理器 ---

// BlockFoldManager 管理每个 entry 的折叠状态。
type BlockFoldManager struct {
	mu     sync.RWMutex
	states map[EntryID]DisplayMode
	rawOn  map[EntryID]bool
}

// NewBlockFoldManager 创建折叠管理器。
func NewBlockFoldManager() *BlockFoldManager {
	return &BlockFoldManager{
		states: make(map[EntryID]DisplayMode),
		rawOn:  make(map[EntryID]bool),
	}
}

// GetDisplayMode 返回 entry 的当前显示模式。
func (m *BlockFoldManager) GetDisplayMode(id EntryID) DisplayMode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if mode, ok := m.states[id]; ok {
		return mode
	}
	return DisplayNormal
}

// ToggleFold 切换 entry 的折叠状态。
func (m *BlockFoldManager) ToggleFold(id EntryID, isRunning bool) DisplayMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.states[id]
	if current == 0 {
		current = DisplayNormal
	}
	next := NextDisplayMode(current)
	m.states[id] = next
	return next
}

// IsRawMode 返回 entry 是否在 raw 模式。
func (m *BlockFoldManager) IsRawMode(id EntryID) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rawOn[id]
}

// ToggleRaw 切换 entry 的 raw 模式。
func (m *BlockFoldManager) ToggleRaw(id EntryID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rawOn[id] = !m.rawOn[id]
	return m.rawOn[id]
}

// SetDisplayMode 显式设置 entry 的显示模式。
func (m *BlockFoldManager) SetDisplayMode(id EntryID, mode DisplayMode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[id] = mode
}
