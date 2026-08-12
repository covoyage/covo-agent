package agent

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------------------
// Blocked IP ranges – pre-computed at init to avoid repeated parsing at
// request time.  Every CIDR here prevents SSRF / internal-network access.
// ---------------------------------------------------------------------------

var blockedCIDRs []netip.Prefix

// blockedSchemes are URL schemes that an agent must never fetch.
var blockedSchemes = map[string]bool{
	"file":   true,
	"ftp":    true,
	"dict":   true,
	"gopher": true,
	"ldap":   true,
	"tftp":   true,
	"jar":    true,
}

// blockedHostSuffixes – hostnames ending with any of these are considered
// internal / private and must not be reached.
var blockedHostSuffixes = []string{
	".local",
	".internal",
	".localhost",
	".corp",
	".intranet",
}

// blockedExactHosts – hostnames that match exactly are always blocked.
var blockedExactHosts = map[string]bool{
	"localhost":                true,
	"127.0.0.1":                true,
	"0.0.0.0":                  true,
	"metadata.google.internal": true,
}

// sensitivePathPrefixes – absolute paths (or prefixes) that are considered
// sensitive.  Filled in init() so we can incorporate the real home directory.
var sensitivePathPrefixes []string

func init() {
	// --- blocked CIDRs --------------------------------------------------
	cidrStrings := []string{
		// Loopback (IPv4 + IPv6)
		"127.0.0.0/8",
		"::1/128",
		// "This host on this network" – treat the single address as blocked.
		"0.0.0.0/32",
		// RFC 1918 private address space
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		// Link-local (includes AWS metadata endpoint 169.254.169.254)
		"169.254.0.0/16",
		// Common Docker bridge networks (172.17.0.0/16 – 172.30.0.0/16)
		"172.17.0.0/16",
		"172.18.0.0/16",
		"172.19.0.0/16",
		"172.20.0.0/16",
		"172.21.0.0/16",
		"172.22.0.0/16",
		"172.23.0.0/16",
		"172.24.0.0/16",
		"172.25.0.0/16",
		"172.26.0.0/16",
		"172.27.0.0/16",
		"172.28.0.0/16",
		"172.29.0.0/16",
		"172.30.0.0/16",
		// Alibaba Cloud instance metadata service
		"100.100.100.200/32",
	}

	blockedCIDRs = make([]netip.Prefix, 0, len(cidrStrings))
	for _, c := range cidrStrings {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			panic(fmt.Sprintf("agent: invalid built-in blocked CIDR %q: %v", c, err))
		}
		blockedCIDRs = append(blockedCIDRs, p)
	}

	// --- sensitive path prefixes ----------------------------------------
	homeDir, _ := os.UserHomeDir() // ignore error; homeDir will be ""

	systemPaths := []string{
		"/etc/passwd",
		"/etc/shadow",
		"/etc/sudoers",
		"/etc/ssl/private",
		"/root",
		"/var/run/docker.sock",
		"/proc",
		"/sys",
		"/dev",
	}
	sensitivePathPrefixes = append(sensitivePathPrefixes, systemPaths...)

	// Per-user credential directories – anchored to the real home dir.
	if homeDir != "" {
		userCredDirs := []string{
			".ssh",
			".aws",
			".gnupg",
			".kube",
			".docker",
			".covo-agent",
			".hermes",
			".qclaw",
			".claude",
			".codex",
		}
		for _, d := range userCredDirs {
			sensitivePathPrefixes = append(sensitivePathPrefixes,
				filepath.Join(homeDir, d),
			)
		}
	}
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// ValidateURL checks whether rawURL is safe to fetch.
//
// The following are blocked:
//   - Dangerous URL schemes (file, ftp, dict, gopher, ldap, tftp, jar)
//   - Hostnames that match internal / private patterns
//   - Hostnames that resolve to private / loopback / blocked IP addresses
//
// Returns nil when the URL is allowed; otherwise an error describing the
// reason (the error message does NOT contain the full URL).
func ValidateURL(rawURL string) error {
	if rawURL == "" {
		return errors.New("empty URL")
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("cannot parse URL: %w", err)
	}

	// --- scheme ---------------------------------------------------------
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "" {
		return errors.New("URL has no scheme")
	}
	if blockedSchemes[scheme] {
		return fmt.Errorf("blocked URL scheme")
	}
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported URL scheme")
	}

	// --- host -----------------------------------------------------------
	host := parsed.Hostname()
	if host == "" {
		return errors.New("URL has no host")
	}

	hostLower := strings.ToLower(host)

	// Exact-match blocklist
	if blockedExactHosts[hostLower] {
		return fmt.Errorf("blocked host")
	}

	// Suffix-match blocklist (e.g. *.internal, *.corp)
	for _, suffix := range blockedHostSuffixes {
		if strings.HasSuffix(hostLower, suffix) {
			return fmt.Errorf("blocked host pattern")
		}
	}

	// If the host is already an IP literal, check it immediately.
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
		if isBlockedIP(ip.Unmap()) {
			return fmt.Errorf("blocked IP address")
		}
		return nil
	}

	// Resolve the hostname and check every returned IP.
	ips, resolveErr := net.LookupIP(host)
	if resolveErr != nil {
		return fmt.Errorf("cannot resolve host: %w", resolveErr)
	}
	for _, raw := range ips {
		addr, ok := netip.AddrFromSlice(raw)
		if !ok {
			continue
		}
		if isBlockedIP(addr.Unmap()) {
			return fmt.Errorf("host resolves to a blocked IP address")
		}
	}

	return nil
}

