package agent

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	CredentialStatusOK        = "ok"
	CredentialStatusExhausted = "exhausted"
	CredentialStatusDead      = "dead"

	AuthTypeAPIKey = "api_key"
	AuthTypeOAuth  = "oauth"

	StrategyFillFirst  = "fill_first"
	StrategyRoundRobin = "round_robin"
	StrategyRandom     = "random"
	StrategyLeastUsed  = "least_used"

	exhaustedTTL401Seconds = 5 * 60
	exhaustedTTL429Seconds = 60 * 60
	exhaustedTTLDefault    = 60 * 60
)

type Credential struct {
	ID          string    `json:"id"`
	Provider    string    `json:"provider"`
	AuthType    string    `json:"auth_type"`
	APIKey      string    `json:"api_key,omitempty"`
	Status      string    `json:"status"`
	AddedAt     time.Time `json:"added_at"`
	LastUsedAt  time.Time `json:"last_used_at"`
	ExhaustedAt time.Time `json:"exhausted_at,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	UsageCount  int64     `json:"usage_count"`
}

type CredentialPool struct {
	mu          sync.RWMutex
	credentials map[string]*Credential
	strategy    string
	nextIndex   int
	provider    string
}

func NewCredentialPool(provider, strategy string) *CredentialPool {
	if strategy == "" {
		strategy = StrategyFillFirst
	}
	return &CredentialPool{
		credentials: make(map[string]*Credential),
		strategy:    strategy,
		provider:    provider,
	}
}

func (p *CredentialPool) AddCredential(cred *Credential) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if cred.ID == "" {
		cred.ID = fmt.Sprintf("%s:%d", p.provider, time.Now().UnixNano())
	}
	if cred.Status == "" {
		cred.Status = CredentialStatusOK
	}
	if cred.AddedAt.IsZero() {
		cred.AddedAt = time.Now()
	}
	p.credentials[cred.ID] = cred
}

func (p *CredentialPool) RemoveCredential(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.credentials, id)
}

func (p *CredentialPool) GetActive() *Credential {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var candidates []*Credential
	for _, c := range p.credentials {
		if p.isReady(c) {
			candidates = append(candidates, c)
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	switch p.strategy {
	case StrategyRoundRobin:
		return p.roundRobin(candidates)
	case StrategyRandom:
		return candidates[rand.Intn(len(candidates))]
	case StrategyLeastUsed:
		return p.leastUsed(candidates)
	default:
		return candidates[0]
	}
}

func (p *CredentialPool) roundRobin(candidates []*Credential) *Credential {
	if p.nextIndex >= len(candidates) {
		p.nextIndex = 0
	}
	cred := candidates[p.nextIndex]
	p.nextIndex = (p.nextIndex + 1) % len(candidates)
	return cred
}

func (p *CredentialPool) leastUsed(candidates []*Credential) *Credential {
	var best *Credential
	for _, c := range candidates {
		if best == nil || c.UsageCount < best.UsageCount {
			best = c
		}
	}
	return best
}

func (p *CredentialPool) isReady(c *Credential) bool {
	if c.Status == CredentialStatusDead {
		return false
	}
	if c.Status == CredentialStatusExhausted {
		if !c.ExhaustedAt.IsZero() && time.Since(c.ExhaustedAt) < p.exhaustedCooldown(c) {
			return false
		}
		c.Status = CredentialStatusOK
	}
	return true
}

func (p *CredentialPool) exhaustedCooldown(c *Credential) time.Duration {
	switch c.LastError {
	case "429":
		return exhaustedTTL429Seconds * time.Second
	case "401", "403":
		return exhaustedTTL401Seconds * time.Second
	default:
		return exhaustedTTLDefault * time.Second
	}
}

func (p *CredentialPool) MarkExhausted(id, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if c, ok := p.credentials[id]; ok {
		c.Status = CredentialStatusExhausted
		c.ExhaustedAt = time.Now()
		c.LastError = reason
	}
}

func (p *CredentialPool) MarkDead(id, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if c, ok := p.credentials[id]; ok {
		c.Status = CredentialStatusDead
		c.LastError = reason
	}
}

func (p *CredentialPool) RecordUsage(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if c, ok := p.credentials[id]; ok {
		c.LastUsedAt = time.Now()
		c.UsageCount++
	}
}

func (p *CredentialPool) LoadFromEnv(envVarPrefix string) {
	poolKey := envVarPrefix + "_POOL"
	poolJSON := os.Getenv(poolKey)
	if poolJSON != "" {
		var creds []Credential
		if err := json.Unmarshal([]byte(poolJSON), &creds); err == nil {
			for i := range creds {
				p.AddCredential(&creds[i])
			}
			return
		}
	}

	primaryKey := envVarPrefix + "_API_KEY"
	if primaryKey == "" {
		primaryKey = "API_KEY"
	}
	if key := os.Getenv(primaryKey); key != "" {
		p.AddCredential(&Credential{
			Provider: p.provider,
			AuthType: AuthTypeAPIKey,
			APIKey:   key,
			Status:   CredentialStatusOK,
		})
	}
}

func (p *CredentialPool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.credentials)
}

func (p *CredentialPool) ActiveCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	count := 0
	for _, c := range p.credentials {
		if p.isReady(c) {
			count++
		}
	}
	return count
}

func (p *CredentialPool) HasActive() bool {
	return p.ActiveCount() > 0
}

// ResetExhausted marks transiently exhausted credentials ready for a fresh
// attempt after a system wake. Permanently dead credentials remain disabled.
func (p *CredentialPool) ResetExhausted() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	reset := 0
	for _, credential := range p.credentials {
		if credential.Status == CredentialStatusExhausted {
			credential.Status = CredentialStatusOK
			credential.ExhaustedAt = time.Time{}
			credential.LastError = ""
			reset++
		}
	}
	return reset
}

func (p *CredentialPool) Load(path string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read credential pool: %w", err)
	}

	var creds []Credential
	if err := json.Unmarshal(data, &creds); err != nil {
		return fmt.Errorf("unmarshal credential pool: %w", err)
	}

	for i := range creds {
		if _, exists := p.credentials[creds[i].ID]; !exists {
			p.credentials[creds[i].ID] = &creds[i]
		}
	}

	return nil
}

func (p *CredentialPool) Save(path string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	creds := make([]Credential, 0, len(p.credentials))
	for _, c := range p.credentials {
		creds = append(creds, *c)
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credential pool: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create credential pool dir: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write credential pool temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename credential pool: %w", err)
	}

	return nil
}

func LoadCredentialPoolFromEnvVars(provider string) *CredentialPool {
	pool := NewCredentialPool(provider, StrategyFillFirst)

	envPrefix := provider
	pool.LoadFromEnv(envPrefix)

	if !pool.HasActive() {
		pool.LoadFromEnv("")
	}

	return pool
}
