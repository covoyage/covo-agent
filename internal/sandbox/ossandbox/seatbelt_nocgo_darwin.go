//go:build darwin && !cgo

package ossandbox

import "fmt"

// sandboxInitInProcess is unavailable when built without CGO
// (e.g. cross-compiling for a different architecture, where CGO is
// disabled by default). In that case the Seatbelt sandbox is skipped
// and Apply() degrades gracefully to a no-op.
//
// To get kernel-enforced Seatbelt on macOS, build with CGO_ENABLED=1
// on the target architecture.
func sandboxInitInProcess(policyPath string) error {
	return fmt.Errorf("seatbelt sandbox requires CGO; rebuild with CGO_ENABLED=1 (skipped, no sandbox applied)")
}
