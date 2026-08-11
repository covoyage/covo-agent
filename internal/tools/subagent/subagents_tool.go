package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// SubagentRun tracks a spawned subagent execution.
type SubagentRun struct {
	ID            string      `json:"id"`
	Task          string      `json:"task"`
	Status        string      `json:"status"` // running, completed, failed
	StartedAt     time.Time   `json:"started_at"`
	EndedAt       *time.Time  `json:"ended_at,omitempty"`
	Depth         int         `json:"depth"`
	InputMessages []string    `json:"-"`
	inputChan     chan string `json:"-"` // real-time message channel
}

// SubagentRegistry tracks active and recent subagent runs.
type SubagentRegistry struct {
	mu              sync.RWMutex
	runs            map[string]*SubagentRun
	cancels         map[string]context.CancelFunc // active cancel funcs by id
	counter         int
	store           *SubagentStore // optional persistence layer
	parentSessionFn func() string  // optional: returns parent session ID for persistence
}

func NewSubagentRegistry() *SubagentRegistry {
	return &SubagentRegistry{
		runs:    make(map[string]*SubagentRun),
		cancels: make(map[string]context.CancelFunc),
	}
}

// SetStore wires a persistent SubagentStore and parent session ID provider.
// When set, all Start/Complete/Interrupt operations are persisted, and
// RecoverOrphaned() should be called once at startup to mark any
// previously-running subagents as orphaned.
func (r *SubagentRegistry) SetStore(store *SubagentStore, parentSessionFn func() string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store = store
	r.parentSessionFn = parentSessionFn
}

// RecoverOrphaned marks all previously-running subagents as orphaned in the
// persistent store. Should be called once at process startup after SetStore.
// Returns the list of orphaned records for diagnostics/logging.
func (r *SubagentRegistry) RecoverOrphaned() ([]SubagentRecord, error) {
	if r.store == nil {
		return nil, nil
	}
	return r.store.Recover()
}

func (r *SubagentRegistry) Start(task string, depth int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.allocIDLocked()
	now := time.Now()
	r.runs[id] = &SubagentRun{
		ID:        id,
		Task:      task,
		Status:    "running",
		StartedAt: now,
		Depth:     depth,
		inputChan: make(chan string, 10),
	}
	r.persistCreateLocked(id, task, depth, now)
	return id
}

// StartWithCancel registers a subagent and returns a cancellable context.
// The cancel function is stored so Interrupt() can trigger it.
func (r *SubagentRegistry) StartWithCancel(parentCtx context.Context, task string, depth int) (context.Context, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.allocIDLocked()
	now := time.Now()
	r.runs[id] = &SubagentRun{
		ID:        id,
		Task:      task,
		Status:    "running",
		StartedAt: now,
		Depth:     depth,
		inputChan: make(chan string, 10),
	}
	r.persistCreateLocked(id, task, depth, now)
	ctx, cancel := context.WithCancel(parentCtx)
	r.cancels[id] = cancel
	return ctx, id
}

// allocIDLocked allocates the next subagent ID. If a persistent store is
// configured, the ID is allocated via the store (crash-safe, no collisions
// with recovered orphans). Otherwise, falls back to in-memory counter.
// Caller must hold r.mu.
func (r *SubagentRegistry) allocIDLocked() string {
	if r.store != nil {
		// Store-based allocation is synchronous and safe under r.mu.
		// We use a sync path to avoid goroutine races; the store's own mu
		// serializes access.
		id, err := r.store.NextID("sub")
		if err == nil && id != "" {
			// Update in-memory counter to stay consistent.
			if n := parseSubagentNum(id); n > r.counter {
				r.counter = n
			}
			return id
		}
		// Fall through to in-memory on error.
	}
	r.counter++
	return fmt.Sprintf("sub-%d", r.counter)
}

// persistCreateLocked writes a new record to the persistent store.
// Caller must hold r.mu.
func (r *SubagentRegistry) persistCreateLocked(id, task string, depth int, started time.Time) {
	if r.store == nil {
		return
	}
	parentSession := ""
	if r.parentSessionFn != nil {
		parentSession = r.parentSessionFn()
	}
	rec := &SubagentRecord{
		ID:              id,
		ParentSessionID: parentSession,
		Task:            task,
		Status:          "running",
		Depth:           depth,
		StartedAt:       started,
		LastHeartbeatAt: started,
	}
	_ = r.store.Create(rec)
}

// UpdateHeartbeat refreshes the persisted last_heartbeat_at for a subagent.
// Used by enhanced stuck detection.
func (r *SubagentRegistry) UpdateHeartbeat(id string) {
	if r.store == nil {
		return
	}
	_ = r.store.UpdateHeartbeat(id)
}

func (r *SubagentRegistry) Complete(id string, failed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[id]
	if !ok {
		return
	}
	now := time.Now()
	run.EndedAt = &now
	status := "completed"
	if failed {
		run.Status = "failed"
		status = "failed"
	} else {
		run.Status = "completed"
	}
	// Persist final state.
	if r.store != nil {
		_ = r.store.MarkCompleted(id, status, "")
	}
}

