package slashcmd

import (
	"fmt"
	"strings"
	"time"

	runtimeapp "github.com/covoyage/covo-agent/internal/app"
)

// handleDashboard handles /dashboard — a unified overview of all active sessions,
// background tasks, and subagents, grouped by status.
//
// This implements the Dashboard concept: a single command that shows
// the state of all concurrent work at a glance. Sessions are grouped by status
// (Working / Idle / Needs input / Completed), and background tasks are listed
// with their current state.
func handleDashboard(sctx *SlashContext, parts []string) bool {
	if sctx.UI.OpenDashboard != nil {
		sctx.UI.OpenDashboard()
		return true
	}
	app := sctx.UI.App
	if app == nil {
		return true
	}

	var b strings.Builder
	b.WriteString("╔══════════════════════════════════════════════════════╗\n")
	b.WriteString("║                    📊 Dashboard                      ║\n")
	b.WriteString("╚══════════════════════════════════════════════════════╝\n\n")

	// --- Section 1: Current Session ---
	b.WriteString("── Current Session ──\n")
	ca := sctx.Runtime.Agents.Current()
	if ca == nil {
		b.WriteString("  (no active agent)\n")
	} else {
		sessionID := ca.SessionManager().CurrentID()
		if sessionID == "" {
			b.WriteString("  (no active session)\n")
		} else {
			shortID := sessionID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			status := "Idle"
			statusIcon := "●"
			if sctx.Runtime.Busy.Load() {
				status = "Working"
				statusIcon = "◐"
			}
			msgCount := 0
			if ag := sctx.Runtime.Agents.Core(); ag != nil {
				msgCount = len(ag.State().Messages())
			}
			b.WriteString(fmt.Sprintf("  %s [%s] %s  (%d messages)\n", statusIcon, status, shortID, msgCount))
		}
	}
	b.WriteString("\n")

	// --- Section 2: Background Tasks ---
	if sctx.Services.BackgroundManager != nil {
		tasks := sctx.Services.BackgroundManager.List()
		b.WriteString(fmt.Sprintf("── Background Tasks (%d) ──\n", len(tasks)))
		if len(tasks) == 0 {
			b.WriteString("  (none)\n")
		} else {
			running, completed, failed := 0, 0, 0
			for _, t := range tasks {
				switch t.Status {
				case runtimeapp.TaskRunning:
					running++
				case runtimeapp.TaskCompleted:
					completed++
				case runtimeapp.TaskFailed:
					failed++
				}
			}
			b.WriteString(fmt.Sprintf("  Summary: %d running, %d completed, %d failed\n\n", running, completed, failed))
			for _, t := range tasks {
				icon := "●"
				switch t.Status {
				case runtimeapp.TaskRunning:
					icon = "◐"
				case runtimeapp.TaskCompleted:
					icon = "✓"
				case runtimeapp.TaskFailed:
					icon = "✗"
				case runtimeapp.TaskCancelled:
					icon = "○"
				}
				age := time.Since(t.StartedAt).Truncate(time.Second)
				b.WriteString(fmt.Sprintf("  %s [%s] %s  turns=%d/%d age=%s\n",
					icon, t.Status, t.ID, t.CurrentTurn, t.Turns, age))
				b.WriteString(fmt.Sprintf("    %s\n", truncate(t.Input, 80)))
				if t.Error != "" {
					b.WriteString(fmt.Sprintf("    ⚠ %s\n", truncate(t.Error, 80)))
				}
			}
		}
		b.WriteString("\n")
	}

	// --- Section 3: All Sessions ---
	mgr := sctx.Runtime.Agents.Current()
	if mgr != nil {
		infos, _ := mgr.SessionManager().ListSessions(sctx.Runtime.Context)
		currentID := mgr.SessionManager().CurrentID()

		b.WriteString(fmt.Sprintf("── Sessions (%d total) ──\n", len(infos)))

		// Group sessions by status.
		var working, idle, completed []sessionDashEntry
		for _, info := range infos {
			entry := sessionDashEntry{
				ID:        info.ID,
				Name:      info.Name,
				Summary:   info.Summary,
				MsgCount:  info.MessageCount,
				UpdatedAt: info.UpdatedAt,
				IsCurrent: info.ID == currentID,
			}

			if entry.IsCurrent && sctx.Runtime.Busy.Load() {
				entry.Status = "Working"
				working = append(working, entry)
			} else if entry.IsCurrent {
				entry.Status = "Needs Input"
				working = append(working, entry) // current session always shows first
			} else if time.Since(info.UpdatedAt) < 5*time.Minute {
				entry.Status = "Idle"
				idle = append(idle, entry)
			} else {
				entry.Status = "Completed"
				completed = append(completed, entry)
			}
		}

		if len(working) > 0 {
			b.WriteString("\n  ▸ Active\n")
			for _, e := range working {
				b.WriteString(formatSessionLine(e))
			}
		}
		if len(idle) > 0 {
			b.WriteString("\n  ▸ Idle (recent)\n")
			for _, e := range idle {
				b.WriteString(formatSessionLine(e))
			}
		}
		if len(completed) > 0 {
			b.WriteString("\n  ▸ Completed\n")
			// Show at most 10 completed sessions.
			shown := completed
			if len(shown) > 10 {
				shown = shown[:10]
			}
			for _, e := range shown {
				b.WriteString(formatSessionLine(e))
			}
			if len(completed) > 10 {
				b.WriteString(fmt.Sprintf("  ... and %d more (use /session to see all)\n", len(completed)-10))
			}
		}
		if len(working)+len(idle)+len(completed) == 0 {
			b.WriteString("  (no sessions)\n")
		}
	}

	b.WriteString("\n")
	b.WriteString("── Quick Actions ──\n")
	b.WriteString("  /session   — browse and switch sessions\n")
	b.WriteString("  /queue     — manage background tasks\n")
	b.WriteString("  /rewind    — rollback to a previous state\n")
	b.WriteString("  /status    — detailed session info\n")

	app.PrintSystem(b.String())
	return true
}

type sessionDashEntry struct {
	ID        string
	Name      string
	Summary   string
	MsgCount  int64
	UpdatedAt time.Time
	IsCurrent bool
	Status    string
}

func formatSessionLine(e sessionDashEntry) string {
	shortID := e.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	name := e.Name
	if name == "" {
		name = shortID
	}
	marker := " "
	if e.IsCurrent {
		marker = "▶"
	}
	summary := e.Summary
	if summary == "" {
		summary = "(no summary)"
	}
	if len(summary) > 60 {
		summary = summary[:57] + "..."
	}
	age := time.Since(e.UpdatedAt).Truncate(time.Minute)
	return fmt.Sprintf("  %s %s  %s  [%s, %d msgs, %s ago]\n    %s\n",
		marker, shortID, name, e.Status, e.MsgCount, age, summary)
}
