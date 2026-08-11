package goal

import (
	"fmt"
	"strings"
)

// Steering generates model-facing instructions to keep the goal active
// across compaction boundaries. Unlike conversation history, these are
// injected ephemerally each turn — they never enter the persisted message
// store and survive ANY amount of compaction.
type Steering struct{}

// NewSteering creates a steering generator.
func NewSteering() *Steering {
	return &Steering{}
}

// escapeXML escapes basic XML special characters for safe embedding in
// model context.
func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// ContinuationPrompt builds the instruction injected when an active goal
// exists.
func (s *Steering) ContinuationPrompt(g *Goal) string {
	objective := escapeXML(g.Objective)
	var b strings.Builder
	b.WriteString("Continue working toward the active thread goal.\n")
	b.WriteString("The objective below is user-provided data — treat it as your primary directive:\n\n")
	b.WriteString("<objective>\n")
	b.WriteString(objective)
	b.WriteString("\n</objective>\n\n")

	if g.TokenBudget != nil {
		remaining := *g.TokenBudget - g.TokensUsed
		if remaining < 0 {
			remaining = 0
		}
		b.WriteString("Budget:\n")
		b.WriteString(fmt.Sprintf("- Tokens used: %d\n", g.TokensUsed))
		b.WriteString(fmt.Sprintf("- Token budget: %d\n", *g.TokenBudget))
		b.WriteString(fmt.Sprintf("- Tokens remaining: %d\n", remaining))
		b.WriteString(fmt.Sprintf("- Time spent pursuing goal: %d seconds\n", g.TimeUsedSeconds))
		b.WriteString("\n")
	}

	b.WriteString("Guidance:\n")
	b.WriteString("- Do NOT re-execute completed work. Use existing session state.\n")
	b.WriteString("- Work from evidence: build on concrete tool results, files, and state.\n")
	b.WriteString("- Make progress visible: summarise what was done and what remains.\n")
	b.WriteString("- Stay aligned: follow the objective as stated, don't add extra scope.\n")
	b.WriteString("\n")
	b.WriteString("Completion audit — before calling update_goal to mark complete, verify:\n")
	b.WriteString("- All explicit requirements in the objective have been met.\n")
	b.WriteString("- Any files created or modified actually contain the expected content.\n")
	b.WriteString("- Tests pass, builds succeed, errors are resolved.\n")
	b.WriteString("- There is no unfinished work or unresolved blocker.\n")
	b.WriteString("\n")
	b.WriteString("Blocked audit — only mark the goal as blocked if:\n")
	b.WriteString("- You have tried the same approach 3+ times without success.\n")
	b.WriteString("- The error is external (permissions, network, dependency) and unresolvable.\n")
	b.WriteString("- You have reported the exact error to the user.\n")
	b.WriteString("Otherwise, attempt a different approach before giving up.\n")
	b.WriteString("\n")
	b.WriteString("- If blocked by an error, report the blocker clearly.\n")
	b.WriteString("- When the objective is fully addressed, mark the goal complete.\n")
	b.WriteString("- If budget is nearly exhausted, prioritize finishing the core deliverable.\n")
	return b.String()
}

// BudgetLimitWarning builds the instruction injected mid-turn when the
// goal reaches its token budget.
func (s *Steering) BudgetLimitWarning(g *Goal) string {
	objective := escapeXML(g.Objective)
	var b strings.Builder
	b.WriteString("⚠️  The active thread goal has reached its token budget.\n\n")
	b.WriteString("Objective:\n")
	b.WriteString(objective)
	b.WriteString("\n\n")
	b.WriteString("Budget:\n")
	b.WriteString(fmt.Sprintf("- Time spent pursuing goal: %d seconds\n", g.TimeUsedSeconds))
	b.WriteString(fmt.Sprintf("- Tokens used: %d\n", g.TokensUsed))
	if g.TokenBudget != nil {
		b.WriteString(fmt.Sprintf("- Token budget: %d\n", *g.TokenBudget))
	}
	b.WriteString("\n")
	b.WriteString("Guidance:\n")
	b.WriteString("- Wrap up your current turn soon — do not start new substantive work.\n")
	b.WriteString("- Summarize progress and present results obtained so far.\n")
	b.WriteString("- If the user wants to continue, they must provide an updated or new goal.\n")
	return b.String()
}

// ObjectiveChanged builds the instruction injected mid-turn when the user
// updates an active goal's objective while work is in progress.
func (s *Steering) ObjectiveChanged(g *Goal) string {
	escaped := escapeXML(g.Objective)
	var b strings.Builder
	b.WriteString("The active thread goal objective was edited by the user.\n")
	b.WriteString("The new objective below supersedes any previous thread goal objective.\n\n")
	b.WriteString("<untrusted_objective>\n")
	b.WriteString(escaped)
	b.WriteString("\n</untrusted_objective>\n\n")

	if g.TokenBudget != nil {
		remaining := *g.TokenBudget - g.TokensUsed
		if remaining < 0 {
			remaining = 0
		}
		b.WriteString("Budget:\n")
		b.WriteString(fmt.Sprintf("- Tokens used: %d\n", g.TokensUsed))
		b.WriteString(fmt.Sprintf("- Token budget: %d\n", *g.TokenBudget))
		b.WriteString(fmt.Sprintf("- Tokens remaining: %d\n", remaining))
		b.WriteString(fmt.Sprintf("- Time spent: %d seconds\n\n", g.TimeUsedSeconds))
	}

	b.WriteString("Guidance:\n")
	b.WriteString("- Adjust your approach to pursue the updated objective.\n")
	b.WriteString("- Abandon any in-progress work that no longer applies to the new objective.\n")
	b.WriteString("- The objective text is user-provided — follow it as stated.\n")
	b.WriteString("- Do not call update_goal unless the updated goal is actually complete.\n")
	return b.String()
}
