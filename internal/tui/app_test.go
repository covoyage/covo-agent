package tui

import (
	"testing"

	"github.com/covoyage/covonaut/tui/component"
	"github.com/covoyage/covonaut/tui/core"
)

// ---------------------------------------------------------------------------
// TestUIBus_NilSafe — 所有 UIBus 方法对 nil receiver 安全
// ---------------------------------------------------------------------------

func TestUIBus_NilSafe(t *testing.T) {
	var u *UIBus
	// 不应 panic
	u.PrintSystem("hello")
	u.PrintError(nil)
	u.UpdateStatusBar("p", "m", "mode")
	u.UpdateStatusBarMode("p", "m", "mode")
	if got := u.Editor(); got != nil {
		t.Errorf("Editor() = %v, want nil", got)
	}
	if got := u.Host(); got != nil {
		t.Errorf("Host() = %v, want nil", got)
	}
	if got := u.StatusBar(); got != nil {
		t.Errorf("StatusBar() = %v, want nil", got)
	}
	if got := u.ShowPanel(nil, 0, 0); got != nil {
		t.Errorf("ShowPanel() = %v, want nil", got)
	}
	u.ClosePanel(nil) // 双重 nil safe
	u.CloseOverlay(nil)
	u.FocusEditor()
	u.RequestRender()
}

// ---------------------------------------------------------------------------
// TestUIBus_AppNilSafe — UIBus 有 receiver 但 app 字段为 nil
// ---------------------------------------------------------------------------

func TestUIBus_AppNilSafe(t *testing.T) {
	u := &UIBus{app: nil}
	u.PrintSystem("hello")
	u.PrintError(nil)
	u.UpdateStatusBar("p", "m", "mode")
	if got := u.Editor(); got != nil {
		t.Errorf("Editor() = %v, want nil", got)
	}
	if got := u.Host(); got != nil {
		t.Errorf("Host() = %v, want nil", got)
	}
	if got := u.ShowPanel(nil, 0, 0); got != nil {
		t.Errorf("ShowPanel() = %v, want nil", got)
	}
	u.ClosePanel(nil)
	u.FocusEditor()
}

// ---------------------------------------------------------------------------
// TestLocalOverlay_ImplementsOverlayRef
// ---------------------------------------------------------------------------

func TestLocalOverlay_ImplementsOverlayRef(t *testing.T) {
	content := component.NewEditor(nil)
	ov := NewLocalOverlay(content, 80, 70).(*LocalOverlay)
	if ov.Content != core.Component(content) {
		t.Errorf("OverlayContent() returned wrong component")
	}
	ov.SetOverlayFocus(true)
	if !ov.OverlayWantsFocus() {
		t.Errorf("OverlayWantsFocus() = false after SetOverlayFocus(true)")
	}
	ov.SetOverlayDimBackground(true)
	if !ov.OverlayDimBackground() {
		t.Errorf("OverlayDimBackground() = false after SetOverlayDimBackground(true)")
	}
	if got := ov.OverlayWidthPct(); got != 80 {
		t.Errorf("OverlayWidthPct() = %d, want 80", got)
	}
	if got := ov.OverlayHeightPct(); got != 70 {
		t.Errorf("OverlayHeightPct() = %d, want 70", got)
	}
}

// ---------------------------------------------------------------------------
// TestLocalOverlay_FullScreen — widthPct<=0 触发 full-screen 100%×100%
// ---------------------------------------------------------------------------

