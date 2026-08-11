package commitments

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitmentDetect(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{
			name: "i'll check",
			text: "I'll check the database logs and get back to you.",
			want: 1,
		},
		{
			name: "i will follow up",
			text: "I will follow up with the team about this issue.",
			want: 1,
		},
		{
			name: "let me find out",
			text: "Let me find out what happened and report back.",
			want: 1,
		},
		{
			name: "no commitment",
			text: "Here is a summary of the findings.",
			want: 0,
		},
		{
			name: "multiple commitments",
			text: "I'll check the logs. I will follow up with the team.",
			want: 2,
		},
		{
			name: "case insensitive",
			text: "I'LL CHECK THE DATABASE.",
			want: 1,
		},
		{
			name: "empty text",
			text: "",
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			s := NewCommitmentStore(dir)
			got := s.Detect(tt.text, "test")
			if len(got) != tt.want {
				t.Errorf("Detect() returned %d commitments, want %d", len(got), tt.want)
			}
		})
	}
}

func TestCommitmentDeduplication(t *testing.T) {
	dir := t.TempDir()
	s := NewCommitmentStore(dir)

	// Same commitment text detected twice
	got1 := s.Detect("I'll check the database performance.", "test")
	if len(got1) != 1 {
		t.Fatalf("expected 1 commitment, got %d", len(got1))
	}

	got2 := s.Detect("I'll check the database performance.", "test")
	if len(got2) != 0 {
		t.Errorf("expected 0 (dedup), got %d", len(got2))
	}

	// Similar text should also dedup (same sentence start)
	got3 := s.Detect("I'll check the database performance first thing tomorrow.", "test")
	if len(got3) != 0 {
		t.Errorf("expected 0 (dedup similar), got %d", len(got3))
	}

	// Different text should NOT dedup
	got4 := s.Detect("I will follow up with the team.", "test")
	if len(got4) != 1 {
		t.Errorf("expected 1 (different), got %d", len(got4))
	}
}

func TestCommitmentDismissComplete(t *testing.T) {
	dir := t.TempDir()
	s := NewCommitmentStore(dir)

	found := s.Detect("I'll investigate the error.", "test")
	if len(found) != 1 {
		t.Fatalf("expected 1 commitment, got %d", len(found))
	}
	id := found[0].ID

	// Verify pending status
	pending := s.ListPending()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}

	// Dismiss
	if err := s.Dismiss(id); err != nil {
		t.Fatal(err)
	}
	pending = s.ListPending()
	if len(pending) != 0 {
		t.Errorf("expected 0 pending after dismiss, got %d", len(pending))
	}

	// Complete
	found2 := s.Detect("I will research the new API.", "test")
	id2 := found2[0].ID
	if err := s.Complete(id2); err != nil {
		t.Fatal(err)
	}

	// Both should be in List but only one pending
	all := s.List()
	if len(all) != 2 {
		t.Errorf("expected 2 total, got %d", len(all))
	}
}

func TestCommitmentErrors(t *testing.T) {
	dir := t.TempDir()
	s := NewCommitmentStore(dir)

	if err := s.Dismiss("nonexistent"); err == nil {
		t.Error("expected error dismissing nonexistent commitment")
	}
	if err := s.Complete("nonexistent"); err == nil {
		t.Error("expected error completing nonexistent commitment")
	}
}

func TestCommitmentPersistence(t *testing.T) {
	dir := t.TempDir()
	s1 := NewCommitmentStore(dir)
	s1.Detect("I'll check the logs.", "session:abc")

	// New store loading from same dir should see the commitment
	s2 := NewCommitmentStore(dir)
	pending := s2.ListPending()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending after reload, got %d", len(pending))
	}
	if !strings.Contains(pending[0].Description, "I'll check the logs") {
		t.Errorf("unexpected description: %q", pending[0].Description)
	}
	if pending[0].Source != "session:abc" {
		t.Errorf("unexpected source: %q", pending[0].Source)
	}
}

func TestCommitmentCount(t *testing.T) {
	dir := t.TempDir()
	s := NewCommitmentStore(dir)

	if n := s.Count(); n != 0 {
		t.Errorf("expected 0, got %d", n)
	}

	s.Detect("I'll check the bug report.", "test")
	if n := s.Count(); n != 1 {
		t.Errorf("expected 1, got %d", n)
	}

	s.Detect("I will investigate the PR.", "test")
	if n := s.Count(); n != 2 {
		t.Errorf("expected 2, got %d", n)
	}
}

func TestCommitmentListOrder(t *testing.T) {
	dir := t.TempDir()
	s := NewCommitmentStore(dir)

	s.Detect("I'll check the first report.", "test")
	s.Detect("I will investigate the second issue.", "test")

	all := s.List()
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}
	// Newest first
	if !strings.Contains(all[0].Description, "second") {
		t.Errorf("expected newest first, got %q", all[0].Description)
	}
}

func TestCommitmentDetectCorrupted(t *testing.T) {
	dir := t.TempDir()
	// Write corrupted JSON to the persistence file
	os.WriteFile(filepath.Join(dir, "commitments.json"), []byte("{corrupted"), 0600)

	s := NewCommitmentStore(dir)
	// Should not panic, start with empty state
	found := s.Detect("I'll check this.", "test")
	if len(found) != 1 {
		t.Errorf("expected 1 commitment, got %d", len(found))
	}
}
