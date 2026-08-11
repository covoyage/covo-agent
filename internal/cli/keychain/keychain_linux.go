//go:build linux

package keychain

import (
	"os/exec"
	"strings"
)

// Linux Secret Service via libsecret's secret-tool CLI.
// Requires: apt install libsecret-tools (or equivalent).
// Attribute format: service=covo-agent, account=<b64_account>

func keyringGet(account string) (string, error) {
	k := resolveAccount(account)
	cmd := exec.Command("secret-tool", "lookup",
		"service", serviceName,
		"account", k,
	)
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 0 {
			return "", nil // entry not found
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func keyringSet(account, password string) error {
	k := resolveAccount(account)
	// Delete existing entry first.
	_ = keyringDelete(account)
	cmd := exec.Command("secret-tool", "store",
		"--label", "covo-agent: "+k,
		"service", serviceName,
		"account", k,
	)
	cmd.Stdin = strings.NewReader(password)
	return cmd.Run()
}

func keyringDelete(account string) error {
	k := resolveAccount(account)
	cmd := exec.Command("secret-tool", "clear",
		"service", serviceName,
		"account", k,
	)
	cmd.Stdin = nil
	_ = cmd.Run()
	return nil
}
