package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/covoyage/covonaut/agentcore"
)

type FailureRecord struct {
	ToolName  string
	Arguments string
	Error     string
	Turn      int
	Timestamp time.Time
}

type FailureTracker struct {
	mu         sync.Mutex
	failures   []FailureRecord
	maxRecords int
	logger     *slog.Logger
}

func NewFailureTracker(logger *slog.Logger) *FailureTracker {
	return &FailureTracker{
		maxRecords: 50,
		logger:     logger,
	}
}

func (ft *FailureTracker) Record(toolName, args, errMsg string, turn int) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.failures = append(ft.failures, FailureRecord{
		ToolName:  toolName,
		Arguments: args,
		Error:     errMsg,
		Turn:      turn,
		Timestamp: time.Now(),
	})
	if len(ft.failures) > ft.maxRecords {
		ft.failures = ft.failures[len(ft.failures)-ft.maxRecords:]
	}
}

func (ft *FailureTracker) Snapshot() []FailureRecord {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	cp := make([]FailureRecord, len(ft.failures))
	copy(cp, ft.failures)
	return cp
}

func (ft *FailureTracker) Clear() {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.failures = ft.failures[:0]
}

// FailureReviewLifecycleHook records tool failures and triggers a review at session end.
type FailureReviewLifecycleHook struct {
	agentcore.BaseLifecycleHook
	tracker *FailureTracker
	ca      *CovoAgent
}

func NewFailureReviewLifecycleHook(tracker *FailureTracker, ca *CovoAgent) *FailureReviewLifecycleHook {
	return &FailureReviewLifecycleHook{
		tracker: tracker,
		ca:      ca,
	}
}

func (h *FailureReviewLifecycleHook) AfterToolExecution(ctx context.Context, arc *agentcore.AgentRunContext, tec *agentcore.ToolExecutionContext) {
	for i, r := range tec.Results {
		if r.Err != nil {
			h.tracker.Record(tec.ToolCalls[i].Name, tec.ToolCalls[i].Arguments, r.Err.Error(), int(arc.Turn))
		}
	}
}

func (h *FailureReviewLifecycleHook) AfterAgentRun(ctx context.Context, arc *agentcore.AgentRunContext, output string, err error) {
	failures := h.tracker.Snapshot()
	if len(failures) == 0 {
		return
	}
	h.tracker.Clear()

	go h.reviewFailures(failures)
}

func (h *FailureReviewLifecycleHook) reviewFailures(failures []FailureRecord) {
	summary := summarizeFailures(failures)
	if summary == "" {
		return
	}

	if h.ca.memory == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(`Review the following tool failure patterns and extract actionable lessons.
Write a concise memory entry (1-3 sentences) that will help avoid similar failures:

%s

Focus on recurring patterns and clear root causes.`, summary)

	result, err := h.ca.invokeForReview(ctx, "You are an expert at analyzing tool failures and extracting lessons.", prompt)
	if err != nil {
		h.log("failure review failed: %v", err)
		return
	}
	if result == "" {
		return
	}

	if err := h.ca.memory.Add(evolution.MemoryAgent, fmt.Sprintf("Tool failure lesson: %s", result)); err != nil {
		h.log("save failure review: %v", err)
	}
}

func (h *FailureReviewLifecycleHook) log(format string, args ...any) {
	if h.ca.baseCfg.Logger != nil {
		h.ca.baseCfg.Logger.Warn(fmt.Sprintf(format, args...))
	}
}

func summarizeFailures(failures []FailureRecord) string {
	byTool := make(map[string]int)
	var details []string
	for _, f := range failures {
		byTool[f.ToolName]++
		if len(details) < 5 {
			err := f.Error
			if len(err) > 120 {
				err = err[:120] + "..."
			}
			details = append(details, fmt.Sprintf("- %s: %s", f.ToolName, err))
		}
	}

	tools := make([]string, 0, len(byTool))
	for tool := range byTool {
		tools = append(tools, tool)
	}
	sort.Strings(tools)

	summary := ""
	for _, tool := range tools {
		if summary != "" {
			summary += ", "
		}
		summary += fmt.Sprintf("%s x%d", tool, byTool[tool])
	}

	if summary == "" {
		return ""
	}

	result := fmt.Sprintf("Failures: %s\nTotal: %d failures", summary, len(failures))
	for _, d := range details {
		result += "\n" + d
	}
	return result
}
