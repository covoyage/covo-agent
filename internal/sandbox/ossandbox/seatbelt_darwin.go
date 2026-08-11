//go:build darwin

package ossandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// platformSupportInfo checks if macOS Seatbelt is available.
func platformSupportInfo() (supported bool, details string) {
	return true, "macOS Seatbelt via sandbox_init"
}

// applySandbox applies the macOS Seatbelt sandbox to the current process
// using the private sandbox_init() API. This is IRREVERSIBLE.
func applySandbox(profile *SandboxProfile, workspace string) error {
	policy := generateSeatbeltProfile(profile, workspace)

	// sandbox_init with SANDBOX_NAMED_EXTERNAL requires a file path.
	tmpFile, err := os.CreateTemp("", "covo-sandbox-*.sb")
	if err != nil {
		return fmt.Errorf("create temp policy file: %w", err)
	}
	policyPath := tmpFile.Name()
	defer os.Remove(policyPath)

	if _, err := tmpFile.WriteString(policy); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write policy file: %w", err)
	}
	tmpFile.Close()

	if err := sandboxInitInProcess(policyPath); err != nil {
		return fmt.Errorf("apply seatbelt: %w", err)
	}

	return nil
}

// generateSeatbeltProfile generates a Seatbelt policy string from the sandbox profile.
//
// Design decision: network is ALWAYS allowed for the agent process because it
// needs LLM API access. Child process network restriction (restrict_network)
// is enforced at the child-execution layer, not by the kernel sandbox. This
// matches the sandbox approach: "Network is left open at the process level
// (agent needs LLM API); child network is blocked per-subprocess via seccomp."
func generateSeatbeltProfile(profile *SandboxProfile, workspace string) string {
	var sb strings.Builder

	sb.WriteString(";; Auto-generated covo-agent Seatbelt profile\n")
	sb.WriteString(";; Profile: " + profile.Name + "\n")
	sb.WriteString(";; Workspace: " + workspace + "\n\n")

	// Version
	sb.WriteString("(version 1)\n\n")

	// Deny by default — everything not explicitly allowed is blocked
	sb.WriteString("(deny default)\n\n")

	// ── Process & IPC operations (always needed) ──
	sb.WriteString(";; Process, thread, and IPC operations\n")
	sb.WriteString("(allow process*)\n")
	sb.WriteString("(allow process-info*)\n")
	sb.WriteString("(allow thread*)\n")
	sb.WriteString("(allow signal (target self))\n")
	sb.WriteString("(allow task-name-port*)\n")
	sb.WriteString("(allow sysctl-read)\n")
	sb.WriteString("(allow mach-lookup)\n")
	sb.WriteString("(allow mach-register)\n")
	sb.WriteString("(allow ipc-posix*)\n")
	sb.WriteString("(allow ipc-sysv*)\n")
	sb.WriteString("(allow system-socket)\n")
	sb.WriteString("(allow socket*)\n")
	sb.WriteString("(allow bind)\n\n")

	// ── Network (always allowed — agent needs LLM API) ──
	// Child process network restriction is handled separately.
	sb.WriteString(";; Network access (agent process always needs LLM API)\n")
	sb.WriteString("(allow network*)\n")
	sb.WriteString("(allow network-outbound*)\n")
	sb.WriteString("(allow network-inbound*)\n\n")

	// ── File system: metadata (always needed for stat, access, etc.) ──
	sb.WriteString(";; File metadata (stat, access, etc.)\n")
	sb.WriteString("(allow file-read-metadata)\n")
	sb.WriteString("(allow file-ioctl)\n\n")

	// ── File system: read access ──
	if profile.DefaultRead {
		sb.WriteString(";; Default read access to entire filesystem\n")
		sb.WriteString("(allow file-read*)\n\n")
	} else {
		// Explicit read-only paths (strict mode)
		sb.WriteString(";; Explicit read-only paths\n")
		// Always allow reading system essentials even in strict mode
		sb.WriteString("(allow file-read* (subpath \"/usr\"))\n")
		sb.WriteString("(allow file-read* (subpath \"/bin\"))\n")
		sb.WriteString("(allow file-read* (subpath \"/sbin\"))\n")
		sb.WriteString("(allow file-read* (subpath \"/lib\"))\n")
		sb.WriteString("(allow file-read* (subpath \"/etc\"))\n")
		sb.WriteString("(allow file-read* (subpath \"/dev\"))\n")
		sb.WriteString("(allow file-read* (subpath \"/private/etc\"))\n")
		sb.WriteString("(allow file-read* (subpath \"/System\"))\n")
		sb.WriteString("(allow file-read* (subpath \"/Library\"))\n")
		for _, p := range profile.ReadOnlyPaths {
			resolved := resolvePath(p, workspace)
			sb.WriteString(fmt.Sprintf("(allow file-read* (subpath %q))\n", resolved))
		}
		sb.WriteString("\n")
	}

	// ── File system: write access ──
	sb.WriteString(";; Read-write paths\n")
	for _, p := range profile.ReadWritePaths {
		resolved := resolvePath(p, workspace)
		sb.WriteString(fmt.Sprintf("(allow file-read* (subpath %q))\n", resolved))
		sb.WriteString(fmt.Sprintf("(allow file-write* (subpath %q))\n", resolved))
	}
	sb.WriteString("\n")

	// ── Essential device files ──
	sb.WriteString(";; Essential device files\n")
	sb.WriteString("(allow file-read* (literal \"/dev/null\"))\n")
	sb.WriteString("(allow file-write* (literal \"/dev/null\"))\n")
	sb.WriteString("(allow file-read* (literal \"/dev/urandom\"))\n")
	sb.WriteString("(allow file-read* (literal \"/dev/random\"))\n")
	sb.WriteString("(allow file-read* (literal \"/dev/zero\"))\n")
	sb.WriteString("(allow file-write* (literal \"/dev/zero\"))\n")
	sb.WriteString("(allow file-read* (literal \"/dev/tty\"))\n")
	sb.WriteString("(allow file-write* (literal \"/dev/tty\"))\n\n")

	// ── Temp directory ──
	sb.WriteString(";; Temp directory\n")
	sb.WriteString(fmt.Sprintf("(allow file-read* (subpath %q))\n", os.TempDir()))
	sb.WriteString(fmt.Sprintf("(allow file-write* (subpath %q))\n\n", os.TempDir()))

	// ── DNS and TLS infrastructure (needed for network) ──
	sb.WriteString(";; DNS and TLS infrastructure\n")
	sb.WriteString("(allow file-read* (literal \"/etc/resolv.conf\"))\n")
	sb.WriteString("(allow file-read* (literal \"/etc/hosts\"))\n")
	sb.WriteString("(allow file-read* (literal \"/etc/hostconfig\"))\n")
	sb.WriteString("(allow file-read* (subpath \"/etc/ssl\"))\n")
	sb.WriteString("(allow file-read* (subpath \"/private/etc/ssl\"))\n")
	// macOS keychain (for TLS certificate validation)
	homeDir, _ := os.UserHomeDir()
	if homeDir != "" {
		sb.WriteString(fmt.Sprintf("(allow file-read* (subpath %q))\n",
			filepath.Join(homeDir, "Library", "Keychains")))
		sb.WriteString(fmt.Sprintf("(allow file-read* (subpath %q))\n",
			filepath.Join(homeDir, "Library", "Preferences")))
	}
	sb.WriteString("\n")

	// ── Deny paths (overrides everything) ──
	if len(profile.DenyPaths) > 0 {
		sb.WriteString(";; Denied paths (read + write denied)\n")
		for _, p := range profile.DenyPaths {
			resolved := resolvePath(p, workspace)
			sb.WriteString(fmt.Sprintf("(deny file-read* (subpath %q))\n", resolved))
			sb.WriteString(fmt.Sprintf("(deny file-write* (subpath %q))\n", resolved))
		}
		sb.WriteString("\n")
	}

	// ── Always-denied system paths (write) ──
	sb.WriteString(";; Always denied: sensitive system paths (write)\n")
	sb.WriteString("(deny file-write* (subpath \"/System\"))\n")
	sb.WriteString("(deny file-write* (subpath \"/usr\"))\n")
	sb.WriteString("(deny file-write* (subpath \"/bin\"))\n")
	sb.WriteString("(deny file-write* (subpath \"/sbin\"))\n")
	sb.WriteString("(deny file-write* (subpath \"/private/etc\"))\n")
	// Deny writes to SSH keys, AWS credentials, etc.
	if homeDir != "" {
		sb.WriteString(fmt.Sprintf("(deny file-write* (subpath %q))\n",
			filepath.Join(homeDir, ".ssh")))
		sb.WriteString(fmt.Sprintf("(deny file-write* (subpath %q))\n",
			filepath.Join(homeDir, ".aws")))
	}
	sb.WriteString("\n")

	return sb.String()
}

// resolvePath resolves a path relative to the workspace if not absolute.
func resolvePath(p, workspace string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	resolved := filepath.Join(workspace, p)
	return filepath.Clean(resolved)
}
