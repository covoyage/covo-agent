// Package safego provides a panic-recovering goroutine launcher.
//
// Use SafeGo instead of bare `go func()` to guarantee a panic in a
// background goroutine is logged (not silently crashing the whole process)
// and the stack trace is captured for debugging.
package safego

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// SafeGo runs fn in a new goroutine with panic recovery.
// If a panic occurs, it is logged with the stack trace.
// Pass nil for logger to use slog.Default().
func SafeGo(fn func(), logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("goroutine panic recovered",
					"panic", fmt.Sprintf("%v", r),
					"stack", string(debug.Stack()),
				)
			}
		}()
		fn()
	}()
}
