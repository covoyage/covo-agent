package gateway

import (
	"sync"
	"time"
)

// suspendEntry records one suspended session.
type suspendEntry struct {
	Reason    string
	ExpiresAt time.Time
}

// SessionSuspendStore tracks sessions that are temporarily suspended
// (e.g. rate-limited, context overflow) with an automatic TTL-based resume.
type SessionSuspendStore struct {
	mu      sync.RWMutex
	entries map[string]*suspendEntry
}

// NewSessionSuspendStore creates a new empty store.
func NewSessionSuspendStore() *SessionSuspendStore {
	return &SessionSuspendStore{
		entries: make(map[string]*suspendEntry),
	}
}

// Suspend marks a session as suspended for the given duration.
func (s *SessionSuspendStore) Suspend(key string, reason string, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = &suspendEntry{
		Reason:    reason,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// IsSuspended returns true if the session is suspended and the TTL has not expired.
// If the TTL has expired, the entry is removed and false is returned.
func (s *SessionSuspendStore) IsSuspended(key string) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		return false, ""
	}
	if time.Now().After(e.ExpiresAt) {
		delete(s.entries, key)
		return false, ""
	}
	return true, e.Reason
}

// Resume explicitly removes a suspension (e.g. admin override).
func (s *SessionSuspendStore) Resume(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
}

// Cleanup removes expired entries. Call periodically.
func (s *SessionSuspendStore) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, e := range s.entries {
		if now.After(e.ExpiresAt) {
			delete(s.entries, k)
		}
	}
}

// Count returns the number of currently suspended sessions.
func (s *SessionSuspendStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}
