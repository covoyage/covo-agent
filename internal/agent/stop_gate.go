package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/goal"
	"github.com/covoyage/covo-agent/internal/kanban"
	toolsplanning "github.com/covoyage/covo-agent/internal/tools/planning"
	"github.com/covoyage/covonaut/agentcore"
)

// defaultMaxStopGateReentry caps how many times the stop gate may nudge the
// model back into work within a single agent run. A small number, since the
// gate is a last-resort safety net and we rely only on deterministic todo
// state (no judge model), so we cannot distinguish "still working" from
// "stalling" beyond a few tries.
const defaultMaxStopGateReentry = 3

// defaultMaxStopHookReentry caps how many times external stop hooks may block
// the agent from stopping. Higher than the task-gate cap because external hooks
// can perform real verification (e.g. running tests) and make informed decisions.
// Matches the 8-consecutive-block stop limit.
const defaultMaxStopHookReentry = 8

// defaultMaxGoalReentry caps how many times the goal judge may re-enter within a
// single agent run. Higher than the task-gate cap because a judge model can
// actually assess progress, but bounded to limit the extra LLM calls.
const defaultMaxGoalReentry = 8

// goalJudgeTimeout bounds the auxiliary judge LLM call.
const goalJudgeTimeout = 25 * time.Second

// stopGateHook is a "stop gate": when the model is about to finish an agent run
// (a turn that makes no tool calls), it checks the todo board for unfinished
// work. If pending/in-progress items remain, it injects a follow-up message to
// nudge the model to either finish them or explicitly close them out, instead
// of stopping prematurely. Re-entries are capped per run to avoid loops.
//
// This is intentionally deterministic and cheap (no LLM judge): it only fires
// when the model itself recorded todos and left them open. Tasks without a todo
// list are never gated.
type stopGateHook struct {
	agentcore.BaseLifecycleHook
	ca           *CovoAgent
	maxReentry   int
	reentryCount int

	// External stop hook: when configured via .covo-agent-hooks.json with
	// event "stop", an external script is invoked when the agent tries to stop.
	// If the script returns {"decision":"block","reason":"..."}, the agent is
	// forced to continue. Capped at maxStopHookReentry consecutive blocks.
	maxStopHookReentry   int
	stopHookReentryCount int

	// Goal judge: when enabled and a goal is active, an auxiliary LLM evaluates
	// whether the goal is satisfied before allowing the agent to stop.
	judgeEnabled     bool
	maxGoalReentry   int
	goalReentryCount int
}

func newStopGateHook(ca *CovoAgent) *stopGateHook {
	return &stopGateHook{
		ca:                  ca,
		maxReentry:          defaultMaxStopGateReentry,
		maxStopHookReentry:  defaultMaxStopHookReentry,
		judgeEnabled:        envBool("COVO_GOAL_JUDGE", true),
		maxGoalReentry:      defaultMaxGoalReentry,
	}
}

// BeforeAgentRun resets the per-run re-entry budgets so each new user request
// starts with a fresh allowance of nudges.
func (h *stopGateHook) BeforeAgentRun(ctx context.Context, arc *agentcore.AgentRunContext) error {
	h.reentryCount = 0
	h.goalReentryCount = 0
	h.stopHookReentryCount = 0
	return nil
}

// AfterTurn runs at the end of every turn. It only acts on a turn with no tool
// calls — the point at which the agent loop is about to stop and drain the
// follow-up queue. Injecting a FollowUp here re-enters the loop.
func (h *stopGateHook) AfterTurn(ctx context.Context, arc *agentcore.AgentRunContext, info agentcore.TurnInfo) {
	if info.HadToolCalls {
		return // model is still working; the gate only guards the stop point
	}
	if arc == nil || arc.Agent == nil {
		return
	}
	// 1. Deterministic task gate (unfinished todos). Cheap; runs first.
	if h.tryTaskGate(arc) {
		return
	}
	// 2. External stop hook: user-configured scripts can block stopping
	// (e.g. "tests not passing"). Runs before the goal judge because it's
	// cheaper (no LLM call) and user-explicit.
	if h.tryExternalStopHook(arc) {
		return
	}
	// 3. Goal judge gate (LLM): only when an objective is set and unmet.
	h.tryGoalGate(ctx, arc)
}

