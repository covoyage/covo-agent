//go:build darwin

package ossandbox

import (
	"fmt"
	"os/exec"
)

// ApplyChildNetworkRestriction wraps an exec.Cmd to block network access
// for the child process on macOS.
//
// On macOS, this uses sandbox-exec with a minimal Seatbelt profile that
// denies all network operations. File system restrictions from the parent's
// sandbox are inherited (Seatbelt restrictions are cumulative — children
// can only become more restricted, never less).
//
// The cmd is modified in place: its Path and Args are replaced to invoke
// sandbox-exec as a wrapper around the original command.
func ApplyChildNetworkRestriction(cmd *exec.Cmd) error {
	if !ShouldRestrictChildNetwork() {
		return nil
	}

	originalPath := cmd.Path
	originalArgs := cmd.Args

	// Build a Seatbelt profile that denies network but allows everything else.
	// File restrictions are inherited from the parent process's sandbox and
	// cannot be escaped.
	seatbeltProfile := `(version 1)
(deny network*)
(allow default)
`
	launcher, err := trustedLauncher("/usr/bin/sandbox-exec")
	if err != nil {
		return err
	}
	if err := probeLauncher(launcher, "-p", seatbeltProfile, "--", "/usr/bin/true"); err != nil {
		return err
	}

	// sandbox-exec -p '<profile>' -- <original command> <original args...>
	cmd.Path = launcher
	cmd.Args = append([]string{launcher, "-p", seatbeltProfile, "--", originalPath}, originalArgs[1:]...)
	prepareLauncherEnvironment(cmd)

	return nil
}

// platformNetworkRestrictionAvailable returns true if this platform can
// restrict child process network access.
func platformNetworkRestrictionAvailable() bool {
	_, err := trustedLauncher("/usr/bin/sandbox-exec")
	return err == nil
}

// NetworkRestrictionDetails returns a human-readable description of how
// child network restriction is implemented on this platform.
func NetworkRestrictionDetails() string {
	if !platformNetworkRestrictionAvailable() {
		return fmt.Sprintf("sandbox-exec not found — cannot restrict child network")
	}
	return "macOS sandbox-exec (Seatbelt network deny)"
}