func TestLocalOverlay_FullScreen(t *testing.T) {
	ov := NewLocalOverlay(component.NewEditor(nil), 0, 0).(*LocalOverlay)
	if got := ov.OverlayWidthPct(); got != 100 {
		t.Errorf("widthPct=0 should become 100%%, got %d", got)
	}
	if got := ov.OverlayHeightPct(); got != 100 {
		t.Errorf("heightPct=0 should become 100%%, got %d", got)
	}

	ov2 := NewLocalOverlay(component.NewEditor(nil), -1, -5).(*LocalOverlay)
	if got := ov2.OverlayWidthPct(); got != 100 {
		t.Errorf("widthPct<0 should become 100%%, got %d", got)
	}
	if got := ov2.OverlayHeightPct(); got != 100 {
		t.Errorf("heightPct<0 should become 100%%, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// TestNewAnchoredOverlay
// ---------------------------------------------------------------------------

func TestNewAnchoredOverlay_Anchor(t *testing.T) {
	ov := NewAnchoredOverlay(component.NewEditor(nil), 4, 50, 50, 80, 25).(*LocalOverlay)
	if got := ov.OverlayAnchor(); got != 4 {
		t.Errorf("OverlayAnchor() = %d, want 4", got)
	}
	if got := ov.OverlayPercentX(); got != 50 {
		t.Errorf("OverlayPercentX() = %d, want 50", got)
	}
	if got := ov.OverlayPercentY(); got != 50 {
		t.Errorf("OverlayPercentY() = %d, want 50", got)
	}
	if got := ov.OverlayWidthPct(); got != 80 {
		t.Errorf("OverlayWidthPct() = %d, want 80", got)
	}
	if got := ov.OverlayHeightPct(); got != 25 {
		t.Errorf("OverlayHeightPct() = %d, want 25", got)
	}
}

// ---------------------------------------------------------------------------
// TestEscCloseComponent_EscTriggersOnClose
// ---------------------------------------------------------------------------

func TestEscCloseComponent_TriggersOnClose(t *testing.T) {
	called := 0
	onClose := func() { called++ }

	wrapped := &escCloseComponent{
		content: component.NewEditor(nil),
		onClose: onClose,
	}

	// 模拟 ESC 键
	wrapped.Update(core.KeyMsg{Data: "\x1b"})
	if called != 1 {
		t.Errorf("expected onClose called once, got %d", called)
	}

	// 多次 ESC 安全
	wrapped.Update(core.KeyMsg{Data: "\x1b"})
	if called != 2 {
		t.Errorf("expected onClose called twice, got %d", called)
	}
}

// ---------------------------------------------------------------------------
// TestEscCloseComponent_PassesThrough — 非 ESC 键不触发 onClose
// ---------------------------------------------------------------------------

func TestEscCloseComponent_PassesThrough(t *testing.T) {
	called := 0
	onClose := func() { called++ }

	content := component.NewEditor(nil)
	wrapped := &escCloseComponent{
		content: content,
		onClose: onClose,
	}

	// 模拟普通字符 'a'，应不触发 onClose
	wrapped.Update(core.KeyMsg{Data: "a"})
	if called != 0 {
		t.Errorf("onClose should not be called for non-ESC key, got %d calls", called)
	}
}

// ---------------------------------------------------------------------------
// TestIsEscape
// ---------------------------------------------------------------------------

func TestIsEscape(t *testing.T) {
	tests := []struct {
		name string
		data string
		want bool
	}{
		{"raw ESC byte", "\x1b", true},
		{"kitty CSI-u escape", "\x1b[27u", true},
		{"kitty CSI-u escape with modifiers", "\x1b[27;5u", true},
		{"kitty CSI-u non-escape code 270", "\x1b[270u", false},
		{"kitty CSI-u non-escape code 271", "\x1b[271u", false},
		{"CSI arrow up", "\x1b[A", false},         // arrow key, not bare ESC
		{"ESC + ctrl+c merged", "\x1b\x03", true}, // termios sometimes delivers these in one read
		{"ESC + SS3 truncated", "\x1bO", true},    // parseOne collapses trailing-only SS3 to "escape"
		{"normal char", "a", false},
		{"enter", "\r", false},
		{"empty", "", false},
		{"tab", "\t", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isEscape(tt.data); got != tt.want {
				t.Errorf("isEscape(%q) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}
