package slashcmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/tui/chat"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/goal"
	"github.com/covoyage/covo-agent/internal/i18n"
)

// handleGoal handles /goal
func handleGoal(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	ctx := sctx.Runtime.Context
	sessionID := covoAgent.SessionManager().CurrentID()
	if sessionID == "" {
		sctx.UI.App.PrintSystem("(no active session)")
		return true
	}

	// /goal: show current goal
	if len(parts) < 2 {
		g, err := covoAgent.GoalStore().Get(ctx, sessionID)
		if err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("goal: %w", err))
			return true
		}
		if g == nil {
			sctx.UI.App.PrintSystem("(no active goal)")
			return true
		}
		showGoalInfo(sctx.UI.App, g)
		return true
	}

	sub := parts[1]
	switch sub {
	case "set":
		if len(parts) < 3 {
			sctx.UI.App.PrintSystem("Usage: /goal set <objective>")
			return true
		}
		accountIdleProgress(ctx, covoAgent, sessionID)
		objective := strings.Join(parts[2:], " ")
		g := &goal.Goal{
			SessionID: sessionID,
			Objective: objective,
			Status:    goal.StatusActive,
		}
		if err := covoAgent.GoalStore().Put(ctx, g); err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("goal set: %w", err))
			return true
		}
		if as := covoAgent.GoalAccountingState(); as != nil {
			as.ResetSteering()
			as.MarkGoalActive(g.GoalID)
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("✓ Goal set: %s", objective))
	case "pause":
		g, err := covoAgent.GoalStore().Pause(ctx, sessionID)
		if err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("goal pause: %w", err))
			return true
		}
		if g == nil {
			sctx.UI.App.PrintSystem("(no active goal to pause)")
		} else {
			sctx.UI.App.PrintSystem("✓ Goal paused")
		}
	case "resume":
		g, err := covoAgent.GoalStore().Resume(ctx, sessionID)
		if err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("goal resume: %w", err))
			return true
		}
		if g == nil {
			sctx.UI.App.PrintSystem("(no paused goal to resume)")
		} else {
			sctx.UI.App.PrintSystem("✓ Goal resumed")
		}
	case "complete", "done":
		g, err := covoAgent.GoalStore().Complete(ctx, sessionID)
		if err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("goal complete: %w", err))
			return true
		}
		if g == nil {
			sctx.UI.App.PrintSystem("(no goal to complete)")
		} else {
			sctx.UI.App.PrintSystem("✓ Goal completed")
		}
	case "block":
		if len(parts) < 3 {
			sctx.UI.App.PrintSystem("Usage: /goal block <reason>")
			return true
		}
		g, err := covoAgent.GoalStore().Block(ctx, sessionID)
		if err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("goal block: %w", err))
			return true
		}
		if g == nil {
			sctx.UI.App.PrintSystem("(no active goal to block)")
		} else {
			sctx.UI.App.PrintSystem(fmt.Sprintf("✓ Goal blocked: %s", strings.Join(parts[2:], " ")))
		}
	case "delete", "clear":
		accountIdleProgress(ctx, covoAgent, sessionID)
		g, err := covoAgent.GoalStore().Delete(ctx, sessionID)
		if err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("goal delete: %w", err))
			return true
		}
		if g == nil {
			sctx.UI.App.PrintSystem("(no goal to delete)")
		} else {
			if as := covoAgent.GoalAccountingState(); as != nil {
				as.ClearActiveGoal()
			}
			sctx.UI.App.PrintSystem("✓ Goal deleted")
		}
	default:
		sctx.UI.App.PrintSystem("Usage: /goal [set|pause|resume|complete|block|delete]")
	}
	return true
}

func showGoalInfo(app *chat.ChatApp, g *goal.Goal) {
	budget := "none"
	if g.TokenBudget != nil {
		budget = fmt.Sprintf("%d (used: %d)", *g.TokenBudget, g.TokensUsed)
	}
	statusIcon := map[goal.Status]string{
		goal.StatusActive:        "●",
		goal.StatusPaused:        "◐",
		goal.StatusBlocked:       "✕",
		goal.StatusUsageLimited:  "⚠",
		goal.StatusBudgetLimited: "⏹",
		goal.StatusComplete:      "✓",
	}
	icon := statusIcon[g.Status]
	app.PrintSystem(fmt.Sprintf("── Goal [%s %s] ──", icon, g.Status))
	app.PrintSystem(fmt.Sprintf("  Objective: %s", g.Objective))
	app.PrintSystem(fmt.Sprintf("  Budget:    %s | Time: %ds", budget, g.TimeUsedSeconds))
	app.PrintSystem(fmt.Sprintf("  ID:        %s", g.GoalID[:8]))
}

// accountIdleProgress flushes pending idle time to the goal store before
// an external goal mutation.
func accountIdleProgress(ctx context.Context, ca *agent.CovoAgent, sessionID string) {
	as := ca.GoalAccountingState()
	if as == nil {
		return
	}
	snap := as.IdleSnapshot()
	if snap == nil || snap.TimeDeltaSeconds <= 0 {
		return
	}
	g, _ := ca.GoalStore().Get(ctx, sessionID)
	if g == nil || g.Status.IsTerminal() {
		return
	}
	outcome, err := ca.GoalAccounting().RecordUsage(ctx, sessionID,
		0, snap.TimeDeltaSeconds,
		goal.AccountingActiveOnly, &g.GoalID,
	)
	if err == nil && outcome.Updated {
		as.MarkIdleProgressAccounted(snap.TimeDeltaSeconds)
	}
}
