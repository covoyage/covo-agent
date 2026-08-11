package sandbox

import (
	"testing"
)

func TestCalculateBackoffMs(t *testing.T) {
	cases := []struct {
		n    int
		want int
	}{
		{-1, 5000},
		{0, 5000},
		{1, 10000},
		{2, 30000},
		{3, 60000},
		{4, 60000},
		{100, 60000},
	}
	for _, tc := range cases {
		got := calculateBackoffMs(tc.n)
		if got != tc.want {
			t.Errorf("calculateBackoffMs(%d) = %d, want %d", tc.n, got, tc.want)
		}
	}
}

func TestPollBackoff_ProgressiveIncrease(t *testing.T) {
	s := NewProcessStore()

	// Simulate a running session with no output
	id := s.NextID()
	s.running[id] = &ProcessSession{ID: id}

	// First poll: should suggest 5s
	retry1 := s.recordPoll(id, false)
	if retry1 != 5000 {
		t.Errorf("poll 1: got %d, want 5000", retry1)
	}

	// Second poll: should suggest 10s
	retry2 := s.recordPoll(id, false)
	if retry2 != 10000 {
		t.Errorf("poll 2: got %d, want 10000", retry2)
	}

	// Third poll: should suggest 30s
	retry3 := s.recordPoll(id, false)
	if retry3 != 30000 {
		t.Errorf("poll 3: got %d, want 30000", retry3)
	}

	// Fourth poll: should suggest 60s
	retry4 := s.recordPoll(id, false)
	if retry4 != 60000 {
		t.Errorf("poll 4: got %d, want 60000", retry4)
	}

	// Fifth poll: should stay at 60s (cap)
	retry5 := s.recordPoll(id, false)
	if retry5 != 60000 {
		t.Errorf("poll 5: got %d, want 60000", retry5)
	}
}

func TestPollBackoff_NewOutputResetsCount(t *testing.T) {
	s := NewProcessStore()
	id := s.NextID()

	// Escalate
	s.recordPoll(id, false)
	s.recordPoll(id, false)
	s.recordPoll(id, false)

	// New output resets to 5s
	retry := s.recordPoll(id, true)
	if retry != 5000 {
		t.Errorf("after output: got %d, want 5000", retry)
	}

	// Next quiet poll starts at 5s again
	retry2 := s.recordPoll(id, false)
	if retry2 != 5000 {
		t.Errorf("after reset poll: got %d, want 5000", retry2)
	}
}

func TestPollBackoff_IndependentSessions(t *testing.T) {
	s := NewProcessStore()
	id1 := s.NextID()
	id2 := s.NextID()

	// Session 1: 2 quiet polls
	s.recordPoll(id1, false)
	s.recordPoll(id1, false)

	// Session 2: first quiet poll
	retry2 := s.recordPoll(id2, false)
	if retry2 != 5000 {
		t.Errorf("session 2 poll 1: got %d, want 5000", retry2)
	}

	// Session 1 should still be at 30s
	retry1 := s.recordPoll(id1, false)
	if retry1 != 30000 {
		t.Errorf("session 1 poll 3: got %d, want 30000", retry1)
	}
}

func TestPollBackoff_ResetClearsState(t *testing.T) {
	s := NewProcessStore()
	id := s.NextID()

	s.recordPoll(id, false)
	s.recordPoll(id, false)

	s.resetPoll(id)

	// After reset, first quiet poll starts at 5s
	retry := s.recordPoll(id, false)
	if retry != 5000 {
		t.Errorf("after reset: got %d, want 5000", retry)
	}
}
