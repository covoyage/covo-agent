// Package sqlitefs provides filesystem-aware SQLite journal mode selection.
//
// WAL mode requires coherent shared memory and reliable POSIX locks —
// guarantees that network filesystems (NFS) do not provide. On NFS mounts,
// we fall back to a TRUNCATE journal mode and use per-host database files
// to prevent cross-host corruption.
package sqlitefs

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// JournalMode represents a SQLite journal mode.
type JournalMode int

const (
	// ModeWAL is the default for local filesystems — best performance.
	ModeWAL JournalMode = iota
	// ModeTruncate is used on network filesystems — safe but slower.
	ModeTruncate
)

func (m JournalMode) String() string {
	switch m {
	case ModeWAL:
		return "WAL"
	case ModeTruncate:
		return "TRUNCATE"
	default:
		return "DELETE"
	}
}

// DSN returns the DSN string for this journal mode.
func (m JournalMode) DSNParam() string {
	switch m {
	case ModeWAL:
		return "WAL"
	case ModeTruncate:
		return "TRUNCATE"
	default:
		return "DELETE"
	}
}

// DetectNFS checks if the given path resides on an NFS mount.
// It uses platform-specific methods to determine the filesystem type.
func DetectNFS(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	switch runtime.GOOS {
	case "linux":
		return detectNFSLinux(abs)
	case "darwin":
		return detectNFSDarwin(abs)
	default:
		return false
	}
}

// detectNFSLinux checks /proc/mounts for NFS mounts.
func detectNFSLinux(path string) bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}

	// Find the longest matching mount point
	bestMatch := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		mountPoint := fields[1]
		fsType := fields[2]

		// Check if the path is under this mount point
		if path == mountPoint || strings.HasPrefix(path, mountPoint+"/") {
			if len(mountPoint) > len(bestMatch) {
				bestMatch = mountPoint
				if strings.HasPrefix(fsType, "nfs") {
					return true
				}
			}
		}
	}
	return false
}

// detectNFSDarwin checks if the path is on an NFS mount using mount output.
func detectNFSDarwin(path string) bool {
	// On macOS, we can check the mount table
	// but the simplest approach is to check if the path contains /net/
	// or use the `mount` command
	data, err := os.ReadFile("/etc/fstab")
	if err == nil {
		if strings.Contains(string(data), "nfs") {
			// Check if path matches an NFS entry
			for _, line := range strings.Split(string(data), "\n") {
				if strings.Contains(line, "nfs") && strings.Contains(line, path) {
					return true
				}
			}
		}
	}

	// Check if home directory is in /net/ (automounted NFS)
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(home, "/net/") {
		return true
	}

	return false
}

// EffectiveDBPath returns the database path appropriate for the filesystem.
// On NFS, it appends the hostname to avoid cross-host WAL conflicts.
func EffectiveDBPath(dir string) string {
	dbPath := filepath.Join(dir, "sessions.db")

	if !DetectNFS(dir) {
		return dbPath
	}

	// On NFS: use per-host database file
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "default"
	}

	// Sanitize hostname
	hostname = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, hostname)

	return filepath.Join(dir, fmt.Sprintf("sessions-%s.db", hostname))
}

// EffectiveJournalMode returns the journal mode appropriate for the filesystem.
func EffectiveJournalMode(dir string) JournalMode {
	if DetectNFS(dir) {
		return ModeTruncate
	}
	return ModeWAL
}

// BuildDSN constructs a SQLite DSN string with the appropriate journal mode
// and busy timeout for the filesystem where the database resides.
func BuildDSN(dir string) string {
	dbPath := EffectiveDBPath(dir)
	mode := EffectiveJournalMode(dir)
	busyTimeout := 5000 // 5 seconds

	return fmt.Sprintf("file:%s?_journal_mode=%s&_busy_timeout=%d",
		dbPath, mode.DSNParam(), busyTimeout)
}

// IsNetworkMount is a convenience function that returns true if the path
// is on any network filesystem (NFS, CIFS, etc.).
func IsNetworkMount(path string) bool {
	return DetectNFS(path)
}

// Hostname returns the sanitized hostname for per-host database naming.
func Hostname() string {
	h, err := os.Hostname()
	if err != nil {
		// Fallback to IP-based hostname
		if addrs, err := net.LookupHost("localhost"); err == nil && len(addrs) > 0 {
			h = addrs[0]
		} else {
			h = "default"
		}
	}
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, h)
}
