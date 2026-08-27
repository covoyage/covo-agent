package commitments

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// CommitmentStatus represents the state of a commitment.
type CommitmentStatus string

const (
	CommitmentPending   CommitmentStatus = "pending"
	CommitmentDismissed CommitmentStatus = "dismissed"
	CommitmentCompleted CommitmentStatus = "completed"
)

// Commitment is an inferred promise extracted from agent conversation.
type Commitment struct {
	ID          string            `json:"id"`
	Description string            `json:"description"`
	Status      CommitmentStatus  `json:"status"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Source      string            `json:"source,omitempty"` // e.g. "session:abc123"
}

// CommitmentStore manages inferred commitments with JSON persistence.
type CommitmentStore struct {
	mu       sync.RWMutex
	items    map[string]*Commitment
	filePath string
	seq      int
}

// NewCommitmentStore creates a store backed by the given directory.
func NewCommitmentStore(dir string) *CommitmentStore {
	s := &CommitmentStore{
		items:    make(map[string]*Commitment),
		filePath: filepath.Join(dir, "commitments.json"),
	}
	s.load()
	return s
}

// Detect extracts commitment-like statements from text and returns new Commitments.
// Uses simple pattern matching on common commitment phrases.
func (s *CommitmentStore) Detect(text, source string) []*Commitment {
	s.mu.Lock()
	defer s.mu.Unlock()

	lower := strings.ToLower(text)
	var found []*Commitment

	// Patterns that suggest a commitment
	patterns := []struct {
		trigger string
		words   int // max words to capture after trigger
	}{
		{"i'll check", 8},
		{"i will check", 8},
		{"i'll look into", 6},
		{"i will look into", 6},
		{"i'll follow up", 6},
		{"i will follow up", 6},
		{"i'll get back", 6},
		{"i will get back", 6},
		{"i'll investigate", 8},
		{"i will investigate", 6},
		{"i'll research", 6},
		{"i will research", 6},
		{"let me find out", 6},
	}

	for _, p := range patterns {
		idx := strings.Index(lower, p.trigger)
		if idx < 0 {
			continue
		}
		// Extract the sentence containing the trigger
		start := idx
		for start > 0 && lower[start-1] != '.' && lower[start-1] != '!' && lower[start-1] != '?' && lower[start-1] != '\n' {
			start--
		}
		end := idx + len(p.trigger)
		// Find end of sentence
		for end < len(lower) && lower[end] != '.' && lower[end] != '!' && lower[end] != '?' && lower[end] != '\n' {
			end++
		}
		desc := strings.TrimSpace(text[start:end])

		// Deduplicate: skip if any pending commitment has substantial overlap
		if len(desc) == 0 {
			continue
		}
		isDup := false
		descLower := strings.ToLower(desc)
		for _, existing := range s.items {
			if existing.Status != CommitmentPending {
				continue
			}
			existingLower := strings.ToLower(existing.Description)
			// Bidirectional overlap: if either string contains the other, it's a dup
			if strings.Contains(descLower, existingLower) || strings.Contains(existingLower, descLower) {
				isDup = true
				break
			}
			// Check for common prefix >= 10 characters (same sentence start)
			commonLen := len(descLower)
			if len(existingLower) < commonLen {
				commonLen = len(existingLower)
			}
			common := 0
			for common < commonLen && descLower[common] == existingLower[common] {
				common++
			}
			if common >= 10 {
				isDup = true
				break
			}
		}
		if !isDup {
			s.seq++
			c := &Commitment{
				ID:          fmt.Sprintf("cmt_%d", s.seq),
				Description: desc,
				Status:      CommitmentPending,
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
				Source:      source,
			}
			s.items[c.ID] = c
			found = append(found, c)
		}
	}

	if len(found) > 0 {
		s.save()
	}
	return found
}

// List returns all commitments, sorted by creation time (newest first).
func (s *CommitmentStore) List() []Commitment {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Commitment, 0, len(s.items))
	for _, c := range s.items {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			// Same instant (possible on Windows, where time.Now() has coarse
			// resolution) — break the tie by the monotonically increasing
			// sequence number so newest-first stays deterministic.
			return commitmentSeq(out[i].ID) > commitmentSeq(out[j].ID)
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// commitmentSeq extracts the monotonically increasing sequence number from a
// commitment ID ("cmt_<n>"). Used as a stable tiebreaker when two commitments
// share the same CreatedAt.
func commitmentSeq(id string) int {
	var n int
	fmt.Sscanf(id, "cmt_%d", &n)
	return n
}

// ListPending returns only pending commitments.
func (s *CommitmentStore) ListPending() []Commitment {
	all := s.List()
	out := make([]Commitment, 0, len(all))
	for _, c := range all {
		if c.Status == CommitmentPending {
			out = append(out, c)
		}
	}
	return out
}

// Dismiss marks a commitment as dismissed.
func (s *CommitmentStore) Dismiss(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.items[id]
	if !ok {
		return fmt.Errorf("commitment %q not found", id)
	}
	c.Status = CommitmentDismissed
	c.UpdatedAt = time.Now()
	s.save()
	return nil
}

// Complete marks a commitment as completed.
func (s *CommitmentStore) Complete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.items[id]
	if !ok {
		return fmt.Errorf("commitment %q not found", id)
	}
	c.Status = CommitmentCompleted
	c.UpdatedAt = time.Now()
	s.save()
	return nil
}

// Count returns the number of pending commitments.
func (s *CommitmentStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, c := range s.items {
		if c.Status == CommitmentPending {
			n++
		}
	}
	return n
}

// --- persistence ---

func (s *CommitmentStore) load() {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return
	}
	var items []*Commitment
	if err := json.Unmarshal(data, &items); err != nil {
		return
	}
	for _, c := range items {
		s.items[c.ID] = c
		if c.ID != "" {
			var n int
			fmt.Sscanf(c.ID, "cmt_%d", &n)
			if n > s.seq {
				s.seq = n
			}
		}
	}
}

func (s *CommitmentStore) save() {
	items := make([]*Commitment, 0, len(s.items))
	for _, c := range s.items {
		items = append(items, c)
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return
	}
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return
	}
	os.WriteFile(s.filePath, data, 0600)
}
