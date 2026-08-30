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
	if sctx.Runtime.CreateAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	mode := sctx.Runtime.ActiveMode()
	id := sctx.Services.BackgroundManager.Start(taskInput, func() *agent.CovoAgent {
		return sctx.Runtime.CreateAgent(mode)
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

func handleStop(sctx *SlashContext, parts []string) bool {
	return handleCancel(sctx, parts)
}

func handleLogs(sctx *SlashContext, parts []string) bool {
	if sctx.Services.BackgroundManager == nil {
		sctx.UI.App.PrintSystem("(background tasks are not available)")
		return true
	}
	if len(parts) < 2 {
		sctx.UI.App.PrintSystem("Usage: /logs <bg-id>")
		return true
	}
	out, err := sctx.Services.BackgroundManager.Logs(parts[1])
	if err != nil {
		sctx.UI.App.PrintError(err)
		return true
	}
	if strings.TrimSpace(out) == "" {
		sctx.UI.App.PrintSystem(fmt.Sprintf("(no output yet for %s)", parts[1]))
		return true
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("── logs %s ──\n%s", parts[1], out))
	return true
}

func handleAttach(sctx *SlashContext, parts []string) bool {
	return handleLogs(sctx, parts)
}

func handleRespawn(sctx *SlashContext, parts []string) bool {
	if sctx.Services.BackgroundManager == nil {
		sctx.UI.App.PrintSystem("(background tasks are not available)")
		return true
	}
	if len(parts) < 2 {
		sctx.UI.App.PrintSystem("Usage: /respawn <bg-id>")
		return true
	}
	if sctx.Runtime.CreateAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	mode := sctx.Runtime.ActiveMode()
	newID, err := sctx.Services.BackgroundManager.Respawn(parts[1], func() *agent.CovoAgent {
		return sctx.Runtime.CreateAgent(mode)
	}, func(msg string) {
		sctx.UI.App.PrintSystem(msg)
	})
	if err != nil {
		sctx.UI.App.PrintError(err)
		return true
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("respawned %s as %s", parts[1], newID))
	return true
}

func handleAgents(sctx *SlashContext, parts []string) bool {
	var b strings.Builder
	b.WriteString("── Agents ──\n")
	ca := sctx.Runtime.Agents.Current()
	if ca == nil {
		b.WriteString("  foreground: (none)\n")
	} else {
		status := "idle"
		if sctx.Runtime.Busy.Load() {
			status = "working"
		}
		sessionID := ca.SessionManager().CurrentID()
		if len(sessionID) > 8 {
			sessionID = sessionID[:8]
		}
		b.WriteString(fmt.Sprintf("  foreground [%s] session=%s model=%s\n", status, sessionID, ca.Model()))
	}
	if sctx.Services.BackgroundManager == nil {
		b.WriteString("  background: (unavailable)\n")
		sctx.UI.App.PrintSystem(b.String())
		return true
	}
	tasks := sctx.Services.BackgroundManager.List()
	if len(tasks) == 0 {
		b.WriteString("  background: (none)\n")
	} else {
		b.WriteString(fmt.Sprintf("  background: %d task(s)\n", len(tasks)))
		for _, t := range tasks {
			b.WriteString(fmt.Sprintf("    %s [%s] turns=%d/%d runtime=%s %s\n",
				t.ID, t.Status, t.CurrentTurn, t.Turns, t.Runtime, truncate(t.Input, 80)))
		}
	}
	sctx.UI.App.PrintSystem(strings.TrimSpace(b.String()))
	return true
}
