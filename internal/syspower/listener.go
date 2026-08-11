// Package syspower provides cross-platform system sleep/wake notifications.
//
// On macOS it uses IOKit via CGo; on Linux it watches the logind D-Bus
// PrepareForSleep signal; on other platforms it falls back to a no-op.
//
// The motivating use case: an OIDC token refresh that is in flight when the
// laptop sleeps can lose its rotated successor token. On wake the client
// holds a dead refresh token and the user is forced to re-login.
package syspower

import (
	"context"
	"sync"
	"sync/atomic"
)

// Event represents a power state transition.
type Event int

const (
	EventWillSleep Event = iota // system is about to sleep
	EventDidWake                // system just woke up
)

func (e Event) String() string {
	switch e {
	case EventWillSleep:
		return "will_sleep"
	case EventDidWake:
		return "did_wake"
	default:
		return "unknown"
	}
}

// Listener is the cross-platform interface for power events.
type Listener struct {
	callbacks atomic.Pointer[[]func(Event)]
	stopCh    chan struct{}
	started   atomic.Bool
}

// NewListener creates a power event listener. Call Start to begin listening.
func NewListener() *Listener {
	defaultCallbacks := []func(Event){}
	l := &Listener{
		stopCh: make(chan struct{}),
	}
	l.callbacks.Store(&defaultCallbacks)
	return l
}

// OnPowerEvent registers a callback for power events.
func (l *Listener) OnPowerEvent(cb func(Event)) {
	for {
		current := l.callbacks.Load()
		next := make([]func(Event), len(*current)+1)
		copy(next, *current)
		next[len(*current)] = cb
		if l.callbacks.CompareAndSwap(current, &next) {
			break
		}
	}
}

// Start begins listening for power events. Returns false if the platform
// doesn't support power notifications (no-op fallback).
func (l *Listener) Start() bool {
	if l.started.Swap(true) {
		return true // already started
	}
	return l.startPlatform()
}

// Stop stops listening.
func (l *Listener) Stop() {
	if !l.started.Load() {
		return
	}
	l.started.Store(false)
	close(l.stopCh)
}

// emit sends a power event to all registered callbacks.
func (l *Listener) emit(event Event) {
	callbacks := l.callbacks.Load()
	for _, cb := range *callbacks {
		if cb != nil {
			cb(event)
		}
	}
}

// startPlatform starts the platform-specific listener.
func (l *Listener) startPlatform() bool {
	// Try platform-specific implementations; fall back to no-op.
	return startPlatformImpl(l)
}

// startPlatformImpl is overridden by platform-specific files.
var startPlatformImpl = func(l *Listener) bool {
	// No-op fallback: no power notifications available.
	return false
}

// WaitForEvent blocks until a power event occurs or ctx is cancelled.
// Returns the event and nil, or (0, ctx.Err()).
func WaitForEvent(ctx context.Context) (Event, error) {
	l := NewListener()
	resultCh := make(chan Event, 1)

	l.OnPowerEvent(func(e Event) {
		select {
		case resultCh <- e:
		default:
		}
	})

	if !l.Start() {
		return 0, nil // no-op platform
	}
	defer l.Stop()

	select {
	case e := <-resultCh:
		return e, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// SleepGate provides a gate that blocks operations during sleep transitions.
// Call Hold() before starting a critical operation (e.g. token refresh)
// and Release() after it completes. If a sleep event fires while held,
// the gate delays the sleep acknowledgement until Release.
type SleepGate struct {
	mu        sync.Mutex
	holders   int
	willSleep atomic.Bool
	wakeCh    chan struct{}
}

// NewSleepGate creates a sleep gate.
func NewSleepGate() *SleepGate {
	return &SleepGate{
		wakeCh: make(chan struct{}),
	}
}

// Hold marks the start of a critical section. The system should not sleep
// while at least one holder is active.
func (g *SleepGate) Hold() {
	g.mu.Lock()
	g.holders++
	g.mu.Unlock()
}

// Release marks the end of a critical section.
func (g *SleepGate) Release() {
	g.mu.Lock()
	g.holders--
	if g.holders <= 0 {
		g.holders = 0
		if g.willSleep.Load() {
			g.willSleep.Store(false)
			select {
			case g.wakeCh <- struct{}{}:
			default:
			}
		}
	}
	g.mu.Unlock()
}

// HandleSleepEvent is called when a sleep event is received. If holders
// are active, it blocks until all holders release.
func (g *SleepGate) HandleSleepEvent(event Event) {
	if event == EventWillSleep {
		g.mu.Lock()
		if g.holders > 0 {
			g.willSleep.Store(true)
			g.mu.Unlock()
			// Wait for all holders to release
			<-g.wakeCh
		} else {
			g.mu.Unlock()
		}
	}
}

// IsActive returns true if at least one critical section is held.
func (g *SleepGate) IsActive() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.holders > 0
}
