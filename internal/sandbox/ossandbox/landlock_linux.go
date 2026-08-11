//go:build linux

package ossandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Landlock access rights.
// Read = execute + read-file + read-dir
// Write = all write/create/remove operations
const (
	landlockAccessFSRead = unix.LANDLOCK_ACCESS_FS_EXECUTE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR

	landlockAccessFSWrite = unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
		unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM

	// REFER (ABI v2, Linux 5.19+) and TRUNCATE (ABI v2) and IOCTL_DEV (ABI v3)
	// are probed at runtime for compatibility.
	landlockAccessFSRefer    = unix.LANDLOCK_ACCESS_FS_REFER
	landlockAccessFSTruncate = unix.LANDLOCK_ACCESS_FS_TRUNCATE

	landlockAccessFSAll = landlockAccessFSRead | landlockAccessFSWrite |
		landlockAccessFSRefer | landlockAccessFSTruncate
)

// platformSupportInfo checks if Linux Landlock is available.
func platformSupportInfo() (supported bool, details string) {
	supportedAccess := probeSupportedAccess()
	if supportedAccess == 0 {
		return false, "Landlock not available (need Linux 5.13+ with CONFIGLandlock)"
	}
	return true, fmt.Sprintf("Linux Landlock (access mask=0x%x)", supportedAccess)
}

// probeSupportedAccess tries creating a ruleset with progressively smaller
// access masks to determine the kernel's Landlock ABI version.
// Returns 0 if Landlock is not available at all.
func probeSupportedAccess() uint64 {
	// Try full ABI (v3: includes Refer, Truncate, IoctlDev)
	attr := unix.LandlockRulesetAttr{Access_fs: landlockAccessFSAll}
	fd, err := landlockCreateRuleset(&attr)
	if err == nil {
		unix.Close(fd)
		return landlockAccessFSAll
	}

	// Try ABI v1 (no Refer/Truncate)
	attr = unix.LandlockRulesetAttr{Access_fs: landlockAccessFSRead | landlockAccessFSWrite}
	fd, err = landlockCreateRuleset(&attr)
	if err == nil {
		unix.Close(fd)
		return landlockAccessFSRead | landlockAccessFSWrite
	}

	return 0
}

// applySandbox applies the Linux Landlock sandbox to the current process.
// This is IRREVERSIBLE — once applied, the restrictions cannot be removed.
func applySandbox(profile *SandboxProfile, workspace string) error {
	supportedAccess := probeSupportedAccess()
	if supportedAccess == 0 {
		return fmt.Errorf("landlock not supported by kernel")
	}

	// Create the ruleset with the kernel-supported access rights
	attr := unix.LandlockRulesetAttr{Access_fs: supportedAccess}
	rulesetFD, err := landlockCreateRuleset(&attr)
	if err != nil {
		return fmt.Errorf("landlock_create_ruleset: %w", err)
	}
	defer unix.Close(rulesetFD)

	// Cap access masks to what the kernel supports
	readAccess := landlockAccessFSRead & supportedAccess
	writeAccess := landlockAccessFSWrite & supportedAccess
	allAccess := supportedAccess

	// If default read is enabled, grant read access to the entire filesystem
	if profile.DefaultRead {
		if err := addPathRule(rulesetFD, "/", readAccess); err != nil {
			return fmt.Errorf("add default read rule for /: %w", err)
		}
	}

	// Grant read-only access to explicit read-only paths
	for _, p := range profile.ReadOnlyPaths {
		resolved := resolvePath(p, workspace)
		if _, err := os.Stat(resolved); err != nil {
			continue
		}
		if err := addPathRule(rulesetFD, resolved, readAccess); err != nil {
			return fmt.Errorf("add read rule for %s: %w", resolved, err)
		}
	}

	// Grant read-write access to read-write paths
	for _, p := range profile.ReadWritePaths {
		resolved := resolvePath(p, workspace)
		if err := os.MkdirAll(resolved, 0755); err != nil {
			continue
		}
		if err := addPathRule(rulesetFD, resolved, allAccess); err != nil {
			return fmt.Errorf("add read-write rule for %s: %w", resolved, err)
		}
	}

	// Allow essential device files
	for _, dev := range []string{"/dev/null", "/dev/urandom", "/dev/random", "/dev/zero"} {
		if _, err := os.Stat(dev); err == nil {
			_ = addPathRule(rulesetFD, dev, readAccess|writeAccess) // non-fatal
		}
	}

	// Allow /dev/pts for PTY slaves
	if _, err := os.Stat("/dev/pts"); err == nil {
		_ = addPathRule(rulesetFD, "/dev/pts", readAccess|writeAccess) // non-fatal
	}

	// Deny paths: Landlock has no explicit deny. With default_read, denied
	// paths remain readable via the "/" rule. True read-deny requires bwrap.
	if len(profile.DenyPaths) > 0 && profile.DefaultRead {
		fmt.Fprintf(os.Stderr, "[sandbox] warning: deny paths are best-effort with default_read profiles on Linux\n")
	}

	// Apply the ruleset — IRREVERSIBLE!
	if err := landlockRestrictSelf(rulesetFD); err != nil {
		return fmt.Errorf("landlock_restrict_self: %w", err)
	}

	return nil
}

// addPathRule adds a path-beneath rule to the Landlock ruleset.
func addPathRule(rulesetFD int, path string, access uint64) error {
	fd, err := syscall.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer syscall.Close(fd)

	pathAttr := unix.LandlockPathBeneathAttr{
		Allowed_access: access,
		Parent_fd:      int32(fd),
	}

	return landlockAddRule(rulesetFD, unix.LANDLOCK_RULE_PATH_BENEATH, unsafe.Pointer(&pathAttr))
}

// ── Raw syscall wrappers (architecture-aware via unix.SYS_LANDLOCK_*) ──

func landlockCreateRuleset(attr *unix.LandlockRulesetAttr) (int, error) {
	r1, _, errno := unix.Syscall(
		unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(attr)),
		unsafe.Sizeof(*attr),
		0, // flags
	)
	if errno != 0 {
		return -1, errno
	}
	return int(r1), nil
}

func landlockAddRule(rulesetFD int, ruleType int, attr unsafe.Pointer) error {
	_, _, errno := unix.Syscall6(
		unix.SYS_LANDLOCK_ADD_RULE,
		uintptr(rulesetFD),
		uintptr(ruleType),
		uintptr(attr),
		0, // flags
		0,
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func landlockRestrictSelf(rulesetFD int) error {
	_, _, errno := unix.Syscall(
		unix.SYS_LANDLOCK_RESTRICT_SELF,
		uintptr(rulesetFD),
		0, // flags
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

// resolvePath resolves a path relative to the workspace if not absolute.
func resolvePath(p, workspace string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	resolved := filepath.Join(workspace, p)
	return filepath.Clean(resolved)
}
