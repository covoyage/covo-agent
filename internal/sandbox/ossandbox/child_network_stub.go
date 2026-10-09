//go:build !linux && !darwin

package ossandbox

import (
	"fmt"
	"os/exec"
)

// ApplyChildNetworkRestriction is a no-op on unsupported platforms.
func ApplyChildNetworkRestriction(cmd *exec.Cmd) error {
	if ShouldRestrictChildNetwork() {
		return fmt.Errorf("child network restriction is unavailable on this platform")
	}
	return nil
}

// platformNetworkRestrictionAvailable returns false on unsupported platforms.
func platformNetworkRestrictionAvailable() bool {
	return false
}

// NetworkRestrictionDetails returns a description of network restriction
// capability on this platform.
func NetworkRestrictionDetails() string {
	return fmt.Sprintf("child network restriction not supported on this platform")
}
