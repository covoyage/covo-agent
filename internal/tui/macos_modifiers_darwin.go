//go:build darwin

package tui

// ---------------------------------------------------------------------------
// macOS 修饰键侧信道探测。
//
// Apple Terminal 会丢掉 Shift/Option/Cmd + Enter 的修饰位。
// 通过 CoreGraphics CGEventSourceFlagsState 直接读取物理修饰键状态，
// 在输入链顶部重写 KeyEvent，确保所有下游看到正确修饰位。
//
// 此文件仅 macOS 编译。
// ---------------------------------------------------------------------------

/*
#cgo LDFLAGS: -framework CoreGraphics
#include <CoreGraphics/CoreGraphics.h>
*/
import "C"

// kCGEventSourceStateHIDSystemState 的值（来自 CGEventSourceStateID）。
// 用数值而非 C 宏，因为 cgo 无法直接引用宏。
const kCGEventSourceStateHIDSystemState = 1

// CGEventFlags 位掩码（来自 <CoreGraphics/CGEventTypes.h>）。
const (
	kCGEventFlagMaskShift     = 0x00020000
	kCGEventFlagMaskControl   = 0x00040000
	kCGEventFlagMaskAlternate = 0x00080000 // Option key
	kCGEventFlagMaskCommand   = 0x00100000
)

// ModifierState 是物理修饰键状态的快照。
type ModifierState struct {
	Command bool
	Option  bool
	Shift   bool
	Control bool
}

// SnapshotModifiers 通过 CoreGraphics 读取当前物理修饰键状态。
// 一次系统调用，解码所有修饰位。
func SnapshotModifiers() ModifierState {
	flags := uint64(C.CGEventSourceFlagsState(kCGEventSourceStateHIDSystemState))
	return ModifierState{
		Command: flags&kCGEventFlagMaskCommand != 0,
		Option:  flags&kCGEventFlagMaskAlternate != 0,
		Shift:   flags&kCGEventFlagMaskShift != 0,
		Control: flags&kCGEventFlagMaskControl != 0,
	}
}

// IsNewlineModifierHeld 检查用户是否持有应该将 Enter 变为换行的修饰键。
// 仅在 Apple Terminal（已知会丢修饰位的终端）上触发 OS 探测。
func IsNewlineModifierHeld(termCtx *TerminalContext) bool {
	if termCtx == nil || !termCtx.NeedsModifierRescue() {
		return false
	}
	s := SnapshotModifiers()
	return s.Shift || s.Option || s.Command
}
