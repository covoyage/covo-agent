package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// HookTrustStore records the content digest of a workspace's project hook
// file. When the file changes, previously allowlisted hook commands from that
// workspace must be re-approved.
type HookTrustStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]string // abs workspace -> hex digest
}

func NewHookTrustStore(homeDir string) *HookTrustStore {
	s := &HookTrustStore{
		path:    filepath.Join(homeDir, "hook-trust.json"),
		entries: make(map[string]string),
	}
	s.load()
	return s
}

func (s *HookTrustStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &s.entries)
	if s.entries == nil {
		s.entries = make(map[string]string)
	}
}

func (s *HookTrustStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

// Digest returns the SHA-256 hex digest of data.
func HookFileDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *HookTrustStore) key(workDir string) string {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return workDir
	}
	return abs
}

// Granted returns the last recorded digest for workDir.
func (s *HookTrustStore) Granted(workDir string) (digest string, ok bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	digest, ok = s.entries[s.key(workDir)]
	return digest, ok
}

// Matches reports whether workDir is trusted for digest.
func (s *HookTrustStore) Matches(workDir, digest string) bool {
	got, ok := s.Granted(workDir)
	return ok && digest != "" && got == digest
}

// Grant records digest as the trusted project-hook snapshot for workDir.
func (s *HookTrustStore) Grant(workDir, digest string) error {
	if s == nil {
		return nil
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		abs = workDir
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[abs] = digest
	return s.saveLocked()
}

// Revoke drops trust for workDir.
func (s *HookTrustStore) Revoke(workDir string) error {
	if s == nil {
		return nil
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		abs = workDir
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, abs)
	return s.saveLocked()
}
