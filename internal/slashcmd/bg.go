package slashcmd

import (
	"fmt"
	"strings"

	"github.com/covoyage/covo-agent/internal/agent"
	runtimeapp "github.com/covoyage/covo-agent/internal/app"
	"github.com/covoyage/covo-agent/internal/i18n"
)

// handleBackground handles /background, /bg
func handleBackground(sctx *SlashContext, parts []string) bool {
	if sctx.Services.BackgroundManager == nil {
		sctx.UI.App.PrintSystem("(background tasks are not available)")
		return true
	}
	cmd := strings.TrimPrefix(parts[0], "/")
	taskInput := strings.TrimSpace(strings.TrimPrefix(sctx.Input, "/"+cmd))
	if taskInput == "" {
		sctx.UI.App.PrintSystem("Usage: /background <task description>")
		return true
	}
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	agentForBg := covoAgent
	id := sctx.Services.BackgroundManager.Start(taskInput, func() *agent.CovoAgent {
		return agentForBg
	}, func(msg string) {
		sctx.UI.App.PrintSystem(msg)
	})
	sctx.UI.App.PrintSystem(fmt.Sprintf("started background task %s", id))
	return true
}

// handleQueue handles /queue, /jobs, /bg-list
func handleQueue(sctx *SlashContext, parts []string) bool {
	if sctx.Services.BackgroundManager == nil {
		sctx.UI.App.PrintSystem("(background tasks are not available)")
		return true
	}
	tasks := sctx.Services.BackgroundManager.List()
	if len(tasks) == 0 {
		sctx.UI.App.PrintSystem("(no background tasks)")
		return true
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("── Background tasks (%d total) ──", len(tasks)))
	for _, t := range tasks {
		statusIcon := "●"
		switch t.Status {
		case runtimeapp.TaskRunning:
			statusIcon = "◐"
		case runtimeapp.TaskCompleted:
			statusIcon = "●"
		case runtimeapp.TaskFailed:
			statusIcon = "✗"
		case runtimeapp.TaskCancelled:
			statusIcon = "○"
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("  %s [%s] %s | turns=%d/%d runtime=%s",
			t.ID, statusIcon, t.Status, t.CurrentTurn, t.Turns, t.Runtime))
		sctx.UI.App.PrintSystem(fmt.Sprintf("    %s", truncate(t.Input, 100)))
	}
	return true
}

// handleSteer handles /steer
func handleSteer(sctx *SlashContext, parts []string) bool {
	if sctx.Services.BackgroundManager == nil {
		sctx.UI.App.PrintSystem("(background tasks are not available)")
		return true
	}
	if len(parts) < 3 {
		sctx.UI.App.PrintSystem("Usage: /steer <bg-id> <instructions>")
		return true
	}
	id := parts[1]
	instructions := strings.Join(parts[2:], " ")
	if err := sctx.Services.BackgroundManager.Steer(id, instructions); err != nil {
		sctx.UI.App.PrintError(err)
		return true
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("steered task %s", id))
	return true
}

// handleCancel handles /cancel
func handleCancel(sctx *SlashContext, parts []string) bool {
	if sctx.Services.BackgroundManager == nil {
		sctx.UI.App.PrintSystem("(background tasks are not available)")
		return true
	}
	if len(parts) < 2 {
		sctx.UI.App.PrintSystem("Usage: /cancel <bg-id>")
		return true
	}
	id := parts[1]
	if err := sctx.Services.BackgroundManager.Cancel(id); err != nil {
		sctx.UI.App.PrintError(err)
		return true
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("cancelled task %s", id))
	return true
}
