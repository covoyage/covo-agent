// Package lifecycle provides an extensible agent lifecycle hook system.
//
// Unlike fixed lifecycle hooks, Contributors can be registered dynamically
// by any package without modifying the agent core. Each contributor can
// hook into multiple lifecycle events:
//   - BeforeTurn: before an agent turn starts
//   - AfterTurn: after an agent turn completes
//   - BeforeToolCall: before a tool is invoked
//   - AfterToolCall: after a tool returns
//   - OnError: when an error occurs
//   - OnSessionStart: when a session begins
//   - OnSessionEnd: when a session ends
package lifecycle

import (
	"context"
	"sync"
)

// Event represents a lifecycle event type.
type Event int

const (
	EventBeforeTurn Event = iota
	EventAfterTurn
	EventBeforeToolCall
	EventAfterToolCall
	EventOnError
	EventOnSessionStart
	EventOnSessionEnd
)

func (e Event) String() string {
	names := []string{
		"before_turn",
		"after_turn",
		"before_tool_call",
		"after_tool_call",
		"on_error",
		"on_session_start",
		"on_session_end",
	}
	if int(e) >= 0 && int(e) < len(names) {
		return names[e]
	}
	return "unknown"
}

// Context provides context to lifecycle hooks.
type HookContext struct {
	Ctx         context.Context
	Turn        int
	ToolName    string
	ToolInput   string
	ToolResult  string
	Error       error
	SessionID   string
	Extra       map[string]any
}

// Contributor is a lifecycle hook contributor.
type Contributor interface {
	Name() string
	OnEvent(event Event, hctx *HookContext)
}

// ContributorFunc is a function-based contributor.
type ContributorFunc struct {
	name     string
	callback func(event Event, hctx *HookContext)
}

// NewContributor creates a function-based contributor.
func NewContributor(name string, cb func(event Event, hctx *HookContext)) *ContributorFunc {
	return &ContributorFunc{name: name, callback: cb}
}

func (c *ContributorFunc) Name() string { return c.name }
func (c *ContributorFunc) OnEvent(event Event, hctx *HookContext) {
	c.callback(event, hctx)
}

// Registry manages lifecycle contributors.
type Registry struct {
	mu           sync.RWMutex
	contributors []Contributor
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a contributor to the registry.
func (r *Registry) Register(c Contributor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.contributors = append(r.contributors, c)
}

// RegisterFunc is a convenience for registering a function-based contributor.
func (r *Registry) RegisterFunc(name string, cb func(event Event, hctx *HookContext)) {
	r.Register(NewContributor(name, cb))
}

// Unregister removes a contributor by name.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, c := range r.contributors {
		if c.Name() == name {
			r.contributors = append(r.contributors[:i], r.contributors[i+1:]...)
			return
		}
	}
}

// Emit fires an event to all registered contributors.
// Contributors are called sequentially; a panicking contributor is recovered
// so one bad contributor can't crash the agent.
func (r *Registry) Emit(event Event, hctx *HookContext) {
	r.mu.RLock()
	contributors := make([]Contributor, len(r.contributors))
	copy(contributors, r.contributors)
	r.mu.RUnlock()

	for _, c := range contributors {
		func() {
			defer func() { _ = recover() }()
			c.OnEvent(event, hctx)
		}()
	}
}

// Names returns the names of all registered contributors.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, len(r.contributors))
	for i, c := range r.contributors {
		names[i] = c.Name()
	}
	return names
}

// Count returns the number of registered contributors.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.contributors)
}

// Global registry for convenience.
var globalRegistry = NewRegistry()

// Global returns the global lifecycle registry.
func Global() *Registry {
	return globalRegistry
}

// Register adds a contributor to the global registry.
func Register(c Contributor) {
	globalRegistry.Register(c)
}

// RegisterFunc adds a function-based contributor to the global registry.
func RegisterFunc(name string, cb func(event Event, hctx *HookContext)) {
	globalRegistry.RegisterFunc(name, cb)
}

// Emit fires an event to all global contributors.
func Emit(event Event, hctx *HookContext) {
	globalRegistry.Emit(event, hctx)
}

// ---------------------------------------------------------------------------
// Built-in contributors
// ---------------------------------------------------------------------------

// LoggingContributor logs lifecycle events to a logger.
type LoggingContributor struct {
	logFn func(format string, args ...any)
}

// NewLoggingContributor creates a contributor that logs events.
func NewLoggingContributor(logFn func(format string, args ...any)) *LoggingContributor {
	return &LoggingContributor{logFn: logFn}
}

func (l *LoggingContributor) Name() string { return "logging" }

func (l *LoggingContributor) OnEvent(event Event, hctx *HookContext) {
	if l.logFn == nil {
		return
	}
	switch event {
	case EventBeforeTurn:
		l.logFn("[lifecycle] before turn %d", hctx.Turn)
	case EventAfterTurn:
		l.logFn("[lifecycle] after turn %d", hctx.Turn)
	case EventBeforeToolCall:
		l.logFn("[lifecycle] tool call: %s", hctx.ToolName)
	case EventAfterToolCall:
		l.logFn("[lifecycle] tool result: %s", hctx.ToolName)
	case EventOnError:
		l.logFn("[lifecycle] error: %v", hctx.Error)
	case EventOnSessionStart:
		l.logFn("[lifecycle] session start: %s", hctx.SessionID)
	case EventOnSessionEnd:
		l.logFn("[lifecycle] session end: %s", hctx.SessionID)
	}
}

// MetricsContributor collects simple metrics about lifecycle events.
type MetricsContributor struct {
	mu             sync.Mutex
	turnCount      int
	toolCallCount  int
	errorCount     int
	sessionCount   int
}

// NewMetricsContributor creates a metrics collector.
func NewMetricsContributor() *MetricsContributor {
	return &MetricsContributor{}
}

func (m *MetricsContributor) Name() string { return "metrics" }

func (m *MetricsContributor) OnEvent(event Event, hctx *HookContext) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch event {
	case EventAfterTurn:
		m.turnCount++
	case EventAfterToolCall:
		m.toolCallCount++
	case EventOnError:
		m.errorCount++
	case EventOnSessionStart:
		m.sessionCount++
	}
}

// MetricsSnapshot is a point-in-time view of collected metrics.
type MetricsSnapshot struct {
	TurnCount     int `json:"turn_count"`
	ToolCallCount int `json:"tool_call_count"`
	ErrorCount    int `json:"error_count"`
	SessionCount  int `json:"session_count"`
}

// Snapshot returns the current metrics.
func (m *MetricsContributor) Snapshot() MetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return MetricsSnapshot{
		TurnCount:     m.turnCount,
		ToolCallCount: m.toolCallCount,
		ErrorCount:    m.errorCount,
		SessionCount:  m.sessionCount,
	}
}
