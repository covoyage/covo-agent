//go:build windows

package keychain

import (
	"os/exec"
	"strings"
	"syscall"
)

// Windows Credential Manager via cmdkey.exe.
// Target name format: covo-agent:<b64_account>

func keyringGet(account string) (string, error) {
	k := resolveAccount(account)
	cmd := exec.Command("cmdkey", "/generic:"+serviceName+":"+k)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	// cmdkey output format: "  User Name: <account>\r\n  Password: <value>\r\n"
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Password:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Password:")), nil
		}
	}
	return "", nil
}

func keyringSet(account, password string) error {
	k := resolveAccount(account)
	// Delete existing first (cmdkey /add with same target silently overwrites
	// but we delete to be safe).
	_ = keyringDelete(account)
	cmd := exec.Command("cmdkey", "/generic:"+serviceName+":"+k,
		"/user:"+k, "/pass:"+password)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = nil
	return cmd.Run()
}

func keyringDelete(account string) error {
	k := resolveAccount(account)
	cmd := exec.Command("cmdkey", "/delete:"+serviceName+":"+k)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = nil
	_ = cmd.Run()
	return nil
}
