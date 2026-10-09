//go:build linux || darwin

package ossandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func trustedLauncher(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err != nil {
			return "", err
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
			return "", fmt.Errorf("sandbox launcher path %q must be root-owned and not group/world-writable", current)
		}
		if current == path && (!info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0) {
			return "", fmt.Errorf("sandbox launcher %q is not executable", path)
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	return path, nil
}

func probeLauncher(path string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sandbox launcher capability probe failed: %w", err)
	}
	return nil
}

func prepareLauncherEnvironment(cmd *exec.Cmd) {
	environment := cmd.Env
	if environment == nil {
		environment = os.Environ()
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "LD_") || strings.HasPrefix(name, "DYLD_") {
			continue
		}
		filtered = append(filtered, entry)
	}
	cmd.Env = filtered
}
