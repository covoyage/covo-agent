package evolution

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	mcpOSVCacheTTL      = time.Hour
	mcpOSVCacheMax      = 256
	mcpOSVMaxAdvisories = 3
)

// mcpOSVEndpoint is the OSV query URL. Tests may override it.
var mcpOSVEndpoint = "https://api.osv.dev/v1/query"

type mcpOSVCacheEntry struct {
	until time.Time
	block string // empty means clean
}

var (
	mcpOSVMu    sync.Mutex
	mcpOSVCache = make(map[string]mcpOSVCacheEntry)
)

func resetMCPOSVCache() {
	mcpOSVMu.Lock()
	defer mcpOSVMu.Unlock()
	mcpOSVCache = make(map[string]mcpOSVCacheEntry)
}

// CheckMCPCommandMalware blocks stdio MCP launches whose inferred npx/uvx/pipx
// package has OSV MAL-* advisories. Network errors, unknown launchers, and
// unparseable args fail open (return nil). Regular CVEs are ignored.
func CheckMCPCommandMalware(command string, args []string) error {
	eco, name, version, ok := ParseMCPLaunchPackage(command, args)
	if !ok {
		return nil
	}
	key := eco + "|" + name + "|" + version
	if blocked, hit := mcpOSVCacheGet(key); hit {
		if blocked != "" {
			return fmt.Errorf("%s", blocked)
		}
		return nil
	}
	ids, summaries, err := queryOSVMalware(name, eco, version)
	if err != nil {
		return nil
	}
	var blocked string
	if len(ids) > 0 {
		blocked = fmt.Sprintf(
			"mcp spawn blocked: package %q (%s) has known malware advisories: %s. Details: %s",
			name, eco, strings.Join(ids, ", "), strings.Join(summaries, "; "),
		)
	}
	mcpOSVCachePut(key, blocked)
	if blocked != "" {
		return fmt.Errorf("%s", blocked)
	}
	return nil
}

// ParseMCPLaunchPackage extracts (ecosystem, name, version) from an MCP stdio
// command. ok is false when the launcher is not npx/uvx/pipx or no package
// token is present.
func ParseMCPLaunchPackage(command string, args []string) (ecosystem, name, version string, ok bool) {
	ecosystem = inferMCPEcosystem(command)
	if ecosystem == "" {
		return "", "", "", false
	}
	token := mcpPackageToken(args)
	if token == "" {
		return "", "", "", false
	}
	switch ecosystem {
	case "npm":
		name, version = parseNPMPackage(token)
	case "PyPI":
		name, version = parsePyPIPackage(token)
	default:
		return "", "", "", false
	}
	if name == "" {
		return "", "", "", false
	}
	return ecosystem, name, version, true
}

func inferMCPEcosystem(command string) string {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(command)))
	base = strings.TrimSuffix(base, ".exe")
	base = strings.TrimSuffix(base, ".cmd")
	base = strings.TrimSuffix(base, ".bat")
	switch base {
	case "npx":
		return "npm"
	case "uvx", "pipx":
		return "PyPI"
	default:
		return ""
	}
}

func mcpPackageToken(args []string) string {
	takeNext := false
	for _, arg := range args {
		if takeNext {
			return strings.TrimSpace(arg)
		}
		if arg == "--" {
			return ""
		}
		if arg == "--package" || arg == "-p" {
			takeNext = true
			continue
		}
		if strings.HasPrefix(arg, "--package=") {
			return strings.TrimSpace(strings.TrimPrefix(arg, "--package="))
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return strings.TrimSpace(arg)
	}
	return ""
}

func parseNPMPackage(token string) (name, version string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", ""
	}
	if strings.HasPrefix(token, "@") {
		slash := strings.Index(token, "/")
		if slash < 0 {
			return token, ""
		}
		rest := token[slash+1:]
		if at := strings.Index(rest, "@"); at >= 0 {
			ver := rest[at+1:]
			if ver == "latest" {
				ver = ""
			}
			return token[:slash+1+at], ver
		}
		return token, ""
	}
	if at := strings.LastIndex(token, "@"); at > 0 {
		ver := token[at+1:]
		if ver == "latest" {
			ver = ""
		}
		return token[:at], ver
	}
	return token, ""
}

func parsePyPIPackage(token string) (name, version string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", ""
	}
	if i := strings.Index(token, "["); i >= 0 {
		if j := strings.Index(token[i:], "]"); j >= 0 {
			token = token[:i] + token[i+j+1:]
		}
	}
	if i := strings.Index(token, "=="); i >= 0 {
		return token[:i], token[i+2:]
	}
	return token, ""
}

func queryOSVMalware(name, ecosystem, version string) (ids, summaries []string, err error) {
	payload := map[string]any{
		"package": map[string]string{
			"name":      name,
			"ecosystem": ecosystem,
		},
	}
	if version != "" {
		payload["version"] = version
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, mcpOSVEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "covo-agent-mcp-osv/1.0")

	resp, err := osvClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("osv: api returned %d", resp.StatusCode)
	}
	vulns := parseSingleResult(raw)
	for _, v := range vulns {
		if !strings.HasPrefix(v.ID, "MAL-") {
			continue
		}
		ids = append(ids, v.ID)
		sum := strings.TrimSpace(v.Summary)
		if sum == "" {
			sum = v.ID
		}
		if len(sum) > 100 {
			sum = sum[:100]
		}
		summaries = append(summaries, sum)
		if len(ids) >= mcpOSVMaxAdvisories {
			break
		}
	}
	return ids, summaries, nil
}

func mcpOSVCacheGet(key string) (blocked string, hit bool) {
	mcpOSVMu.Lock()
	defer mcpOSVMu.Unlock()
	ent, ok := mcpOSVCache[key]
	if !ok || time.Now().After(ent.until) {
		if ok {
			delete(mcpOSVCache, key)
		}
		return "", false
	}
	return ent.block, true
}

func mcpOSVCachePut(key, blocked string) {
	mcpOSVMu.Lock()
	defer mcpOSVMu.Unlock()
	if len(mcpOSVCache) >= mcpOSVCacheMax {
		now := time.Now()
		for k, ent := range mcpOSVCache {
			if now.After(ent.until) {
				delete(mcpOSVCache, k)
			}
		}
		if len(mcpOSVCache) >= mcpOSVCacheMax {
			mcpOSVCache = make(map[string]mcpOSVCacheEntry)
		}
	}
	mcpOSVCache[key] = mcpOSVCacheEntry{until: time.Now().Add(mcpOSVCacheTTL), block: blocked}
}
