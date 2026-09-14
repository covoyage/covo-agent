package gateway

import (
	"context"
	"sync"
	"time"
)

// Event types emitted to the gateway event log (visible via
// `events_poll` / `events_wait` MCP tools and the IPC API).
const (
	EventMessageReceived    = "message_received"
	EventReplySent          = "reply_sent"
	EventReplyFailed        = "reply_failed"
	EventPairingRequested   = "pairing_requested"
	EventSessionSuspended   = "session_suspended"
	EventPermissionRequest  = "permission_requested"
	EventPermissionDecision = "permission_decided"
	EventMessageDropped     = "message_dropped"
)

// Event is a single gateway occurrence, sequenced for cursor-based polling.
type Event struct {
	Seq       uint64    `json:"seq"`
	Time      time.Time `json:"time"`
	Type      string    `json:"type"`
	Platform  string    `json:"platform,omitempty"`
	ChannelID string    `json:"channel_id,omitempty"`
	UserID    string    `json:"user_id,omitempty"`
	UserName  string    `json:"user_name,omitempty"`
	Text      string    `json:"text,omitempty"` // truncated preview
	Detail    string    `json:"detail,omitempty"`
}

const (
	eventBufferCap  = 1024
	eventPreviewLen = 280
)

// EventLog is a bounded, sequenced in-memory event log with long-poll wait
// support. Events are consumed by cursor: clients remember the last Seq and
// ask for everything after it.
type EventLog struct {
	mu     sync.Mutex
	seq    uint64
	buf    []Event
	notify chan struct{}
}

func NewEventLog() *EventLog {
	return &EventLog{notify: make(chan struct{})}
}

// Append records an event and wakes long-poll waiters. Text/Detail are
// truncated to keep the ring buffer bounded.
func (l *EventLog) Append(e Event) Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	e.Seq = l.seq
	e.Time = time.Now()
	e.Text = truncateRunes(e.Text, eventPreviewLen)
	e.Detail = truncateRunes(e.Detail, eventPreviewLen)
	l.buf = append(l.buf, e)
	if len(l.buf) > eventBufferCap {
		l.buf = l.buf[len(l.buf)-eventBufferCap:]
	}
	close(l.notify)
	l.notify = make(chan struct{})
	return e
}

// Since returns all buffered events with Seq > cursor.
func (l *EventLog) Since(cursor uint64) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sinceLocked(cursor)
}

// LastSeq returns the newest assigned sequence number (0 when empty).
func (l *EventLog) LastSeq() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.seq
}

func (l *EventLog) sinceLocked(cursor uint64) []Event {
	var out []Event
	for _, e := range l.buf {
		if e.Seq > cursor {
			out = append(out, e)
		}
	}
	return out
}

// Wait long-polls: returns immediately when events after cursor exist,
// otherwise blocks until new events arrive, ctx is cancelled, or the timeout
// elapses. The returned slice may be empty on timeout.
func (l *EventLog) Wait(ctx context.Context, cursor uint64, timeout time.Duration) []Event {
	if events := l.Since(cursor); len(events) > 0 {
		return events
	}
	if timeout <= 0 {
		return nil
	}
	deadline := time.After(timeout)
	for {
		l.mu.Lock()
		notify := l.notify
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return l.Since(cursor)
		case <-deadline:
			return l.Since(cursor)
		case <-notify:
			if events := l.Since(cursor); len(events) > 0 {
				return events
			}
		}
	}
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
