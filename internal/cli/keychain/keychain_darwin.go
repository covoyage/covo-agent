//go:build darwin

package keychain

import (
	"os/exec"
	"strings"
)

// macOS-specific keychain access via /usr/bin/security.
// Calls the security CLI directly rather than linking a native keychain
// library — the go-keyring crate wraps the Security.framework C API, but
// the CLI approach avoids native dependency headaches.

func keyringGet(account string) (string, error) {
	k := resolveAccount(account)
	cmd := exec.Command("security", "find-generic-password",
		"-s", serviceName,
		"-a", k,
		"-w",
	)
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		if isNotFound(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func keyringSet(account, password string) error {
	k := resolveAccount(account)
	// Delete existing entry first (add-generic-password -U doesn't always update).
	_ = keyringDelete(account)
	cmd := exec.Command("security", "add-generic-password",
		"-s", serviceName,
		"-a", k,
		"-w", password,
		"-U", // update if exists
	)
	cmd.Stdin = nil
	return cmd.Run()
}

func keyringDelete(account string) error {
	k := resolveAccount(account)
	cmd := exec.Command("security", "delete-generic-password",
		"-s", serviceName,
		"-a", k,
	)
	cmd.Stdin = nil
	// Non-zero exit means entry doesn't exist – that's fine.
	_ = cmd.Run()
	return nil
}

func isNotFound(err error) bool {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode() == 44 || exitErr.ExitCode() == 45
	}
	return false
}
