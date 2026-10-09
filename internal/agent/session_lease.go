package agent

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// SessionLease serializes mutating admission when more than one client
// (TUI, editor protocol, gateway) can submit to the same agent. A stale
// generation means the caller lost ownership and must not continue a run.
type SessionLease struct {
	mu         sync.Mutex
	owner      string
	generation atomic.Uint64
	busy       atomic.Bool
}

func (l *SessionLease) Owner() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.owner
}

func (l *SessionLease) Generation() uint64 {
	if l == nil {
		return 0
	}
	return l.generation.Load()
}

func (l *SessionLease) Busy() bool {
	return l != nil && l.busy.Load()
}

// Claim records owner as the current client. A different owner while busy
// is rejected so a second client cannot steal an in-flight run.
func (l *SessionLease) Claim(owner string) (uint64, error) {
	if l == nil {
		return 0, nil
	}
	if owner == "" {
		owner = "default"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.busy.Load() && l.owner != "" && l.owner != owner {
		return l.generation.Load(), fmt.Errorf("session busy under owner %q", l.owner)
	}
	if l.owner != owner {
		l.owner = owner
		l.generation.Add(1)
	}
	return l.generation.Load(), nil
}

func (l *SessionLease) begin(gen uint64) error {
	if l == nil {
		return nil
	}
	if gen != 0 && l.generation.Load() != gen {
		return fmt.Errorf("stale session lease")
	}
	if !l.busy.CompareAndSwap(false, true) {
		return fmt.Errorf("session already running")
	}
	return nil
}

func (l *SessionLease) end() {
	if l == nil {
		return
	}
	l.busy.Store(false)
}
