package evolution

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SkillURLSyncer downloads skills from remote URLs and caches them locally
// so they can be discovered by the standard skill loader. Each URL is expected
// to point to a SKILL.md file.
type SkillURLSyncer struct {
	cacheDir string
	client   *http.Client
	logger   *slog.Logger
}

// NewSkillURLSyncer creates a syncer that caches downloaded skills under
// cacheDir/. The cache directory is created on first sync if it doesn't exist.
func NewSkillURLSyncer(cacheDir string, logger *slog.Logger) *SkillURLSyncer {
	if logger == nil {
		logger = slog.Default()
	}
	return &SkillURLSyncer{
		cacheDir: cacheDir,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

// Sync downloads each URL in urls to the local cache and returns the list of
// cached skill directories that should be added to the skill load paths.
// URLs that fail to download are logged as warnings and skipped.
func (s *SkillURLSyncer) Sync(urls []string) []string {
	if len(urls) == 0 {
		return nil
	}

	var cached []string
	for _, rawURL := range urls {
		dir, err := s.syncOne(rawURL)
		if err != nil {
			s.logger.Warn("skill URL sync failed, skipping",
				"url", rawURL,
				"error", err,
			)
			continue
		}
		if dir != "" {
			cached = append(cached, dir)
		}
	}
	return cached
}

func (s *SkillURLSyncer) syncOne(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}

	// Derive a unique cache key from the URL.
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(rawURL)))[:16]
	skillDir := filepath.Join(s.cacheDir, key)

	// Check if we already have a cached copy (simple existence check;
	// always re-fetch to get latest — lightweight SKILL.md files).
	_ = os.MkdirAll(s.cacheDir, 0755)

	resp, err := s.client.Get(rawURL)
	if err != nil {
		return "", fmt.Errorf("fetch %q: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %q: %s", rawURL, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read %q: %w", rawURL, err)
	}

	// Run security scan before caching.
	findings := ScanContent(string(body), "SKILL.md")
	for _, f := range findings {
		if f.Severity == "critical" || f.Severity == "high" {
			return "", fmt.Errorf(
				"skill blocked by security scan: [%s] %s: %s",
				f.Severity, f.PatternID, f.Description)
		}
	}

	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}

	// Determine skill name from URL stem or frontmatter.
	name := skillNameFromURL(parsed)
	skillFile := filepath.Join(skillDir, "SKILL.md")

	if err := os.WriteFile(skillFile, body, 0644); err != nil {
		return "", fmt.Errorf("write cache: %w", err)
	}

	if name != "" && name != filepath.Base(skillDir) {
		_ = os.WriteFile(filepath.Join(skillDir, ".name"), []byte(name), 0644)
	}

	s.logger.Debug("skill synced from URL", "url", rawURL, "cache", skillDir)
	return skillDir, nil
}

// skillNameFromURL extracts a plausible skill name from a URL path.
// For example "https://example.com/skills/my-skill/SKILL.md" → "my-skill".
func skillNameFromURL(u *url.URL) string {
	path := strings.TrimSuffix(u.Path, "/")
	path = strings.TrimSuffix(path, "/SKILL.md")
	path = strings.TrimSuffix(path, "/skill.md")
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return ""
}

// urlSkillCacheDefault returns the default cache directory under the covo home dir.
func UrlSkillCacheDefault(covoHomeDir string) string {
	return filepath.Join(covoHomeDir, "cache", "skills-url")
}


