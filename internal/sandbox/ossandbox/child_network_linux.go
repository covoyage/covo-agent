//go:build linux

package ossandbox

import (
	"os/exec"
)

// ApplyChildNetworkRestriction wraps an exec.Cmd to block network access
// for the child process on Linux.
//
// On Linux, this uses `unshare -Urn` to create a new user namespace and
// network namespace with no interfaces, effectively blocking all network
// access. Landlock file system restrictions from the parent are inherited.
//
// Requires kernel support for unprivileged user namespaces
// (kernel.unprivileged_userns_clone = 1, which is the default on most
// modern Linux distributions).
//
// The cmd is modified in place: its Path and Args are replaced to invoke
// unshare as a wrapper around the original command.
func ApplyChildNetworkRestriction(cmd *exec.Cmd) error {
	if !ShouldRestrictChildNetwork() {
		return nil
	}

	originalPath := cmd.Path
	originalArgs := cmd.Args
	launcher, err := trustedLauncher("unshare")
	if err != nil {
		return err
	}
	if err := probeLauncher(launcher, "-Urn", "--", "/bin/true"); err != nil {
		return err
	}

	// unshare -Urn -- <original command> <original args...>
	// -U: new user namespace (allows unprivileged network namespace creation)
	// -r: map current user to root in the new namespace
	// -n: new network namespace (no interfaces = no network)
	cmd.Path = launcher
	cmd.Args = append([]string{launcher, "-Urn", "--", originalPath}, originalArgs[1:]...)
	prepareLauncherEnvironment(cmd)

	return nil
}

// platformNetworkRestrictionAvailable returns true if this platform can
// restrict child process network access.
func platformNetworkRestrictionAvailable() bool {
	_, err := trustedLauncher("unshare")
	return err == nil
}

// NetworkRestrictionDetails returns a human-readable description of how
// child network restriction is implemented on this platform.
func NetworkRestrictionDetails() string {
	if !platformNetworkRestrictionAvailable() {
		return "unshare not found — cannot restrict child network (install util-linux)"
	}
	return "Linux unshare -Urn (network namespace isolation)"
}