// IsPrivateIP reports whether ipStr represents an IP address that falls
// inside any of the blocked (private / internal / loopback) ranges.
//
// It is safe to call independently – no URL parsing or DNS resolution is
// performed.
func IsPrivateIP(ipStr string) bool {
	addr, err := netip.ParseAddr(ipStr)
	if err != nil {
		return false
	}
	return isBlockedIP(addr.Unmap())
}

// ValidatePath ensures that relPath, resolved relative to baseDir, stays
// within baseDir and does not point at a sensitive location.
//
// baseDir must be an absolute path.  relPath must be relative.
//
// On success the cleaned, absolute path is returned.
// On failure an error is returned (path traversal, sensitive file, empty
// input, etc.).
func ValidatePath(baseDir, relPath string) (string, error) {
	if baseDir == "" {
		return "", errors.New("base directory is empty")
	}
	if relPath == "" {
		return "", errors.New("relative path is empty")
	}

	baseDir = filepath.Clean(baseDir)
	if !filepath.IsAbs(baseDir) {
		return "", errors.New("base directory must be an absolute path")
	}

	cleaned := filepath.Clean(relPath)

	// Reject input that is already absolute – the caller promised relative.
	// On Windows a volume-relative path (e.g. "\etc\passwd") is not reported
	// absolute by filepath.IsAbs, so also reject any leading separator.
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, string(filepath.Separator)) {
		return "", errors.New("relative path must not be absolute")
	}

	// After cleaning, anything that still starts with ".." is escaping.
	if strings.HasPrefix(cleaned, "..") {
		return "", errors.New("path traversal detected")
	}

	// Join and clean again for absolute resolution.
	fullPath := filepath.Join(baseDir, cleaned)
	fullPath = filepath.Clean(fullPath)

	// Enforce that the result is strictly under baseDir.
	// filepath.Clean strips trailing separators, so compare with a
	// separator appended to catch directory-prefix mismatches (e.g.
	// /var/app vs /var/app-other).
	if fullPath != baseDir &&
		!strings.HasPrefix(fullPath, baseDir+string(filepath.Separator)) {
		return "", errors.New("path escapes base directory")
	}

	if IsSensitivePath(fullPath) {
		return "", errors.New("path references a sensitive location")
	}

	return fullPath, nil
}

// IsSensitivePath reports whether path points at a known-sensitive system
// file, SSH key, credential directory, agent configuration, or similar.
//
// Matching is done in forward-slash form so the Unix-style patterns apply
// regardless of the host OS separator.
func IsSensitivePath(path string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	const sep = "/"

	// Exact or prefix match against the pre-computed list.
	for _, prefix := range sensitivePathPrefixes {
		prefix = filepath.ToSlash(prefix)
		if path == prefix {
			return true
		}
		if strings.HasPrefix(path, prefix+sep) {
			return true
		}
	}

	// --- Generic home-directory patterns (/home/<user>/..., /Users/<user>/...) ---
	parts := strings.Split(path, "/")
	// On Unix the first part after cleaning is "" (leading /).
	if len(parts) >= 4 && parts[1] != "" {
		top := parts[1]    // "home" or "Users"
		dotDir := parts[3] // e.g. ".ssh", ".aws"
		credentialSets := map[string]bool{
			".ssh":        true,
			".aws":        true,
			".gnupg":      true,
			".kube":       true,
			".docker":     true,
			".covo-agent": true,
			".hermes":     true,
			".qclaw":      true,
			".claude":     true,
			".codex":      true,
		}
		if (top == "home" || top == "Users") && credentialSets[dotDir] {
			return true
		}
	}

	// --- .env files in home directories ---
	if filepath.Base(path) == ".env" && len(parts) >= 3 && parts[1] != "" {
		top := parts[1]
		if top == "home" || top == "Users" {
			return true
		}
		// Also catch the current user's home-directory .env (which is
		// already in sensitivePathPrefixes through init, but double-check
		// in case the home dir wasn't available at init time).
		if home, err := os.UserHomeDir(); err == nil {
			if path == filepath.ToSlash(filepath.Join(home, ".env")) {
				return true
			}
		}
	}

	return false
}

// SanitizeURL returns a redacted version of rawURL that is safe for display
// and logging.  It strips:
//
//   - userinfo (credentials like user:password@)
//   - query parameters
//   - fragments
//
// Only scheme + host + path are preserved.
// If the URL cannot be parsed the string "[invalid URL]" is returned.
func SanitizeURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "[invalid URL]"
	}

	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.RawFragment = ""
	parsed.ForceQuery = false

	return parsed.String()
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// isBlockedIP returns true when ip is contained by any of the pre-computed
// blocked CIDR prefixes.
func isBlockedIP(ip netip.Addr) bool {
	for i := range blockedCIDRs {
		if blockedCIDRs[i].Contains(ip) {
			return true
		}
	}
	return false
}
