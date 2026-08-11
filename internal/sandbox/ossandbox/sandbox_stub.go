//go:build !linux && !darwin

package ossandbox

import "fmt"

// platformSupportInfo returns unsupported for non-Unix platforms.
func platformSupportInfo() (supported bool, details string) {
	return false, fmt.Sprintf("OS-level sandbox not supported on %s", "this platform")
}

// applySandbox is a no-op stub for unsupported platforms.
func applySandbox(profile *SandboxProfile, workspace string) error {
	return fmt.Errorf("sandbox not supported on this platform")
}

// resolvePath resolves a path relative to the workspace if not absolute.
func resolvePath(p, workspace string) string {
	return p
}
