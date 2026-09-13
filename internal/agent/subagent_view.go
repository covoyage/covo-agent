package agent

import (
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/tools/subagent"
	"github.com/covoyage/covonaut/agentcore"
)

// SubagentViewEntry is one spawned child agent as seen by the TUI: enough
// state to render a live transcript view (opencode-style "switch to the
// subagent view") plus a captured transcript after completion.
type SubagentViewEntry struct {
	id        string
	task      string
	depth     int
	startedAt time.Time

	mu       sync.Mutex
	status   string                     // running | completed | failed
	live     func() []agentcore.Message // while running: reads the child's live state
	snapshot []agentcore.Message        // captured at completion
	endedAt  time.Time
}

// ID returns the display id ("agent-1", ...).
func (e *SubagentViewEntry) ID() string { return e.id }

// Task returns the child's task description.
func (e *SubagentViewEntry) Task() string { return e.task }

// Depth returns the child's spawn depth.
func (e *SubagentViewEntry) Depth() int { return e.depth }

// StartedAt returns the spawn time.
func (e *SubagentViewEntry) StartedAt() time.Time { return e.startedAt }

// Status returns the entry's lifecycle status.
func (e *SubagentViewEntry) Status() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

// Live reports whether the child is still running.
func (e *SubagentViewEntry) Live() bool {
	return e.Status() == "running"
}

// EndedAt returns the completion time, zero while running.
func (e *SubagentViewEntry) EndedAt() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.endedAt
}

// Messages returns the child's transcript: live from its state while
// running, from the captured snapshot afterwards.
func (e *SubagentViewEntry) Messages() []agentcore.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.live != nil {
		return e.live()
	}
	return e.snapshot
}

// maxLiveAge bounds how long a view entry may remain "running" without its
// completion signal. Every normal path reaches MarkFinished; this is the
// belt-and-braces guarantee that a leaked child goroutine (e.g. a provider
// stream that ignores cancellation) can never pin the footer live-agent strip
// open forever. Overridable in tests.
var maxLiveAge = 60 * time.Minute

// SubagentViewRegistry tracks spawned child agents for view switching.
// Entries are created at spawn time and stay for the lifetime of the parent
// agent (process), so completed transcripts remain inspectable.
type SubagentViewRegistry struct {
	mu      sync.Mutex
	counter int
	entries map[string]*SubagentViewEntry
	order   []string
}

// NewSubagentViewRegistry creates an empty registry.
func NewSubagentViewRegistry() *SubagentViewRegistry {
	return &SubagentViewRegistry{entries: make(map[string]*SubagentViewEntry)}
}

// Register records a newly spawned child. The live closure reads the child's
// message state while it runs. Returns the display id ("agent-1", ...).
func (r *SubagentViewRegistry) Register(task string, depth int, live func() []agentcore.Message) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counter++
	id := "agent-" + itoa(r.counter)
	r.registerLocked(id, task, depth, live)
	return id
}

// RegisterWithID records a child under an externally supplied id (the
// sub-agent registry id) so chat-side tool blocks can reference the view.
// Empty ids fall back to the sequential "agent-N" form.
func (r *SubagentViewRegistry) RegisterWithID(id, task string, depth int, live func() []agentcore.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == "" {
		r.counter++
		id = "agent-" + itoa(r.counter)
	}
	r.registerLocked(id, task, depth, live)
}

func (r *SubagentViewRegistry) registerLocked(id, task string, depth int, live func() []agentcore.Message) {
	// Re-registration with the same id (SubagentRunner reruns reuse the view
	// ref) must not duplicate the order entry — otherwise List() counts the
	// sub-agent multiple times in the footer/dashboard.
	if _, exists := r.entries[id]; !exists {
		r.order = append(r.order, id)
	}
	r.entries[id] = &SubagentViewEntry{
		id:        id,
		task:      task,
		depth:     depth,
		startedAt: time.Now(),
		status:    "running",
		live:      live,
	}
}

// MarkFinished captures the final transcript and flips the entry's status.
func (r *SubagentViewRegistry) MarkFinished(id, status string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return
	}
	if status != "completed" && status != "failed" {
		status = "failed"
	}
	e.mu.Lock()
	if e.live != nil {
		e.snapshot = e.live()
		e.live = nil
	}
	e.status = status
	e.endedAt = time.Now()
	e.mu.Unlock()
}

// Get returns an entry by id.
func (r *SubagentViewRegistry) Get(id string) (*SubagentViewEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	return e, ok
}

// List returns all entries in spawn order, sweeping entries that have been
// "running" beyond maxLiveAge (see the watchdog comment).
func (r *SubagentViewRegistry) List() []*SubagentViewEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepStaleLocked(time.Now())
	out := make([]*SubagentViewEntry, 0, len(r.order))
	for _, id := range r.order {
		if e, ok := r.entries[id]; ok {
			out = append(out, e)
		}
	}
	return out
}

// sweepStaleLocked force-terminates over-aged running entries so the footer
// strip and dashboards can never be pinned open by a leaked child.
func (r *SubagentViewRegistry) sweepStaleLocked(now time.Time) {
	for _, e := range r.entries {
		e.mu.Lock()
		if e.status == "running" && now.Sub(e.startedAt) > maxLiveAge {
			e.status = "stale"
			e.endedAt = now
			e.live = nil
			e.snapshot = nil // release the child state reference
		}
		e.mu.Unlock()
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// SubagentViews returns the agent's subagent view registry (TUI integration).
func (ca *CovoAgent) SubagentViews() *SubagentViewRegistry {
	return ca.subagentViews
}

// SubagentRegistry returns the live sub-agent run registry, which powers
// steer (SendInput) and interrupt from the TUI dashboard/panels.
func (ca *CovoAgent) SubagentRegistry() *subagent.SubagentRegistry {
	if ca.agentTools == nil {
		return nil
	}
	return ca.agentTools.SubagentRegistry()
}