// tryTaskGate nudges the model back if tasks remain unfinished. Returns true if
// it injected a re-entry.
//
// The kanban board is the authoritative source of truth when the agent has used
// it: if a board exists with tasks, only its state is consulted. The lightweight
// TodoStore is consulted only as a fallback when no kanban board is active
// (agent used the legacy todo tool, or created no tasks at all). This follows
// a "DB truth over model self-report" principle.
func (h *stopGateHook) tryTaskGate(arc *agentcore.AgentRunContext) bool {
	if h.reentryCount >= h.maxReentry {
		return false
	}

	// Prefer the kanban board as the source of truth.
	if km := h.ca.KanbanManager(); km != nil {
		if board := km.ActiveBoard(); board != nil && len(board.Tasks) > 0 {
			var incomplete []*kanban.Task
			for _, s := range []kanban.TaskStatus{kanban.StatusBacklog, kanban.StatusTodo, kanban.StatusInProgress} {
				incomplete = append(incomplete, board.TasksByStatus(s)...)
			}
			if len(incomplete) == 0 {
				return false // kanban is authoritative: all tasks done
			}
			h.reentryCount++
			arc.Agent.FollowUp(agentcore.Message{
				Role:    agentcore.RoleSystem,
				Content: buildKanbanReentry(incomplete, h.reentryCount, h.maxReentry),
			})
			return true
		}
	}

	// Fallback: lightweight in-memory todos.
	store := h.ca.TodoStore()
	if store == nil {
		return false
	}
	reentry, msg := stopGateDecision(store.Read(), h.reentryCount, h.maxReentry)
	if !reentry {
		return false
	}
	h.reentryCount++
	arc.Agent.FollowUp(agentcore.Message{Role: agentcore.RoleSystem, Content: msg})
	return true
}

// tryExternalStopHook invokes user-configured "stop" hooks from
// .covo-agent-hooks.json. If a hook returns {"decision":"block","reason":"..."},
// the agent is forced to continue working. This allows users to define custom
// stop conditions via external scripts (e.g. "tests must pass before stopping").
//
// The hook receives the current conversation context as JSON stdin and can
// run any verification (test suites, lint checks, etc.). Up to
// maxStopHookReentry consecutive blocks are allowed before the agent is
// allowed to stop regardless.
//
// Example .covo-agent-hooks.json:
//
//	{
//	  "hooks": [
//	    {
//	      "event": "stop",
//	      "command": "cd $COVO_WORKDIR && npm test 2>&1 || echo '{\"decision\":\"block\",\"reason\":\"Tests are failing\"}'"
//	    }
//	  ]
//	}
func (h *stopGateHook) tryExternalStopHook(arc *agentcore.AgentRunContext) bool {
	if h.stopHookReentryCount >= h.maxStopHookReentry {
		return false
	}
	if h.ca.shellHooks == nil {
		return false
	}

	// Build the hook payload with conversation context.
	sessionID := h.ca.SessionManager().CurrentID()
	cwd := h.ca.workDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	// Extract last assistant message for context.
	var lastAssistant string
	if msgs := arc.Messages; len(msgs) > 0 {
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == agentcore.RoleAssistant {
				lastAssistant = msgs[i].Content
				if len(lastAssistant) > 2000 {
					lastAssistant = lastAssistant[:2000] + "…"
				}
				break
			}
		}
	}

	payload := &HookEvent{
		EventName: "stop",
		SessionID: sessionID,
		Cwd:       cwd,
		Extra: map[string]any{
			"last_assistant_message": lastAssistant,
			"stop_attempt":           h.stopHookReentryCount + 1,
			"max_stop_attempts":      h.maxStopHookReentry,
		},
	}

	result := h.ca.shellHooks.Invoke("stop", payload)
	if result == nil || !result.Blocked {
		return false
	}

	h.stopHookReentryCount++
	reason := result.Reason
	if reason == "" {
		reason = "External stop hook blocked termination."
	}
	arc.Agent.FollowUp(agentcore.Message{
		Role: agentcore.RoleSystem,
		Content: fmt.Sprintf(
			"An external stop hook has blocked termination: %s\n\nContinue working to address this issue. "+
				"(stop hook block %d/%d)",
			reason, h.stopHookReentryCount, h.maxStopHookReentry,
		),
	})
	return true
}

// buildKanbanReentry composes the nudge shown to the model when it tries to
// stop with unfinished kanban tasks.
func buildKanbanReentry(incomplete []*kanban.Task, attempt, maxAttempt int) string {
	var b strings.Builder
	b.WriteString("You are about to finish, but these kanban tasks are still unfinished:\n")
	for _, t := range incomplete {
		b.WriteString(fmt.Sprintf("  - [%s] %s: %s\n", t.Status, t.ID, t.Title))
	}
	b.WriteString("\nEither complete the remaining work now, or — if a task is no longer needed or is genuinely blocked — update its status to done/cancelled (with a brief reason) before finishing. ")
	b.WriteString("Do not stop with silently unfinished tasks. ")
	b.WriteString(fmt.Sprintf("(automatic continuation check %d/%d)", attempt, maxAttempt))
	return b.String()
}

