package tools

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type PermissionDecision int

const (
	PermAllow PermissionDecision = iota
	PermDeny
	PermOnce
)

type PermissionRequest struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Tool      string    `json:"tool"`
	Patterns  []string  `json:"patterns"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
	Result    chan PermissionDecision
}

type PermissionRule struct {
	Tool    string `json:"tool"`
	Pattern string `json:"pattern"`
	Action  string `json:"action"`
}

type PermissionDeferred struct {
	mu      sync.RWMutex
	pending map[string]*PermissionRequest
	rules   []PermissionRule
}

func NewPermissionDeferred() *PermissionDeferred {
	return &PermissionDeferred{pending: make(map[string]*PermissionRequest)}
}

func (d *PermissionDeferred) AddRule(rule PermissionRule) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rules = append(d.rules, rule)
}

func (d *PermissionDeferred) Ask(sessionID, tool string, patterns []string, reason string, timeout time.Duration) (PermissionDecision, error) {
	d.mu.RLock()
	var matchedRule *PermissionRule
	for i := len(d.rules) - 1; i >= 0; i-- {
		r := d.rules[i]
		if r.Tool == tool {
			for _, p := range patterns {
				if matchPattern(p, r.Pattern) {
					matchedRule = &r
					break
				}
			}
			if matchedRule != nil {
				break
			}
		}
	}
	d.mu.RUnlock()

	if matchedRule != nil {
		switch matchedRule.Action {
		case "allow":
			return PermAllow, nil
		case "deny":
			return PermDeny, nil
		}
	}

	req := &PermissionRequest{
		ID:        fmt.Sprintf("perm-%d", time.Now().UnixNano()),
		SessionID: sessionID, Tool: tool, Patterns: patterns, Reason: reason,
		CreatedAt: time.Now(),
		Result:    make(chan PermissionDecision, 1),
	}

	d.mu.Lock()
	d.pending[req.ID] = req
	d.mu.Unlock()

	select {
	case decision := <-req.Result:
		return decision, nil
	case <-time.After(timeout):
		d.Reply(req.ID, PermDeny)
		return PermDeny, fmt.Errorf("permission timeout")
	}
}

func (d *PermissionDeferred) Reply(id string, decision PermissionDecision) {
	d.mu.Lock()
	req, ok := d.pending[id]
	if ok {
		delete(d.pending, id)
	}
	d.mu.Unlock()
	if ok {
		req.Result <- decision
	}
}

func (d *PermissionDeferred) PendingRequests() []*PermissionRequest {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var reqs []*PermissionRequest
	for _, r := range d.pending {
		reqs = append(reqs, r)
	}
	return reqs
}

func matchPattern(path, pattern string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(path, strings.TrimSuffix(pattern, "*"))
	}
	return path == pattern
}
