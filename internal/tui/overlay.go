package tui

import (
	"strings"

	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
)

// LocalOverlay 实现 chat.OverlayRef 接口。
// 迁自 cmd/covo-agent/model_picker.go（6-24 重构）。
// 业务侧用 NewLocalOverlay 构造，用 ShowPanel 推入。
type LocalOverlay struct {
	Content       core.Component
	Focus         bool
	DimBackground bool
	Anchor        int
	PercentX      int
	PercentY      int
	WidthPct      int
	HeightPct     int
}

func (o *LocalOverlay) OverlayContent() core.Component { return o.Content }
func (o *LocalOverlay) SetOverlayFocus(v bool)         { o.Focus = v }
func (o *LocalOverlay) SetOverlayDimBackground(v bool) { o.DimBackground = v }
func (o *LocalOverlay) OverlayWantsFocus() bool        { return o.Focus }
func (o *LocalOverlay) OverlayDimBackground() bool     { return o.DimBackground }
func (o *LocalOverlay) OverlayAnchor() int             { return o.Anchor }
func (o *LocalOverlay) OverlayPercentX() int           { return o.PercentX }
func (o *LocalOverlay) OverlayPercentY() int           { return o.PercentY }
func (o *LocalOverlay) OverlayWidthPct() int           { return o.WidthPct }
func (o *LocalOverlay) OverlayHeightPct() int          { return o.HeightPct }

// newLocalOverlay 构造一个 full-screen overlay。
// 总是开 dimBackground、focus、anchor=4 (center)，percentX/Y=50。
// widthPct/heightPct：百分比（0-100），传 <=0 视为 100（full-screen，绕开 dim bug）。
func newLocalOverlay(content core.Component, widthPct, heightPct int) *LocalOverlay {
	if widthPct <= 0 || widthPct > 100 {
		widthPct = 100
	}
	if heightPct <= 0 || heightPct > 100 {
		heightPct = 100
	}
	return &LocalOverlay{
		Content:       content,
		Focus:         true,
		DimBackground: true,
		Anchor:        4,
		PercentX:      50,
		PercentY:      50,
		WidthPct:      widthPct,
		HeightPct:     heightPct,
	}
}

// NewLocalOverlay 暴露公共构造函数，供业务侧使用未包装的 overlay。
// 与 ShowPanel 不同：不自动 esc-close 包装。
// 业务侧负责监听自己的取消事件调 UIBus.CloseOverlay(ov)。
// 默认 full-screen 100%×100%（绕开 covonaut 库 dim 缺陷）。
func NewLocalOverlay(content core.Component, widthPct, heightPct int) chat.OverlayRef {
	return newLocalOverlay(content, widthPct, heightPct)
}

// NewAnchoredOverlay 允许业务侧指定 anchor/percent/width/height。
// 用于居中但不占满全屏的弹框（如 approval picker）。
// 不包装 esc-close；业务侧自己处理取消。
func NewAnchoredOverlay(content core.Component, anchor, percentX, percentY, widthPct, heightPct int) chat.OverlayRef {
	if widthPct <= 0 || widthPct > 100 {
		widthPct = 100
	}
	if heightPct <= 0 || heightPct > 100 {
		heightPct = 100
	}
	return &LocalOverlay{
		Content:       content,
		Focus:         true,
		DimBackground: true,
		Anchor:        anchor,
		PercentX:      percentX,
		PercentY:      percentY,
		WidthPct:      widthPct,
		HeightPct:     heightPct,
	}
}

// NewEscClose 包装 content 为 escCloseComponent，业务侧提供 onClose。
// 业务侧在 onClose 里调 UIBus.CloseOverlay(ov) 关闭。
func NewEscClose(content core.Component, onClose func()) core.Component {
	return &escCloseComponent{content: content, onClose: onClose}
}

// escCloseComponent 包装任意 Component，添加 ESC 关闭语义。
//
// 设计要点（covo-agent 6-24 bug 修复）：
//  1. (A) push 后由 caller 显式 Focus，避免被 editor 抢回焦点
//  2. (B) ESC 双匹配：先 MatchesKey，失败再回退到 "\x1b"（兼容 kitty 协议）
//  3. (C) 弹框用 100%×100% full-screen overlay，绕开 covonaut 库
//     dimBackground 函数只覆盖弹框所在行的设计缺陷
type escCloseComponent struct {
	content core.Component
	onClose func()
}

func (e *escCloseComponent) Render(width int64) []string {
	return e.content.Render(width)
}

func (e *escCloseComponent) Invalidate() {
	e.content.Invalidate()
}

func (e *escCloseComponent) Update(msg core.Msg) core.Cmd {
	if key, ok := msg.(core.KeyMsg); ok && isEscape(key.Data) {
		// Give the inner component a chance to handle ESC first
		if interceptor, ok := e.content.(escHandler); ok && interceptor.HandleEsc() {
			return nil
		}
		if e.onClose != nil {
			e.onClose()
		}
		return nil
	}
	if updatable, ok := e.content.(core.Updatable); ok {
		return updatable.Update(msg)
	}
	return nil
}

// escHandler allows inner components to intercept ESC for inline modes (search, rename).
type escHandler interface {
	HandleEsc() bool // return true if ESC was consumed
}

// isEscape 双匹配：covonaut 的 MatchesKey 优先，失败回退到裸 ESC 字节。
// 解决 kitty/iTerm2 协议下 ESC 序列匹配失败的问题。
func isEscape(data string) bool {
	if terminal.MatchesKey(data, "escape") {
		return true
	}
	// kitty keyboard protocol may encode ESC as CSI-u: "\x1b[27u" or
	// "\x1b[27;...u". Match strictly to avoid false positives such as
	// "\x1b[270u".
	if data == "\x1b[27u" || strings.HasPrefix(data, "\x1b[27;") && strings.HasSuffix(data, "u") {
		return true
	}
	// 兜底：raw ESC byte（0x1b）单独成键。termios 偶发会把
	// ESC 和紧随的 ctrl+c 在同一个 read() 读回，buf 拿到
	// "\x1b\x03" 这种合并片段，这时 MatchesKey 拿不到标准
	// "escape" name，所以再加一个按前缀的判断；前缀同时是
	// 真 ANSI 序列首字节（"["/"O"）的不算，那不是 ESC。
	if len(data) == 0 || data[0] != 0x1B {
		return false
	}
	// \x1b[A / \x1bOH 等已经是真序列的，走 ParseKeys
	// 会被识别成 arrow-up / Home，MatchesKey("escape")
	// 早就 return false 到了这里；我们再放过它只会把
	// arrow-up 错当成 ESC，所以这种不算。
	if len(data) >= 2 && (data[1] == '[' || data[1] == 'O') {
		return false
	}
	return true
}

// pushFullScreenOverlay 推入一个弹框。
// widthPct/heightPct：弹框百分比（<=0 视为 100% full-screen，绕开 dim bug）。
func pushFullScreenOverlay(app *chat.ChatApp, content core.Component, widthPct, heightPct int) chat.OverlayRef {
	var ref chat.OverlayRef
	closeFn := func() {
		if ref != nil {
			app.Host().RemoveOverlay(ref)
		}
		app.Host().Focus(app.Editor())
	}
	wrapped := &escCloseComponent{content: content, onClose: closeFn}

	ov := newLocalOverlay(wrapped, widthPct, heightPct)
	ref = ov
	app.Host().PushOverlay(ov)

	// (A) 显式把焦点切到弹框，避免 editor 抢回焦点导致 ESC 路由失败。
	app.Host().Focus(wrapped)

	return ref
}
