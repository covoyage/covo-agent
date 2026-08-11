package syspower

import (
	"testing"
	"time"
)

func TestListener_Callbacks(t *testing.T) {
	l := NewListener()

	var events []Event
	l.OnPowerEvent(func(e Event) {
		events = append(events, e)
	})

	l.emit(EventWillSleep)
	l.emit(EventDidWake)

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0] != EventWillSleep {
		t.Errorf("expected will_sleep, got %s", events[0])
	}
	if events[1] != EventDidWake {
		t.Errorf("expected did_wake, got %s", events[1])
	}
}

func TestListener_MultipleCallbacks(t *testing.T) {
	l := NewListener()

	var count int
	l.OnPowerEvent(func(e Event) { count++ })
	l.OnPowerEvent(func(e Event) { count++ })

	l.emit(EventWillSleep)
	if count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}
}

func TestListener_StartStop(t *testing.T) {
	l := NewListener()
	if !l.started.Load() {
		// Start may return false on unsupported platforms; that's ok
		l.Start()
	}
	// Should not panic on double start
	l.Start()
	l.Stop()
}

func TestEvent_String(t *testing.T) {
	if EventWillSleep.String() != "will_sleep" {
		t.Error("bad string")
	}
	if EventDidWake.String() != "did_wake" {
		t.Error("bad string")
	}
}

func TestSleepGate_NoHolders(t *testing.T) {
	g := NewSleepGate()
	if g.IsActive() {
		t.Error("expected not active initially")
	}

	// Should not block when no holders
	done := make(chan struct{})
	go func() {
		g.HandleSleepEvent(EventWillSleep)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("HandleSleepEvent blocked with no holders")
	}
}

func TestSleepGate_WithHolders(t *testing.T) {
	g := NewSleepGate()
	g.Hold()
	if !g.IsActive() {
		t.Error("expected active after hold")
	}

	// Should block until release
	done := make(chan struct{})
	go func() {
		g.HandleSleepEvent(EventWillSleep)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("should have blocked")
	case <-time.After(50 * time.Millisecond):
	}

	g.Release()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("should have unblocked after release")
	}
}

func TestSleepGate_MultipleHolders(t *testing.T) {
	g := NewSleepGate()
	g.Hold()
	g.Hold()

	done := make(chan struct{})
	go func() {
		g.HandleSleepEvent(EventWillSleep)
		close(done)
	}()

	g.Release()
	select {
	case <-done:
		t.Fatal("should still block with 1 holder left")
	case <-time.After(50 * time.Millisecond):
	}

	g.Release()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("should unblock after all holders release")
	}
}

func TestSleepGate_ReleaseWithoutHold(t *testing.T) {
	g := NewSleepGate()
	// Should not panic or go negative
	g.Release()
	if g.IsActive() {
		t.Error("expected not active")
	}
}
