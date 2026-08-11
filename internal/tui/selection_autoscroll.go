package tui

import (
	"github.com/covoyage/covonaut/tui/chat"
)

// ---------------------------------------------------------------------------
// SelectionAutoScroll — 将 AutoScrollController 接入 ChatApp 的滚动。
//
// 由于 covonaut ChatHistory 内部处理鼠标事件，本集成通过创建一个
// AutoScrollController 并将其滚动回调绑定到 ChatHistory.ScrollBy 来实现接入。
// 调用方在鼠标拖选时通过 controller.Update(row, height) 驱动自动滚动。
// ---------------------------------------------------------------------------

// BindSelectionAutoScroll 创建并绑定自动滚动控制器到 ChatApp。
// 返回的控制器在鼠标拖选时应调用 Update(mouseRow, contentHeight)。
// 当鼠标在边缘阈值内时，控制器会自动调用 ScrollBy 滚动聊天历史。
func BindSelectionAutoScroll(app *chat.ChatApp) *AutoScrollController {
	if app == nil {
		return nil
	}
	history := app.History()
	controller := NewAutoScrollController(func(dir AutoScrollDirection, speed int) {
		switch dir {
		case AutoScrollUp:
			history.ScrollBy(int64(speed))
		case AutoScrollDown:
			history.ScrollBy(int64(-speed))
		}
		if app.Host() != nil {
			app.Host().RequestRender()
		}
	})
	return controller
}
