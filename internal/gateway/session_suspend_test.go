package gateway

import (
	"testing"
	"time"
)

func TestSessionSuspendStore_BasicLifecycle(t *testing.T) {
	s := NewSessionSuspendStore()

	// Not suspended initially
	if ok, _ := s.IsSuspended("key1"); ok {
		t.Fatal("expected not suspended")
	}

	// Suspend
	s.Suspend("key1", "rate_limit", 1*time.Second)
	if ok, reason := s.IsSuspended("key1"); !ok || reason != "rate_limit" {
		t.Fatalf("expected suspended with reason 'rate_limit', got ok=%v reason=%q", ok, reason)
	}

	// Resume
	s.Resume("key1")
	if ok, _ := s.IsSuspended("key1"); ok {
		t.Fatal("expected not suspended after resume")
	}
}

func TestSessionSuspendStore_TTLExpiry(t *testing.T) {
	s := NewSessionSuspendStore()

	s.Suspend("key1", "rate_limit", 50*time.Millisecond)

	// Still suspended
	if ok, _ := s.IsSuspended("key1"); !ok {
		t.Fatal("expected suspended")
	}

	// Wait for expiry
	time.Sleep(100 * time.Millisecond)

	if ok, _ := s.IsSuspended("key1"); ok {
		t.Fatal("expected not suspended after TTL")
	}
}

func TestSessionSuspendStore_Cleanup(t *testing.T) {
	s := NewSessionSuspendStore()

	s.Suspend("key1", "rate_limit", 50*time.Millisecond)
	s.Suspend("key2", "context_overflow", 10*time.Second)

	// Wait for key1 to expire
	time.Sleep(100 * time.Millisecond)

	s.Cleanup()

	if s.Count() != 1 {
		t.Fatalf("expected 1 entry after cleanup, got %d", s.Count())
	}

	if ok, _ := s.IsSuspended("key2"); !ok {
		t.Fatal("key2 should still be suspended")
	}
}

func TestSessionSuspendStore_IndependentKeys(t *testing.T) {
	s := NewSessionSuspendStore()

	s.Suspend("key1", "rate_limit", 10*time.Second)
	s.Suspend("key2", "context_overflow", 10*time.Second)

	s.Resume("key1")

	if ok, _ := s.IsSuspended("key1"); ok {
		t.Fatal("key1 should not be suspended")
	}
	if ok, _ := s.IsSuspended("key2"); !ok {
		t.Fatal("key2 should still be suspended")
	}
}
