package safety

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func BuildDeniedPaths(homeDir string) map[string]string {
	paths := []string{
		filepath.Join(homeDir, ".ssh", "authorized_keys"),
		filepath.Join(homeDir, ".ssh", "id_rsa"),
		filepath.Join(homeDir, ".ssh", "id_ed25519"),
		filepath.Join(homeDir, ".ssh", "config"),
		filepath.Join(homeDir, ".env"),
		filepath.Join(homeDir, ".bashrc"),
		filepath.Join(homeDir, ".zshrc"),
		filepath.Join(homeDir, ".profile"),
		filepath.Join(homeDir, ".bash_profile"),
		filepath.Join(homeDir, ".zprofile"),
		filepath.Join(homeDir, ".netrc"),
		filepath.Join(homeDir, ".pgpass"),
		filepath.Join(homeDir, ".npmrc"),
		filepath.Join(homeDir, ".pypirc"),
		filepath.Join(homeDir, ".git-credentials"),
		filepath.Join(homeDir, ".gitconfig"),
		filepath.Join(homeDir, ".aws", "credentials"),
		filepath.Join(homeDir, ".aws", "config"),
		filepath.Join(homeDir, ".gcloud"),
		filepath.Join(homeDir, ".kube", "config"),
		filepath.Join(homeDir, ".docker", "config.json"),
	}

	denied := make(map[string]string, len(paths))
	for _, p := range paths {
		real, err := resolvePath(p)
		if err != nil {
			continue
		}
		denied[real] = p
	}
	return denied
}

func IsPathSafe(path string, homeDir string) error {
	return IsPathSafeWithWorkspace(path, homeDir, "", false)
}

func IsPathSafeWithWorkspace(path string, homeDir string, workDir string, workspaceOnly bool) error {
	real, err := resolvePath(path)
	if err != nil {
		return fmt.Errorf("cannot resolve path %q: %w", path, err)
	}

	// Workspace boundary check
	if workspaceOnly && workDir != "" {
		if err := IsInWorkspace(real, workDir); err != nil {
			return err
		}
	}

	denied := BuildDeniedPaths(homeDir)
	if display, denied := denied[real]; denied {
		return fmt.Errorf("path %q is write-protected (maps to %q)", path, display)
	}

	if strings.Contains(real, "..") {
		return fmt.Errorf("path %q contains parent directory traversal", path)
	}

	return nil
}

// IsInWorkspace checks whether the resolved path is within the workspace directory.
func IsInWorkspace(resolvedPath, workDir string) error {
	absWS, err := filepath.Abs(workDir)
	if err != nil {
		return fmt.Errorf("cannot resolve workspace directory %q: %w", workDir, err)
	}
	if !strings.HasPrefix(resolvedPath, absWS+string(filepath.Separator)) && resolvedPath != absWS {
		return fmt.Errorf("path %q is outside the workspace directory %q", resolvedPath, absWS)
	}
	return nil
}

func IsWriteSafe(path string, homeDir string) error {
	return IsWriteSafeWithWorkspace(path, homeDir, "", false)
}

func IsWriteSafeWithWorkspace(path string, homeDir string, workDir string, workspaceOnly bool) error {
	real, err := resolvePath(path)
	if err != nil {
		return fmt.Errorf("cannot resolve path %q: %w", path, err)
	}

	// Workspace boundary check
	if workspaceOnly && workDir != "" {
		if err := IsInWorkspace(real, workDir); err != nil {
			return err
		}
	}

	if real == "/" {
		return fmt.Errorf("cannot write to root filesystem")
	}

	if strings.HasPrefix(real, "/etc/") || real == "/etc" {
		return fmt.Errorf("cannot write to system configuration directory: %s", real)
	}

	if strings.HasPrefix(real, "/boot/") || real == "/boot" {
		return fmt.Errorf("cannot write to boot directory: %s", real)
	}

	if strings.HasPrefix(real, "/dev/") || real == "/dev" {
		return fmt.Errorf("cannot write to device directory: %s", real)
	}

	if strings.HasPrefix(real, "/proc/") || real == "/proc" {
		return fmt.Errorf("cannot write to proc filesystem: %s", real)
	}

	if strings.HasPrefix(real, "/sys/") || real == "/sys" {
		return fmt.Errorf("cannot write to sys filesystem: %s", real)
	}

	if strings.HasPrefix(real, "/usr/bin/") || real == "/usr/bin" ||
		strings.HasPrefix(real, "/usr/sbin/") || real == "/usr/sbin" ||
		strings.HasPrefix(real, "/bin/") || real == "/bin" ||
		strings.HasPrefix(real, "/sbin/") || real == "/sbin" {
		return fmt.Errorf("cannot write to system binary directory: %s", real)
	}

	return IsPathSafeWithWorkspace(path, homeDir, workDir, workspaceOnly)
}

func resolvePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return abs, nil
		}
		return "", err
	}
	return real, nil
}
