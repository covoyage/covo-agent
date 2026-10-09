//go:build darwin && !cgo

package tui

// macOS 修饰键侧信道探测 — 无 CGO 的降级实现。
//
// CGO_ENABLED=0 构建时无法读取 CoreGraphics 修饰键状态，
// 退化为恒返回 false（与 macos_modifiers_other.go 相同行为）：
// Apple Terminal 修饰键修复在此构建下不可用，其余功能不受影响。
// 如需该修复，请以 CGO_ENABLED=1 构建 macOS 版本。

// ModifierState 是物理修饰键状态的快照（无 CGO 构建下恒为 false）。
type ModifierState struct {
	Command bool
	Option  bool
	Shift   bool
	Control bool
}

// SnapshotModifiers returns an all-false snapshot when built without CGO.
func SnapshotModifiers() ModifierState {
	return ModifierState{}
}

// IsNewlineModifierHeld always reports false when built without CGO.
func IsNewlineModifierHeld(_ *TerminalContext) bool {
	return false
}
