// Package tui 提供 covo-agent 业务 UI 适配层。
//
// 业务侧（cmd/covo-agent/）不应直接依赖 covonaut/tui/chat 内部 API。
// 所有 UI 操作通过本包的 UIBus 语义化方法进行：
//   - PrintSystem/PrintError  -> 状态消息
//   - UpdateStatusBar         -> 状态栏更新
//   - ShowPanel/ClosePanel    -> 弹框（统一处理 ESC 路由、焦点、dim 背景）
//   - FocusEditor             -> 焦点控制
//
// 依赖方向：
//
//	cmd/covo-agent/*  ->  internal/tui/*  ->  covonaut/tui/{core,terminal,theme,component,chat}
//
// 注：UIBus 当前直接 wrap *chat.ChatApp；中长期可换为自有实现。
package tui

import (
	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/component"
	"github.com/covoyage/covonaut/tui/core"
)

// UIBus 是 covo-agent 业务侧的 UI 门面。
// 所有 UI 操作（系统消息、状态栏、弹框、焦点）都走这里。
type UIBus struct {
	app *chat.ChatApp
}

// NewUIBus 构造 UIBus。
func NewUIBus(app *chat.ChatApp) *UIBus {
	return &UIBus{app: app}
}

// App 返回底层 ChatApp（仅供需要直接调底层 API 的地方使用，
// 业务代码应避免直接访问）。
func (u *UIBus) App() *chat.ChatApp { return u.app }

// Editor 返回 ChatApp 的输入编辑器。app==nil 时返回 nil。
func (u *UIBus) Editor() *component.Editor {
	if u == nil || u.app == nil {
		return nil
	}
	return u.app.Editor()
}

// Host 返回 ChatApp 的 host 抽象。app==nil 时返回 nil。
func (u *UIBus) Host() chat.AppHost {
	if u == nil || u.app == nil {
		return nil
	}
	return u.app.Host()
}

// StatusBar 返回 ChatApp 的状态栏。app==nil 时返回 nil。
func (u *UIBus) StatusBar() *component.StatusBar {
	if u == nil || u.app == nil {
		return nil
	}
	return u.app.StatusBar()
}

// PrintSystem 输出一条系统消息。app==nil 时 no-op。
func (u *UIBus) PrintSystem(msg string) {
	if u == nil || u.app == nil {
		return
	}
	u.app.PrintSystem(msg)
}

// PrintError 输出一条错误消息。app==nil 时 no-op。
func (u *UIBus) PrintError(err error) {
	if u == nil || u.app == nil {
		return
	}
	u.app.PrintError(err)
}

// UpdateStatusBar 更新状态栏的 provider/model/mode。app==nil 时 no-op。
func (u *UIBus) UpdateStatusBar(provider, model, mode string) {
	if u == nil || u.app == nil {
		return
	}
	u.app.UpdateStatusBar(provider, model, mode)
}

// UpdateStatusBarMode 仅更新 mode 字段。app==nil 时 no-op。
func (u *UIBus) UpdateStatusBarMode(provider, model, mode string) {
	if u == nil || u.app == nil {
		return
	}
	u.app.UpdateStatusBar(provider, model, mode)
}

// ShowPanel 推入一个全屏弹框（处理 dim 背景 + ESC 关闭 + 焦点）。
// widthPct/heightPct: 弹框相对屏幕的百分比（0-100）；0 表示 full-screen 100%。
// 返回 chat.OverlayRef，业务侧可调 CloseOverlay 关闭。
// app==nil 时返回 nil。
func (u *UIBus) ShowPanel(content core.Component, widthPct, heightPct int) chat.OverlayRef {
	if u == nil || u.app == nil {
		return nil
	}
	return pushFullScreenOverlay(u.app, content, widthPct, heightPct)
}

// ClosePanel 关闭弹框（接受 chat.OverlayRef）。nil/no-op safe。
func (u *UIBus) ClosePanel(ref chat.OverlayRef) {
	if u == nil || u.app == nil || ref == nil {
		return
	}
	u.app.Host().RemoveOverlay(ref)
	u.app.Host().Focus(u.app.Editor())
}

// CloseOverlay 是 ClosePanel 的别名，供语义清晰处使用。
func (u *UIBus) CloseOverlay(ref chat.OverlayRef) {
	u.ClosePanel(ref)
}

// FocusEditor 把焦点切回主输入框。app==nil 时 no-op。
func (u *UIBus) FocusEditor() {
	if u == nil || u.app == nil {
		return
	}
	u.app.Host().Focus(u.app.Editor())
}

// RequestRender 通知 host 重绘（用于数据变更后）。app==nil 时 no-op。
func (u *UIBus) RequestRender() {
	if u == nil || u.app == nil || u.app.Host() == nil {
		return
	}
	u.app.Host().RequestRender()
}
