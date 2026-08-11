package gateway

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	alphabet              = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	codeLength            = 8
	codeTTLSeconds        = 3600
	rateLimitSeconds      = 600
	lockoutSeconds        = 3600
	maxPendingPerPlatform = 3
	maxFailedAttempts     = 5
)

type PairingCode struct {
	Code      string    `json:"code"`
	UserID    string    `json:"user_id"`
	UserName  string    `json:"user_name"`
	CreatedAt time.Time `json:"created_at"`
}

type PairingStore struct {
	mu       sync.RWMutex
	dir      string
	pending  map[string][]PairingCode
	approved map[string]map[string]bool
	rateLim  map[string]time.Time
	failures map[string]int
	lockouts map[string]time.Time
}

func NewPairingStore(homeDir string) *PairingStore {
	dir := filepath.Join(homeDir, "pairing")
	_ = os.MkdirAll(dir, 0700)

	ps := &PairingStore{
		dir:      dir,
		pending:  make(map[string][]PairingCode),
		approved: make(map[string]map[string]bool),
		rateLim:  make(map[string]time.Time),
		failures: make(map[string]int),
		lockouts: make(map[string]time.Time),
	}
	ps.load()
	return ps
}

func (ps *PairingStore) load() {
	ps.loadPending()
	ps.loadApproved()
}

func (ps *PairingStore) loadPending() {
	entries, _ := filepath.Glob(filepath.Join(ps.dir, "*-pending.json"))
	for _, path := range entries {
		platform := filepath.Base(path)
		platform = platform[:len(platform)-len("-pending.json")]
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var codes []PairingCode
		if err := json.Unmarshal(data, &codes); err != nil {
			continue
		}
		valid := make([]PairingCode, 0)
		for _, c := range codes {
			if time.Since(c.CreatedAt) < codeTTLSeconds*time.Second {
				valid = append(valid, c)
			}
		}
		ps.pending[platform] = valid
	}
}

func (ps *PairingStore) loadApproved() {
	entries, _ := filepath.Glob(filepath.Join(ps.dir, "*-approved.json"))
	for _, path := range entries {
		platform := filepath.Base(path)
		platform = platform[:len(platform)-len("-approved.json")]
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var users []string
		if err := json.Unmarshal(data, &users); err != nil {
			continue
		}
		set := make(map[string]bool)
		for _, u := range users {
			set[u] = true
		}
		ps.approved[platform] = set
	}
}

func (ps *PairingStore) writePending(platform string, codes []PairingCode) {
	data, _ := json.MarshalIndent(codes, "", "  ")
	path := filepath.Join(ps.dir, platform+"-pending.json")
	_ = os.WriteFile(path, data, 0600)
}

func (ps *PairingStore) writeApproved(platform string, users map[string]bool) {
	list := make([]string, 0, len(users))
	for u := range users {
		list = append(list, u)
	}
	data, _ := json.MarshalIndent(list, "", "  ")
	path := filepath.Join(ps.dir, platform+"-approved.json")
	_ = os.WriteFile(path, data, 0600)
}

func generateCode() (string, error) {
	code := make([]byte, codeLength)
	for i := range code {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", fmt.Errorf("generate code: %w", err)
		}
		code[i] = alphabet[n.Int64()]
	}
	return string(code), nil
}

func (ps *PairingStore) IsApproved(platform, userID string) bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	if set, ok := ps.approved[platform]; ok {
		return set[userID]
	}
	return false
}

func (ps *PairingStore) IsLockedOut(platform, userID string) bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.isLockedOut(platform, userID)
}

func (ps *PairingStore) isLockedOut(platform, userID string) bool {
	key := platform + ":" + userID
	if lockTime, ok := ps.lockouts[key]; ok {
		if time.Since(lockTime) < lockoutSeconds*time.Second {
			return true
		}
	}
	return false
}

func (ps *PairingStore) IsRateLimited(platform, userID string) bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.isRateLimited(platform, userID)
}

func (ps *PairingStore) isRateLimited(platform, userID string) bool {
	key := platform + ":" + userID
	if lastReq, ok := ps.rateLim[key]; ok {
		if time.Since(lastReq) < rateLimitSeconds*time.Second {
			return true
		}
	}
	return false
}

func (ps *PairingStore) RequestCode(platform, userID, userName string) (string, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	key := platform + ":" + userID

	if ps.isLockedOut(platform, userID) {
		return "", fmt.Errorf("rate limited: too many attempts, try again later")
	}

	if ps.isRateLimited(platform, userID) {
		return "", fmt.Errorf("rate limited: please wait before requesting another code")
	}

	pending := ps.pending[platform]
	if len(pending) >= maxPendingPerPlatform {
		return "", fmt.Errorf("too many pending requests for platform %s", platform)
	}

	code, err := generateCode()
	if err != nil {
		return "", err
	}

	ps.pending[platform] = append(pending, PairingCode{
		Code:      code,
		UserID:    userID,
		UserName:  userName,
		CreatedAt: time.Now(),
	})
	ps.rateLim[key] = time.Now()

	ps.writePending(platform, ps.pending[platform])
	return code, nil
}

func (ps *PairingStore) ApproveCode(platform, code string) (string, bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	pending := ps.pending[platform]
	for i, c := range pending {
		if c.Code == code {
			if time.Since(c.CreatedAt) > codeTTLSeconds*time.Second {
				ps.pending[platform] = append(pending[:i], pending[i+1:]...)
				ps.writePending(platform, ps.pending[platform])
				return "", false
			}

			if ps.approved[platform] == nil {
				ps.approved[platform] = make(map[string]bool)
			}
			ps.approved[platform][c.UserID] = true

			ps.pending[platform] = append(pending[:i], pending[i+1:]...)
			ps.writePending(platform, ps.pending[platform])
			ps.writeApproved(platform, ps.approved[platform])
			return c.UserID, true
		}
	}

	key := platform + ":" + code
	ps.failures[key]++
	if ps.failures[key] >= maxFailedAttempts {
		ps.lockouts[key] = time.Now()
	}

	return "", false
}

func (ps *PairingStore) ListPending(platform string) []PairingCode {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	now := time.Now()
	valid := make([]PairingCode, 0)
	for _, c := range ps.pending[platform] {
		if now.Sub(c.CreatedAt) < codeTTLSeconds*time.Second {
			valid = append(valid, c)
		}
	}
	return valid
}

func (ps *PairingStore) ListApproved(platform string) []string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	list := make([]string, 0, len(ps.approved[platform]))
	for u := range ps.approved[platform] {
		list = append(list, u)
	}
	return list
}

func (ps *PairingStore) RemoveApproved(platform, userID string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if set, ok := ps.approved[platform]; ok {
		delete(set, userID)
		ps.writeApproved(platform, ps.approved[platform])
	}
}

func (ps *PairingStore) Cleanup() {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	now := time.Now()
	for platform, codes := range ps.pending {
		valid := make([]PairingCode, 0)
		for _, c := range codes {
			if now.Sub(c.CreatedAt) < codeTTLSeconds*time.Second {
				valid = append(valid, c)
			}
		}
		ps.pending[platform] = valid
		ps.writePending(platform, valid)
	}

	for key, lockTime := range ps.lockouts {
		if now.Sub(lockTime) >= lockoutSeconds*time.Second {
			delete(ps.lockouts, key)
		}
	}
}
