package tui

import (
	"sync"

	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/core"
)

// ---------------------------------------------------------------------------
// ModalManager — 统一弹窗生命周期管理。
//
// 取代业务侧逐个 pushFullScreenOverlay + 手动管理 OverlayRef 的模式。
//
// 架构：
//   - ModalKind 枚举：标识所有弹窗类型
//   - ModalManager：持有当前活跃弹窗的 OverlayRef，统一 ESC 路由和焦点管理
//   - ShowModal/CloseModal/ActiveModal：统一 API
//
// 使用方式：
//
//	mm := NewModalManager(uibus)
//	mm.ShowModal(ModalKindModelPicker, picker, 80, 80)
//	...
//	mm.CloseModal()  // 关闭当前弹窗
//	mm.ActiveModal() // 获取当前活跃弹窗类型
// ---------------------------------------------------------------------------

// ModalKind 枚举标识所有弹窗类型。
type ModalKind int

const (
	ModalNone ModalKind = iota
	ModalModelPicker
	ModalSessionTree
	ModalApprovalPicker
	ModalMCPMarketplace
	ModalChangedFiles
	ModalSessionSelector
	ModalKeyHelp
	ModalStatusLineConfig
	ModalGeneric // 通用弹窗（未指定具体类型）
)

func (m ModalKind) String() string {
	switch m {
	case ModalModelPicker:
		return "model_picker"
	case ModalSessionTree:
		return "session_tree"
	case ModalApprovalPicker:
		return "approval_picker"
	case ModalMCPMarketplace:
		return "mcp_marketplace"
	case ModalChangedFiles:
		return "changed_files"
	case ModalSessionSelector:
		return "session_selector"
	case ModalKeyHelp:
		return "key_help"
	case ModalStatusLineConfig:
		return "status_line_config"
	case ModalGeneric:
		return "generic"
	default:
		return "none"
	}
}

// ModalEntry 记录一个已打开弹窗的状态。
type ModalEntry struct {
	Kind    ModalKind
	Ref     chat.OverlayRef
	Content core.Component
}

// ModalManager 管理弹窗生命周期。
type ModalManager struct {
	mu      sync.Mutex
	bus     *UIBus
	current *ModalEntry // 当前活跃弹窗（栈深度 1，支持将来扩展为栈）
}

// NewModalManager 创建弹窗管理器。
func NewModalManager(bus *UIBus) *ModalManager {
	return &ModalManager{bus: bus}
}

// ShowModal 推入一个弹窗。如果已有弹窗打开，先关闭旧的。
// kind 标识弹窗类型，content 是弹窗内容组件。
// widthPct/heightPct 是弹窗百分比（0=full-screen）。
// 返回 OverlayRef。
func (m *ModalManager) ShowModal(kind ModalKind, content core.Component, widthPct, heightPct int) chat.OverlayRef {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 先关闭已有弹窗
	if m.current != nil {
		m.closeCurrentLocked()
	}

	ref := m.bus.ShowPanel(content, widthPct, heightPct)
	m.current = &ModalEntry{
		Kind:    kind,
		Ref:     ref,
		Content: content,
	}
	return ref
}

// CloseModal 关闭当前弹窗并恢复焦点到编辑器。
func (m *ModalManager) CloseModal() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCurrentLocked()
}

// closeCurrentLocked 必须在持锁时调用。
func (m *ModalManager) closeCurrentLocked() {
	if m.current == nil {
		return
	}
	m.bus.ClosePanel(m.current.Ref)
	m.current = nil
}

// ActiveModal 返回当前活跃弹窗类型（ModalNone 表示无弹窗）。
func (m *ModalManager) ActiveModal() ModalKind {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return ModalNone
	}
	return m.current.Kind
}

// IsActive 检查指定类型的弹窗是否当前活跃。
func (m *ModalManager) IsActive(kind ModalKind) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current != nil && m.current.Kind == kind
}

// CurrentContent 返回当前弹窗的内容组件（无弹窗返回 nil）。
func (m *ModalManager) CurrentContent() core.Component {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return nil
	}
	return m.current.Content
}

// IsModalOpen 返回是否任一弹窗正在显示。
func (m *ModalManager) IsModalOpen() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current != nil
}

// HandleESC 统一处理 ESC 事件。
// 如果当前有弹窗打开，关闭它并返回 true。
// 无弹窗则返回 false（交由其他处理器处理）。
func (m *ModalManager) HandleESC() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return false
	}
	m.closeCurrentLocked()
	return true
}
