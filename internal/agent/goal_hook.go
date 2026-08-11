package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/goal"
	"github.com/covoyage/covonaut/agentcore"
)

// goalAccountingHook wires the GoalAccountingState into the agent lifecycle,
// hooking into token-usage and tool-completion events to track goal progress.
type goalAccountingHook struct {
	agentcore.BaseLifecycleHook
	ca          *CovoAgent
	acctState   *goal.AccountingState
	currentTurn string
}

// newGoalAccountingHook creates a lifecycle hook for goal accounting.
func newGoalAccountingHook(ca *CovoAgent) *goalAccountingHook {
	return &goalAccountingHook{
		ca:        ca,
		acctState: goal.NewAccountingState(),
	}
}

// AccountingState returns the in-memory accounting state.
func (h *goalAccountingHook) AccountingState() *goal.AccountingState {
	return h.acctState
}

// BeforeAgentRun starts tracking at the beginning of an agent run.
func (h *goalAccountingHook) BeforeAgentRun(ctx context.Context, arc *agentcore.AgentRunContext) error {
	h.currentTurn = fmt.Sprintf("turn_%d", time.Now().UnixNano())
	planMode := h.detectPlanMode(arc)
	h.acctState.StartTurn(h.currentTurn, goal.TokenUsage{}, planMode)
	h.acctState.ResetSteering()
	return nil
}

// detectPlanMode checks if the current run is in plan mode.
func (h *goalAccountingHook) detectPlanMode(arc *agentcore.AgentRunContext) bool {
	cfg := h.ca.Config()
	return cfg.Name == "plan" || cfg.Name == "architect"
}

// AfterModelCall records token usage after each LLM call.
func (h *goalAccountingHook) AfterModelCall(ctx context.Context, arc *agentcore.AgentRunContext, mcc *agentcore.ModelCallContext) {
	if mcc.Response == nil || mcc.Response.Usage.TotalTokens == 0 {
		return
	}
	if h.acctState.PlanMode() {
		return
	}

	usage := goal.TokenUsage{
		InputTokens:  mcc.Response.Usage.PromptTokens,
		OutputTokens: mcc.Response.Usage.CompletionTokens,
		TotalTokens:  mcc.Response.Usage.TotalTokens,
	}

	h.acctState.RecordTokenUsage(h.currentTurn, usage)
}

// AfterToolExecution accounts progress after tools complete.
func (h *goalAccountingHook) AfterToolExecution(ctx context.Context, arc *agentcore.AgentRunContext, tec *agentcore.ToolExecutionContext) {
	if h.acctState.PlanMode() {
		return
	}
	// Skip non-productive outcomes and the update_goal tool itself.
	if !toolCountsForGoalProgress(tec) {
		return
	}
	sessionID := h.ca.SessionManager().CurrentID()
	if sessionID == "" {
		return
	}
	g, err := h.ca.GoalStore().Get(ctx, sessionID)
	if err != nil || g == nil || g.Status != goal.StatusActive {
		return
	}

	// Snapshot (read-only) and account
	snap := h.acctState.Snapshot(h.currentTurn)
	if snap == nil {
		return
	}

	outcome, err := h.ca.GoalAccounting().RecordUsage(ctx, sessionID,
		snap.TokenDelta, snap.TimeDeltaSeconds,
		goal.AccountingActiveOnly, &g.GoalID,
	)
	if err == nil && outcome.Updated {
		h.acctState.MarkProgressAccounted(h.currentTurn, snap.TimeDeltaSeconds)
	}

	// If budget just exceeded, inject mid-turn budget limit steering (once per run)
	if outcome != nil && outcome.BudgetExceeded && h.acctState.BudgetLimitReported(g.GoalID) {
		g, _ = h.ca.GoalStore().Get(ctx, sessionID)
		if g != nil && g.Status == goal.StatusBudgetLimited {
			warning := h.ca.GoalSteering().BudgetLimitWarning(g)
			h.ca.core.State().AddMessage(agentcore.Message{
				Role:    agentcore.RoleSystem,
				Content: warning,
			})
			h.acctState.SteeringInjected("budget_limit")
		}
	}
}

