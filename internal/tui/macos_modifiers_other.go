//go:build !darwin

package tui

// ---------------------------------------------------------------------------
// macOS 修饰键侧信道探测 — 非 macOS 平台的空实现。
// ---------------------------------------------------------------------------

// ModifierState 是物理修饰键状态的快照（非 macOS 全 false）。
type ModifierState struct {
	Command bool
	Option  bool
	Shift   bool
	Control bool
}

// SnapshotModifiers 非 macOS 返回全 false。
func SnapshotModifiers() ModifierState {
	return ModifierState{}
}

// IsNewlineModifierHeld 非 macOS 返回 false。
func IsNewlineModifierHeld(termCtx *TerminalContext) bool {
	return false
}
