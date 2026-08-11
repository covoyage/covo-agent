//go:build !darwin && !linux

package syspower

// startPlatformImpl is the no-op fallback for unsupported platforms.
func init() {
	startPlatformImpl = func(l *Listener) bool {
		return false
	}
}
