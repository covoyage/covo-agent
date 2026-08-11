package slashcmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/i18n"
)

// handleImportForeign handles /import-foreign for supported external sessions.
//
// Usage:
//   /import-foreign                — list discoverable foreign sessions
//   /import-foreign <path>         — import a specific foreign session file
//   /import-foreign scan           — rescan for foreign sessions
func handleImportForeign(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}

	// /import-foreign <path> — import a specific file
	if len(parts) >= 2 && parts[1] != "scan" && parts[1] != "list" {
		path := parts[1]
		return importForeignFile(sctx, covoAgent, path)
	}

	// /import-foreign (no args) or /import-foreign list — discover and display
	sessions, err := agent.DiscoverForeignSessions(sctx.Services.HomeDir)
	if err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("discover foreign sessions: %w", err))
		return true
	}

	if len(sessions) == 0 {
		sctx.UI.App.PrintSystem(strings.Join([]string{
			"No foreign sessions found.",
			"",
			"Claude Code sessions are searched in: ~/.claude/projects/",
			"Codex sessions are searched in: ~/.codex/sessions/",
			"",
			"Use /import-foreign <path> to import a specific file.",
		}, "\n"))
		return true
	}

	sctx.UI.App.PrintSystem(fmt.Sprintf("── Foreign sessions (%d) ──", len(sessions)))
	for _, s := range sessions {
		typeIcon := "🤖"
		if s.Type == agent.ForeignClaude {
			typeIcon = "💚" // Nested external session format.
		} else if s.Type == agent.ForeignCodex {
			typeIcon = "🔷" // Flat external session format.
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("  %s [%s] %s — %d msgs, %s",
			typeIcon, s.Type, s.Name, s.MessageCount, s.Modified.Format("2006-01-02 15:04")))
		if s.Preview != "" {
			sctx.UI.App.PrintSystem(fmt.Sprintf("    %s", truncate(s.Preview, 80)))
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("    Path: %s", s.Path))
	}
	sctx.UI.App.PrintSystem("\nUse /import-foreign <path> to import a session.")
	return true
}

// importForeignFile imports a specific foreign session file, auto-detecting
// the format.
func importForeignFile(sctx *SlashContext, covoAgent *agent.CovoAgent, path string) bool {
	// Detect the foreign session type
	sessionType, ok := agent.DetectForeignType(path)
	if !ok {
		// Try to detect from file content
		sctx.UI.App.PrintSystem("Could not detect session type from path. Ensure the file is from ~/.claude/ or ~/.codex/.")
		return true
	}

	messages, err := agent.ConvertForeignSession(path, sessionType)
	if err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("convert foreign session: %w", err))
		return true
	}

	ctx := sctx.Runtime.Context
	sessionMgr := covoAgent.SessionManager()

	// Create a new session and import the converted messages
	id := fmt.Sprintf("%x", time.Now().UnixNano())
	sessionMgr.SetCurrentSessionID(id)
	sessionMgr.DB().EnsureSession(ctx, id, "")

	for _, msg := range messages {
		sessionMgr.DB().AppendMessage(ctx, id, msg)
	}

	// Set title from filename
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	sessionMgr.RenameSession(ctx, id, fmt.Sprintf("[%s] %s", sessionType, name))

	sctx.UI.App.PrintSystem(fmt.Sprintf("✅ Imported %d messages from %s session as %s", len(messages), sessionType, id[:8]))

	// Restore into the active agent
	if ag := sctx.Runtime.Agents.Core(); ag != nil {
		snap, _ := sessionMgr.LoadSession(ctx, id)
		ag.State().Restore(snap)
		if sctx.UI.App != nil {
			sctx.UI.RestoreChatHistory(sctx.UI.App, snap.Messages)
		}
	}
	return true
}