func (r *SubagentRegistry) List(activeOnly bool) []*SubagentRun {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var runs []*SubagentRun
	for _, run := range r.runs {
		if activeOnly && run.Status != "running" {
			continue
		}
		runs = append(runs, run)
	}
	// Sort by started_at desc
	for i := 0; i < len(runs); i++ {
		for j := i + 1; j < len(runs); j++ {
			if runs[j].StartedAt.After(runs[i].StartedAt) {
				runs[i], runs[j] = runs[j], runs[i]
			}
		}
	}
	return runs
}

func (r *SubagentRegistry) Get(id string) (*SubagentRun, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	run, ok := r.runs[id]
	return run, ok
}

func (r *SubagentRegistry) Interrupt(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[id]
	if !ok || run.Status != "running" {
		return false
	}
	// Cancel the context to signal the subagent to stop
	if cancel, ok := r.cancels[id]; ok {
		cancel()
	}
	now := time.Now()
	run.EndedAt = &now
	run.Status = "interrupted"
	// Persist interrupted state.
	if r.store != nil {
		_ = r.store.MarkCompleted(id, "interrupted", "interrupted by parent")
	}
	return true
}

func (r *SubagentRegistry) ActiveIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var ids []string
	for _, run := range r.runs {
		if run.Status == "running" {
			ids = append(ids, run.ID)
		}
	}
	return ids
}

func (r *SubagentRegistry) Close(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[id]
	if !ok || run.Status == "running" {
		return false
	}
	delete(r.runs, id)
	delete(r.cancels, id)
	return true
}

func (r *SubagentRegistry) SendInput(id, message string) bool {
	r.mu.RLock()
	run, ok := r.runs[id]
	if !ok || run.Status != "running" {
		r.mu.RUnlock()
		return false
	}
	ch := run.inputChan
	run.InputMessages = append(run.InputMessages, message)
	r.mu.RUnlock()
	select {
	case ch <- message:
		return true
	default:
		return false
	}
}

// DrainInput returns all pending input messages for a subagent without blocking.
func (run *SubagentRun) DrainInput() []string {
	var msgs []string
	for {
		select {
		case msg := <-run.inputChan:
			msgs = append(msgs, msg)
		default:
			return msgs
		}
	}
}

// ListOrphaned returns subagent records marked as orphaned (previously running
// when the process died). Available only when a persistent store is configured.
func (r *SubagentRegistry) ListOrphaned() ([]SubagentRecord, error) {
	if r.store == nil {
		return nil, nil
	}
	return r.store.ListByStatus("orphaned")
}

// parseSubagentNum extracts the numeric suffix from a subagent ID like "sub-42".
// Returns 0 if the suffix is non-numeric.
func parseSubagentNum(id string) int {
	idx := strings.LastIndex(id, "-")
	if idx < 0 || idx >= len(id)-1 {
		return 0
	}
	n, err := strconv.Atoi(id[idx+1:])
	if err != nil {
		return 0
	}
	return n
}

func BuildSubagentsTool(registry *SubagentRegistry) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "subagents",
		Description: "List active and recent subagent runs spawned by this session. Use this to check what subagents are still running, what completed, and their results. Set include_orphaned=true to also show subagents orphaned by a previous process crash (recovered on startup).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"active_only": map[string]any{
					"type":        "boolean",
					"description": "If true, only show currently running subagents (default: false).",
				},
				"include_orphaned": map[string]any{
					"type":        "boolean",
					"description": "If true, also include subagents orphaned by a previous process crash (default: false).",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				ActiveOnly      bool `json:"active_only"`
				IncludeOrphaned bool `json:"include_orphaned"`
			}
			json.Unmarshal(args, &params)

			runs := registry.List(params.ActiveOnly)
			out := make([]map[string]any, len(runs))
			active, completed, failed := 0, 0, 0
			for i, r := range runs {
				entry := map[string]any{
					"id":         r.ID,
					"task":       r.Task,
					"status":     r.Status,
					"depth":      r.Depth,
					"started_at": r.StartedAt.Format(time.RFC3339),
				}
				if r.EndedAt != nil {
					entry["ended_at"] = r.EndedAt.Format(time.RFC3339)
				}
				switch r.Status {
				case "running":
					active++
				case "completed":
					completed++
				case "failed":
					failed++
				}
				out[i] = entry
			}

			result := map[string]any{
				"subagents": out,
				"total":     len(out),
				"active":    active,
				"completed": completed,
				"failed":    failed,
			}

			// Include orphaned records from previous process crash if requested.
			if params.IncludeOrphaned {
				orphaned, err := registry.ListOrphaned()
				if err == nil && len(orphaned) > 0 {
					orphans := make([]map[string]any, len(orphaned))
					for i, o := range orphaned {
						entry := map[string]any{
							"id":                o.ID,
							"task":              o.Task,
							"status":            o.Status,
							"depth":             o.Depth,
							"parent_session_id": o.ParentSessionID,
							"started_at":        o.StartedAt.Format(time.RFC3339),
						}
						if !o.EndedAt.IsZero() {
							entry["ended_at"] = o.EndedAt.Format(time.RFC3339)
						}
						if o.Error != "" {
							entry["error"] = o.Error
						}
						orphans[i] = entry
					}
					result["orphaned"] = orphans
					result["orphaned_count"] = len(orphans)
				}
			}

			return result, nil
		},
	}
}