// tryGoalGate asks an auxiliary judge model whether the active goal is satisfied
// and, if not, re-enters the loop. It fails open: any error (no provider, no
// goal, judge failure) allows the model to stop.
func (h *stopGateHook) tryGoalGate(ctx context.Context, arc *agentcore.AgentRunContext) {
	if !h.judgeEnabled || h.goalReentryCount >= h.maxGoalReentry {
		return
	}

	// Use auxiliary client for the goal judge LLM call when available,
	// falling back to the main provider otherwise.
	var provider agentcore.Provider
	var model string
	if ac := h.ca.auxClient; ac != nil && ac.HasProvider(TaskReview) {
		provider = ac.Provider(TaskReview)
		model = ac.Model(TaskReview)
	} else {
		provider = h.ca.cfg.Provider
		model = h.ca.model
	}
	if provider == nil {
		return
	}
	sessionID := h.ca.SessionManager().CurrentID()
	if sessionID == "" {
		return
	}
	g, err := h.ca.GoalStore().Get(ctx, sessionID)
	if err != nil || g == nil || g.Status != goal.StatusActive || strings.TrimSpace(g.Objective) == "" {
		return
	}

	transcript := buildGoalTranscript(arc.Messages, 6000)
	if strings.TrimSpace(transcript) == "" {
		return
	}

	judgeCtx, cancel := context.WithTimeout(ctx, goalJudgeTimeout)
	defer cancel()
	satisfied, reason, err := judgeGoalSatisfied(judgeCtx, provider, model, g.Objective, transcript)
	if err != nil || satisfied {
		return // fail open, or genuinely done
	}

	h.goalReentryCount++
	arc.Agent.FollowUp(agentcore.Message{
		Role:    agentcore.RoleSystem,
		Content: buildGoalGateReentry(g.Objective, reason, h.goalReentryCount, h.maxGoalReentry),
	})
}

// stopGateDecision is the pure decision core of the stop gate. Given the current
// todo items and re-entry counters, it reports whether the model should be
// nudged to continue and, if so, the message to inject. It returns false when
// there is no outstanding work or the re-entry cap has been reached.
func stopGateDecision(items []toolsplanning.TodoItem, reentryCount, maxReentry int) (bool, string) {
	if reentryCount >= maxReentry {
		return false, ""
	}
	var incomplete []toolsplanning.TodoItem
	for _, t := range items {
		if t.Status == toolsplanning.TodoPending || t.Status == toolsplanning.TodoInProgress {
			incomplete = append(incomplete, t)
		}
	}
	if len(incomplete) == 0 {
		return false, ""
	}
	return true, buildStopGateReentry(incomplete, reentryCount+1, maxReentry)
}

// buildStopGateReentry composes the nudge shown to the model when it tries to
// stop with unfinished todos.
func buildStopGateReentry(incomplete []toolsplanning.TodoItem, attempt, maxAttempt int) string {
	var b strings.Builder
	b.WriteString("You are about to finish, but these todo items are still unfinished:\n")
	for _, t := range incomplete {
		b.WriteString(fmt.Sprintf("  - [%s] %s\n", t.Status, t.Content))
	}
	b.WriteString("\nEither complete the remaining work now, or — if an item is no longer needed or is genuinely blocked — update the todo list to mark it completed/cancelled (with a brief reason) before finishing. ")
	b.WriteString("Do not stop with silently unfinished items. ")
	b.WriteString(fmt.Sprintf("(automatic continuation check %d/%d)", attempt, maxAttempt))
	return b.String()
}

const goalJudgeSystemPrompt = `You are an impartial completion judge for an autonomous coding agent. Given the user's GOAL and a transcript of the agent's recent work, decide whether the goal has been FULLY satisfied.

Be strict: partial progress, intentions, or "I will now..." statements do NOT count as satisfied. The goal is satisfied only if the work to achieve it has actually been completed.

Respond on a single line in exactly this format:
VERDICT: SATISFIED|NOT_SATISFIED — <one short sentence reason>`

// judgeGoalSatisfied asks an auxiliary model whether the goal is fully met.
func judgeGoalSatisfied(ctx context.Context, provider agentcore.Provider, model, objective, transcript string) (bool, string, error) {
	req := &agentcore.ProviderRequest{
		Model: model,
		Messages: []agentcore.Message{
			{Role: agentcore.RoleSystem, Content: goalJudgeSystemPrompt},
			{Role: agentcore.RoleUser, Content: fmt.Sprintf("GOAL:\n%s\n\nRECENT WORK TRANSCRIPT:\n%s", objective, transcript)},
		},
		MaxTokens:   200,
		Temperature: 0,
	}
	resp, err := provider.Complete(ctx, req)
	if err != nil {
		return false, "", err
	}
	satisfied, reason := parseJudgeVerdict(resp.Content)
	return satisfied, reason, nil
}

