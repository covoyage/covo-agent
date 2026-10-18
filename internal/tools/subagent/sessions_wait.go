package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// waitSeen persists across tool calls within one agent instance so each
// terminal transition is reported exactly once (mailbox semantics, in the
// spirit of codex's wait_agent): a completed child is announced on the first
// wait that observes it and never re-reported afterwards.
type waitSeen struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (w *waitSeen) mark(ids []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, id := range ids {
		w.seen[id] = true
	}
}

func (w *waitSeen) isNew(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.seen[id]
}

// isTerminalStatus reports whether a registry status ends a child's run.
func isTerminalStatus(status string) bool {
	switch status {
	case "completed", "failed", "interrupted", "orphaned":
		return true
	}
	return false
}

// BuildSessionsWaitTool builds the wait tool. It returns a STATUS SUMMARY
// (ids, tasks, states) — never full outputs: each child's result is delivered
// to the parent inbox on completion (auto-drained on the next run, or steered
// into the current run by the completion notification).
func BuildSessionsWaitTool(registry *SubagentRegistry) *agentcore.Tool {
	seen := &waitSeen{seen: make(map[string]bool)}
	return &agentcore.Tool{
		Name: "sessions_wait",
		Description: strings.Join([]string{
			"Wait for sub-agent activity. Returns a STATUS SUMMARY (ids, tasks, states) —",
			"full outputs are delivered to your inbox when each agent completes.",
			"",
			"- Omit agent_ids: return as soon as any agent not yet reported to you reaches a",
			"  terminal state (completed/failed/interrupted), or on timeout with the still-running",
			"  list. Each completion is reported to you exactly once.",
			"- With agent_ids: block until ALL listed agents reach a terminal state.",
			"- Call again (without agent_ids) to keep collecting completions while other agents",
			"  keep running.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Agent IDs to wait for. Empty = wait for any not-yet-reported agent to finish.",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Max wait time in seconds (default: 300).",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				AgentIDs       []string `json:"agent_ids"`
				TimeoutSeconds int      `json:"timeout_seconds"`
			}
			json.Unmarshal(args, &params)
			if params.TimeoutSeconds <= 0 {
				params.TimeoutSeconds = 300
			}

			deadline := time.After(time.Duration(params.TimeoutSeconds) * time.Second)
			tick := time.NewTicker(200 * time.Millisecond)
			defer tick.Stop()

			// Targeted mode: block until every listed agent is terminal.
			if len(params.AgentIDs) > 0 {
				for _, id := range params.AgentIDs {
					if _, ok := registry.Get(id); !ok {
						return nil, fmt.Errorf("agent %q not found", id)
					}
				}
				for {
					select {
					case <-deadline:
						return map[string]any{
							"timed_out": true,
							"agents":    summarizeRuns(registry, params.AgentIDs),
							"running":   summarizeRuns(registry, registry.ActiveIDs()),
						}, nil
					case <-tick.C:
						allDone := true
						for _, id := range params.AgentIDs {
							r, ok := registry.Get(id)
							if !ok || !isTerminalStatus(r.Status) {
								allDone = false
								break
							}
						}
						if allDone {
							seen.mark(params.AgentIDs)
							allSucceeded := true
							for _, id := range params.AgentIDs {
								run, _ := registry.Get(id)
								allSucceeded = allSucceeded && run != nil && run.Status == "completed"
							}
							return map[string]any{
								"all_finished":  true,
								"all_completed": allSucceeded,
								"all_succeeded": allSucceeded,
								"agents":        summarizeRuns(registry, params.AgentIDs),
								"guidance":      waitGuidance,
							}, nil
						}
					case <-ctx.Done():
						return nil, fmt.Errorf("cancelled")
					}
				}
			}

			// Mailbox mode: return as soon as any unseen agent is terminal.
			for {
				updated, running := splitUnseen(registry, seen)
				if len(updated) > 0 {
					ids := make([]string, len(updated))
					for i, u := range updated {
						ids[i] = u["id"].(string)
					}
					seen.mark(ids)
					return map[string]any{
						"updated":  updated,
						"running":  running,
						"guidance": waitGuidance,
					}, nil
				}
				select {
				case <-deadline:
					return map[string]any{
						"timed_out": true,
						"running":   running,
						"guidance":  waitGuidance,
					}, nil
				case <-tick.C:
					// re-check on next iteration
				case <-ctx.Done():
					return nil, fmt.Errorf("cancelled")
				}
			}
		},
	}
}

const waitGuidance = "A successful wait only means the wait completed, not that child tasks succeeded. Inspect each child's status and error; all_finished includes failures and interruptions, while all_succeeded/all_completed require every child to have succeeded. Full results are delivered to your inbox; use subagents for details. Do not bypass role, scope, or approval restrictions after a child fails."

// splitUnseen partitions the registry into terminal runs not yet reported
// (updated) and still-running runs.
func splitUnseen(registry *SubagentRegistry, seen *waitSeen) (updated, running []map[string]any) {
	for _, r := range registry.List(false) {
		switch {
		case isTerminalStatus(r.Status):
			if seen.isNew(r.ID) {
				updated = append(updated, runSummary(r))
			}
		case r.Status == "running":
			running = append(running, runSummary(r))
		}
	}
	return updated, running
}

// summarizeRuns builds per-agent summaries for the given ids; missing ids
// are skipped (they may have been reaped).
func summarizeRuns(registry *SubagentRegistry, ids []string) []map[string]any {
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		if r, ok := registry.Get(id); ok {
			out = append(out, runSummary(r))
		}
	}
	return out
}

func runSummary(r *SubagentRun) map[string]any {
	entry := map[string]any{
		"id":     r.ID,
		"task":   truncateRunTask(r.Task, 120),
		"status": r.Status,
		"depth":  r.Depth,
	}
	if r.EndedAt != nil {
		entry["ended_at"] = r.EndedAt.Format(time.RFC3339)
	}
	if isTerminalStatus(r.Status) {
		entry["succeeded"] = r.Status == "completed"
	}
	if r.Error != "" {
		entry["error"] = r.Error
	}
	return entry
}

// truncateRunTask caps a task description in wait summaries.
func truncateRunTask(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
