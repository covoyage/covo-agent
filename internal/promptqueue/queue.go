// Package promptqueue implements a multi-prompt queue with merge rules.
//
// When the agent is busy processing a prompt, the user can queue additional
// prompts. Unlike a single-pending-input model, this queue preserves all
// queued prompts and supports merge rules for combining related prompts.
package promptqueue

import (
	"strings"
	"sync"
	"time"
)

// Entry represents a single queued prompt.
type Entry struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	QueuedAt  time.Time `json:"queued_at"`
	Combined  bool      `json:"combined"`
	SourceIDs []string  `json:"source_ids,omitempty"` // IDs merged into this entry
}

// Queue is a thread-safe multi-prompt queue with merge support.
type Queue struct {
	mu      sync.Mutex
	entries []Entry
	maxSize int
}

// New creates a prompt queue with the given maximum size.
func New(maxSize int) *Queue {
	if maxSize <= 0 {
		maxSize = 100
	}
	return &Queue{maxSize: maxSize}
}

// Push adds a prompt to the back of the queue.
func (q *Queue) Push(text string) Entry {
	q.mu.Lock()
	defer q.mu.Unlock()

	entry := Entry{
		ID:       generateID(text, len(q.entries)),
		Text:     text,
		QueuedAt: time.Now(),
	}
	q.entries = append(q.entries, entry)
	q.trim()
	return entry
}

// PushFront adds a prompt to the front of the queue (send-now semantics).
func (q *Queue) PushFront(text string) Entry {
	q.mu.Lock()
	defer q.mu.Unlock()

	entry := Entry{
		ID:       generateID(text, 0),
		Text:     text,
		QueuedAt: time.Now(),
	}
	q.entries = append([]Entry{entry}, q.entries...)
	q.trim()
	return entry
}

// Pop removes and returns the front entry. Returns ok=false if empty.
func (q *Queue) Pop() (Entry, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.entries) == 0 {
		return Entry{}, false
	}
	entry := q.entries[0]
	q.entries = q.entries[1:]
	return entry, true
}

// Peek returns the front entry without removing it.
func (q *Queue) Peek() (Entry, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.entries) == 0 {
		return Entry{}, false
	}
	return q.entries[0], true
}

// Len returns the number of queued prompts.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.entries)
}

// IsEmpty returns true if the queue has no entries.
func (q *Queue) IsEmpty() bool {
	return q.Len() == 0
}

// All returns a copy of all entries.
func (q *Queue) All() []Entry {
	q.mu.Lock()
	defer q.mu.Unlock()
	result := make([]Entry, len(q.entries))
	copy(result, q.entries)
	return result
}

// Clear removes all entries.
func (q *Queue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.entries = nil
}

// Remove removes the entry with the given ID. Returns true if found.
func (q *Queue) Remove(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, e := range q.entries {
		if e.ID == id {
			q.entries = append(q.entries[:i], q.entries[i+1:]...)
			return true
		}
	}
	return false
}

// TryMerge attempts to merge the last entry with the given text if they
// are similar enough. Returns true if merged, false if the text was pushed
// as a new entry.
//
// Merge rules:
//   - Two entries can merge if both are short (< mergeThreshold chars)
//   - The merged text combines both with a separator
func (q *Queue) TryMerge(text string, mergeThreshold int) bool {
	if mergeThreshold <= 0 {
		mergeThreshold = 200
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.entries) == 0 {
		entry := Entry{
			ID:       generateID(text, 0),
			Text:     text,
			QueuedAt: time.Now(),
		}
		q.entries = append(q.entries, entry)
		q.trim()
		return false
	}

	last := &q.entries[len(q.entries)-1]
	if len(last.Text) < mergeThreshold && len(text) < mergeThreshold {
		// Merge: combine texts
		merged := Entry{
			ID:        generateID(last.Text+text, len(q.entries)),
			Text:      last.Text + "\n---\n" + text,
			QueuedAt:  last.QueuedAt,
			Combined:  true,
			SourceIDs: append(last.SourceIDs, last.ID),
		}
		q.entries[len(q.entries)-1] = merged
		return true
	}

	// Can't merge — push new entry
	entry := Entry{
		ID:       generateID(text, len(q.entries)),
		Text:     text,
		QueuedAt: time.Now(),
	}
	q.entries = append(q.entries, entry)
	q.trim()
	return false
}

// CanMergeFollower checks if a new entry can merge with the back of the queue.
func (q *Queue) CanMergeFollower(text string, mergeThreshold int) bool {
	if mergeThreshold <= 0 {
		mergeThreshold = 200
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.entries) == 0 {
		return false
	}
	last := q.entries[len(q.entries)-1]
	return len(last.Text) < mergeThreshold && len(text) < mergeThreshold
}

// CanMergeFront checks if a new entry can merge with the front of the queue.
func (q *Queue) CanMergeFront(text string, mergeThreshold int) bool {
	if mergeThreshold <= 0 {
		mergeThreshold = 200
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.entries) == 0 {
		return false
	}
	first := q.entries[0]
	return len(first.Text) < mergeThreshold && len(text) < mergeThreshold
}

// DrainAll returns all entries and clears the queue.
func (q *Queue) DrainAll() []Entry {
	q.mu.Lock()
	defer q.mu.Unlock()

	result := q.entries
	q.entries = nil
	return result
}

func (q *Queue) trim() {
	if len(q.entries) > q.maxSize {
		q.entries = q.entries[len(q.entries)-q.maxSize:]
	}
}

func generateID(text string, seq int) string {
	// Simple ID: timestamp + seq + text hash prefix
	t := time.Now().UnixNano()
	h := uint64(0)
	for _, c := range text {
		h = h*31 + uint64(c)
	}
	id := strings.ToUpper(strings.TrimLeft(
		toHex(uint64(t)^h^uint64(seq)), "0",
	))
	if id == "" {
		id = "0"
	}
	return id[:min(8, len(id))]
}

func toHex(n uint64) string {
	if n == 0 {
		return "0"
	}
	chars := "0123456789abcdef"
	var result string
	for n > 0 {
		result = string(chars[n&0xf]) + result
		n >>= 4
	}
	return result
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
