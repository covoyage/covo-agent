package recovery

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// BufferedStatus buffers status messages during retry/fallback operations.
// Messages are only flushed (delivered) if the final attempt fails.
// If a retry succeeds, all buffered messages are silently discarded.
type BufferedStatus struct {
	mu        sync.Mutex
	messages  []string
	flushFn   func(string)
	droppedFn func(string)
}

func NewBufferedStatus(flushFn func(string)) *BufferedStatus {
	return &BufferedStatus{
		flushFn: flushFn,
	}
}

// SetDroppedCallback sets a callback for when buffered messages are dropped
// (on successful retry). This is useful for logging/debugging.
func (b *BufferedStatus) SetDroppedCallback(fn func(string)) {
	b.droppedFn = fn
}

// Buffer adds a message to the buffer instead of sending it immediately.
func (b *BufferedStatus) Buffer(format string, args ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.messages = append(b.messages, fmt.Sprintf(format, args...))
}

// Flush sends all buffered messages to the flush function and clears the buffer.
// Call this when all retries have been exhausted and the operation failed.
func (b *BufferedStatus) Flush() {
	b.mu.Lock()
	messages := b.messages
	b.messages = nil
	b.mu.Unlock()

	if len(messages) == 0 || b.flushFn == nil {
		return
	}
	b.flushFn(strings.Join(messages, "\n"))
}

// Discard drops all buffered messages without delivering them.
// Call this when a retry succeeds.
func (b *BufferedStatus) Discard() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.messages) > 0 && b.droppedFn != nil {
		b.droppedFn(fmt.Sprintf("discarded %d buffered retry message(s)", len(b.messages)))
	}
	b.messages = nil
}

// HasBuffered returns true if there are buffered messages.
func (b *BufferedStatus) HasBuffered() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.messages) > 0
}

// RunWithRetry executes a function with buffered status handling.
// Status messages emitted via the buffer are only flushed if fn returns an error.
func RunWithRetry(ctx context.Context, buffer *BufferedStatus, fn func() error) error {
	err := fn()
	if err != nil {
		buffer.Flush()
	} else {
		buffer.Discard()
	}
	return err
}