// AfterTurn accounts remaining progress at turn end.
func (h *goalAccountingHook) AfterTurn(ctx context.Context, arc *agentcore.AgentRunContext, info agentcore.TurnInfo) {
	sessionID := h.ca.SessionManager().CurrentID()
	if sessionID == "" || h.acctState.PlanMode() {
		h.acctState.FinishTurn(h.currentTurn)
		return
	}
	snap := h.acctState.Snapshot(h.currentTurn)
	h.acctState.FinishTurn(h.currentTurn)

	g, _ := h.ca.GoalStore().Get(ctx, sessionID)
	if g == nil || g.Status.IsTerminal() {
		return
	}
	if snap == nil {
		return
	}

	// Final accounting for this turn, using ActiveOrComplete for completing turns
	mode := goal.AccountingActiveOnly
	if g.Status == goal.StatusComplete {
		mode = goal.AccountingActiveOrComplete
	}
	outcome, err := h.ca.GoalAccounting().RecordUsage(ctx, sessionID,
		snap.TokenDelta, snap.TimeDeltaSeconds,
		mode, &g.GoalID,
	)
	if err == nil && outcome.Updated {
		h.acctState.MarkProgressAccounted(h.currentTurn, snap.TimeDeltaSeconds)
	}
}

// AfterAgentRun handles goal state transitions on run-level errors.
// Accounts pending progress first, then blocks active goals.
func (h *goalAccountingHook) AfterAgentRun(ctx context.Context, arc *agentcore.AgentRunContext, output string, runErr error) {
	// Account pending progress before changing goal state (don't lose unflushed tokens).
	sessionID := h.ca.SessionManager().CurrentID()
	if sessionID != "" && !h.acctState.PlanMode() {
		snap := h.acctState.Snapshot(h.currentTurn)
		if snap != nil {
			g, _ := h.ca.GoalStore().Get(ctx, sessionID)
			if g != nil && !g.Status.IsTerminal() {
				outcome, err := h.ca.GoalAccounting().RecordUsage(ctx, sessionID,
					snap.TokenDelta, snap.TimeDeltaSeconds,
					goal.AccountingActiveOnly, &g.GoalID,
				)
				if err == nil && outcome.Updated {
					h.acctState.MarkProgressAccounted(h.currentTurn, snap.TimeDeltaSeconds)
				}
			}
		}
	}

	h.acctState.ClearActiveGoal()
	if runErr == nil {
		return
	}
	if sessionID == "" {
		return
	}
	g, _ := h.ca.GoalStore().Get(ctx, sessionID)
	if g == nil || g.Status != goal.StatusActive {
		return
	}

	// On any non-retryable error, block the goal to prevent infinite loops.
	if isRateLimitError(runErr) {
		_, _ = h.ca.GoalStore().UsageLimit(ctx, sessionID)
	} else {
		_, _ = h.ca.GoalStore().Block(ctx, sessionID)
	}
}

// isRateLimitError detects rate-limit / usage-limit errors from the error message.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return containsAny(msg, "rate limit", "rate_limit", "usage limit", "usage_limit",
		"quota exceeded", "too many requests", "429", "RateLimitError")
}

func containsAny(s string, substrs ...string) bool {
	sLower := strings.ToLower(s)
	for _, sub := range substrs {
		if strings.Contains(sLower, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

// toolCountsForGoalProgress filters tool executions that should count toward
// goal progress. Excludes the update_goal/create_goal tools themselves.
func toolCountsForGoalProgress(tec *agentcore.ToolExecutionContext) bool {
	if len(tec.ToolCalls) == 0 {
		return false
	}
	// Exclude goal management tools from progress counting
	for _, tc := range tec.ToolCalls {
		if tc.Name == "update_goal" || tc.Name == "create_goal" {
			return false
		}
	}
	return true
}
