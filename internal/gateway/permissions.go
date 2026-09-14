package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// pendingPermissionTTL bounds how long an undecided permission request stays
// registered. After the TTL the request is dropped and the waiter denied.
const pendingPermissionTTL = 30 * time.Minute

// PermissionRequest is a command (or tool call) that reached the manual
// approval stage in a gateway-mode agent and is waiting for a human decision.
type PermissionRequest struct {
	ID          string    `json:"id"`
	Platform    string    `json:"platform,omitempty"`
	ChannelID   string    `json:"channel_id,omitempty"`
	UserID      string    `json:"user_id,omitempty"`
	UserName    string    `json:"user_name,omitempty"`
	SessionKey  string    `json:"session_key,omitempty"`
	Command     string    `json:"command"`
	PatternKey  string    `json:"pattern_key,omitempty"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type permEntry struct {
	req      PermissionRequest
	decision chan string
}

// PermissionStore tracks pending gateway permission requests. The agent-side
// approval bridge registers a request and blocks on Wait; a human responds
// via the IPC API (permissions_respond MCP tool or `covo-agent gateway
// permission` CLI).
//
// Respond delivers into a buffered channel without removing the entry — the
// entry is consumed by Wait. This keeps the Register→Wait window race-free
// even when the decision arrives first.
type PermissionStore struct {
	mu      sync.Mutex
	pending map[string]*permEntry
}

func NewPermissionStore() *PermissionStore {
	return &PermissionStore{pending: make(map[string]*permEntry)}
}

// Register stores a new pending request and returns it with an assigned ID.
func (ps *PermissionStore) Register(req PermissionRequest) PermissionRequest {
	id := newPermissionID()
	req.ID = id
	req.CreatedAt = time.Now()
	ps.mu.Lock()
	ps.pending[id] = &permEntry{req: req, decision: make(chan string, 1)}
	ps.mu.Unlock()
	return req
}

// ListOpen returns all currently undecided, unexpired requests, oldest first.
// Decided-but-unconsumed and expired entries are dropped.
func (ps *PermissionStore) ListOpen() []PermissionRequest {
	now := time.Now()
	ps.mu.Lock()
	defer ps.mu.Unlock()
	out := make([]PermissionRequest, 0, len(ps.pending))
	for id, e := range ps.pending {
		if now.Sub(e.req.CreatedAt) > pendingPermissionTTL {
			delete(ps.pending, id)
			continue
		}
		if len(e.decision) > 0 { // already decided, waiting for consumption
			continue
		}
		out = append(out, e.req)
	}
	for i := 1; i < len(out); i++ { // insertion sort: tiny n, stable order
		for j := i; j > 0 && out[j].CreatedAt.Before(out[j-1].CreatedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Respond delivers a decision ("once"/"session"/"always"/"deny") to the
// agent waiting on the request. Returns false when the ID is unknown or
// already decided.
func (ps *PermissionStore) Respond(id, choice string) bool {
	ps.mu.Lock()
	entry, ok := ps.pending[id]
	ps.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case entry.decision <- choice:
		return true
	default: // already decided
		return false
	}
}

// Wait blocks until a decision arrives for the request, ctx is cancelled, or
// the TTL elapses. Empty choice means the caller should deny.
func (ps *PermissionStore) Wait(ctx context.Context, id string) (string, error) {
	ps.mu.Lock()
	entry, ok := ps.pending[id]
	ps.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("permission request %q not found", id)
	}
	timer := time.NewTimer(pendingPermissionTTL)
	defer timer.Stop()
	select {
	case choice := <-entry.decision:
		ps.mu.Lock()
		delete(ps.pending, id)
		ps.mu.Unlock()
		return choice, nil
	case <-timer.C:
		ps.mu.Lock()
		delete(ps.pending, id)
		ps.mu.Unlock()
		return "", fmt.Errorf("permission request %q expired", id)
	case <-ctx.Done():
		ps.mu.Lock()
		delete(ps.pending, id)
		ps.mu.Unlock()
		return "", ctx.Err()
	}
}

// newPermissionID returns a short random hex id.
func newPermissionID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("perm-%d", time.Now().UnixNano())
	}
	return "perm-" + hex.EncodeToString(b)
}