// parseJudgeVerdict extracts the SATISFIED/NOT_SATISFIED verdict and reason from
// the judge's response. Unknown/garbled output is treated as SATISFIED so the
// gate fails open (never traps the agent on an unparseable judge reply).
func parseJudgeVerdict(s string) (satisfied bool, reason string) {
	up := strings.ToUpper(s)
	// NOT_SATISFIED must be checked before SATISFIED since it contains it.
	notIdx := strings.Index(up, "NOT_SATISFIED")
	if notIdx < 0 {
		notIdx = strings.Index(up, "NOT SATISFIED")
	}
	satIdx := strings.Index(up, "SATISFIED")

	reason = strings.TrimSpace(s)
	if i := strings.Index(reason, "—"); i >= 0 {
		reason = strings.TrimSpace(reason[i+len("—"):])
	} else if i := strings.Index(reason, "-"); i >= 0 && i+1 < len(reason) {
		reason = strings.TrimSpace(reason[i+1:])
	}

	if notIdx >= 0 {
		return false, reason
	}
	if satIdx >= 0 {
		return true, reason
	}
	return true, "" // unparseable → fail open
}

// buildGoalTranscript renders the tail of the conversation for the judge,
// capped to maxChars (most recent messages kept).
func buildGoalTranscript(messages []agentcore.Message, maxChars int) string {
	var parts []string
	total := 0
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		if len(content) > 1200 {
			content = content[:1200] + "…"
		}
		line := fmt.Sprintf("[%s] %s", m.Role, content)
		if total+len(line) > maxChars && len(parts) > 0 {
			break
		}
		parts = append(parts, line)
		total += len(line)
	}
	// parts are newest-first; reverse to chronological order.
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "\n")
}

// carriedStateForCompaction renders a compact block of the agent's live state —
// the active goal objective and any unfinished todos — to be injected into a
// compaction summary so they survive context reconstruction. Returns "" when
// there is nothing to carry. Reads are cheap (in-memory todos + a goal DB get).
func (ca *CovoAgent) carriedStateForCompaction() string {
	var b strings.Builder
	if sid := ca.SessionManager().CurrentID(); sid != "" && ca.GoalStore() != nil {
		if g, err := ca.GoalStore().Get(context.Background(), sid); err == nil &&
			g != nil && g.Status == goal.StatusActive && strings.TrimSpace(g.Objective) != "" {
			b.WriteString("Active goal: " + strings.TrimSpace(g.Objective) + "\n")
		}
	}
	// Prefer kanban board as source of truth for unfinished tasks.
	if km := ca.KanbanManager(); km != nil {
		if board := km.ActiveBoard(); board != nil && len(board.Tasks) > 0 {
			var lines []string
			for _, s := range []kanban.TaskStatus{kanban.StatusBacklog, kanban.StatusTodo, kanban.StatusInProgress} {
				for _, t := range board.TasksByStatus(s) {
					lines = append(lines, fmt.Sprintf("  - [%s] %s: %s", t.Status, t.ID, t.Title))
				}
			}
			if len(lines) > 0 {
				b.WriteString("Unfinished kanban tasks:\n" + strings.Join(lines, "\n") + "\n")
			}
			return strings.TrimSpace(b.String())
		}
	}

	// Fallback: lightweight in-memory todos.
	if store := ca.TodoStore(); store != nil {
		var lines []string
		for _, t := range store.Read() {
			if t.Status == toolsplanning.TodoPending || t.Status == toolsplanning.TodoInProgress {
				lines = append(lines, fmt.Sprintf("  - [%s] %s", t.Status, t.Content))
			}
		}
		if len(lines) > 0 {
			b.WriteString("Unfinished todos:\n" + strings.Join(lines, "\n") + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// buildGoalGateReentry composes the nudge when the judge finds the goal unmet.
func buildGoalGateReentry(objective, reason string, attempt, maxAttempt int) string {
	var b strings.Builder
	b.WriteString("Your stated goal is not yet satisfied:\n")
	b.WriteString("  " + strings.TrimSpace(objective) + "\n")
	if strings.TrimSpace(reason) != "" {
		b.WriteString("\nJudge note: " + strings.TrimSpace(reason) + "\n")
	}
	b.WriteString("\nContinue working toward this goal. Do not stop until it is genuinely met, or — if you are truly blocked — say so explicitly and explain why. ")
	b.WriteString(fmt.Sprintf("(goal completion check %d/%d)", attempt, maxAttempt))
	return b.String()
}
