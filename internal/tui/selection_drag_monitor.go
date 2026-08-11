package tui

import (
	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/core"
)

// ---------------------------------------------------------------------------
// SelectionDragMonitor — 鼠标拖选监控器。
//
// 作为 ChatApp 的子组件运行，接收 MouseMsg 事件，
// 追踪拖选状态并驱动 AutoScrollController。
//
// 当鼠标在内容区边缘拖选时，自动启动/停止边缘滚动。
// 组件本身不可见（Render 返回 nil），仅处理鼠标事件。
// ---------------------------------------------------------------------------

// SelectionDragMonitor 监控鼠标拖选并驱动自动滚动。
type SelectionDragMonitor struct {
	controller    *AutoScrollController
	dragging      bool
	lastMouseRow  int
	contentHeight int
}

// NewSelectionDragMonitor 创建拖选监控器。
// controller 是自动滚动控制器，app 用于获取内容区高度。
func NewSelectionDragMonitor(controller *AutoScrollController, app *chat.ChatApp) *SelectionDragMonitor {
	if controller == nil || app == nil {
		return nil
	}
	return &SelectionDragMonitor{
		controller: controller,
	}
}

// Render 实现 core.Component，返回 nil（不可见组件）。
func (m *SelectionDragMonitor) Render(int64) []string { return nil }

// Invalidate 实现 core.Component。
func (m *SelectionDragMonitor) Invalidate() {}

// Update 实现 core.Updatable，处理鼠标事件。
func (m *SelectionDragMonitor) Update(msg core.Msg) core.Cmd {
	if m == nil || m.controller == nil {
		return nil
	}
	switch ev := msg.(type) {
	case core.MouseMsg:
		switch ev.Action {
		case core.MousePress:
			if ev.Button != 0 {
				return nil
			}
			m.dragging = true
			m.lastMouseRow = int(ev.Row)
		case core.MouseMotion:
			if m.dragging {
				m.lastMouseRow = int(ev.Row)
				if m.contentHeight > 0 {
					m.controller.Update(m.lastMouseRow, m.contentHeight)
				}
			}
		case core.MouseRelease:
			m.dragging = false
			m.controller.Stop()
		}
	case core.WindowSizeMsg:
		// 用窗口高度作为内容区高度近似值
		m.contentHeight = int(ev.Height)
	}
	return nil
}

// Active reports whether an edge-scroll timer is currently running.
func (m *SelectionDragMonitor) Active() bool {
	return m != nil && m.controller != nil && m.controller.IsActive()
}

var _ core.Component = (*SelectionDragMonitor)(nil)
var _ core.Updatable = (*SelectionDragMonitor)(nil)
