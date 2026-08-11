package sessions

import (
	"fmt"
	"sync"
)

type sessionRunner struct {
	running   bool
	sessionID string
	turnID    int
}

type RunnerMutex struct {
	mu      sync.Mutex
	runners map[string]*sessionRunner
}

func NewRunnerMutex() *RunnerMutex {
	return &RunnerMutex{runners: make(map[string]*sessionRunner)}
}

func (rm *RunnerMutex) Acquire(sessionID string) (release func(), err error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	r, exists := rm.runners[sessionID]
	if !exists {
		r = &sessionRunner{sessionID: sessionID}
		rm.runners[sessionID] = r
	}

	if r.running {
		return nil, fmt.Errorf("session %s is already running (turn %d)", sessionID, r.turnID)
	}

	r.running = true
	r.turnID++

	return func() {
		rm.mu.Lock()
		r.running = false
		rm.mu.Unlock()
	}, nil
}

func (rm *RunnerMutex) IsRunning(sessionID string) bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	r, ok := rm.runners[sessionID]
	return ok && r.running
}

func (rm *RunnerMutex) Cancel(sessionID string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if r, ok := rm.runners[sessionID]; ok {
		r.running = false
	}
}

func (rm *RunnerMutex) Cleanup() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	for id, r := range rm.runners {
		if !r.running {
			delete(rm.runners, id)
		}
	}
}
