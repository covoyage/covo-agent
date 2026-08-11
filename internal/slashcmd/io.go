package slashcmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/i18n"
)

// handleExport handles /export, /export-session
func handleExport(sctx *SlashContext, parts []string) bool {
	ag := sctx.Runtime.Agents.Core()
	if ag == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	exportPath := ""
	if len(parts) > 1 {
		exportPath = parts[1]
	}
	sctx.IO.ExportSessionHTML(sctx.UI.App, ag, exportPath)
	return true
}

// handleExportTrajectory handles /export-trajectory
func handleExportTrajectory(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	sctx.IO.ExportTrajectoryJSONL(sctx.UI.App, covoAgent)
	return true
}

// handleCopy handles /copy
func handleCopy(sctx *SlashContext, parts []string) bool {
	ag := sctx.Runtime.Agents.Core()
	if ag == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	n := 0
	if len(parts) > 1 {
		fmt.Sscanf(parts[1], "%d", &n)
	}
	msgs := ag.State().Messages()
	var text string
	found := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == agentcore.RoleAssistant {
			if found == n {
				text = msgs[i].Content
				break
			}
			found++
		}
	}
	if text == "" {
		sctx.UI.App.PrintSystem("(no assistant message to copy)")
		return true
	}
	sctx.IO.CopyToClipboard(text)
	preview := truncate(text, 60)
	sctx.UI.App.PrintSystem(fmt.Sprintf("copied to clipboard: %s", preview))
	return true
}

// handleBtw handles /btw, /side
func handleBtw(sctx *SlashContext, parts []string) bool {
	cmd := strings.TrimPrefix(parts[0], "/")
	if sctx.Runtime.Busy.Load() {
		sctx.UI.App.PrintSystem(i18n.T("system.busy"))
		return true
	}
	question := strings.TrimSpace(strings.TrimPrefix(sctx.Input, "/"+cmd))
	if question == "" {
		sctx.UI.App.PrintSystem("Usage: /btw <question>")
		return true
	}
	ag := sctx.Runtime.Agents.Core()
	if ag == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	savedSnap := ag.State().Snapshot()

	newCA := sctx.Runtime.CreateAgent(sctx.Runtime.ActiveMode())
	if newCA == nil {
		return true
	}
	newCore := newCA.Core()
	defer newCA.Close()

	ctx, cancel := context.WithCancel(sctx.Runtime.Context)
	defer cancel()
	result, err := newCore.Run(ctx, question)
	if err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("side question: %w", err))
	} else {
		preview := truncate(result, 200)
		sctx.UI.App.PrintSystem(fmt.Sprintf("💬 %s", preview))
	}

	ag.State().Restore(savedSnap)
	return true
}

// handleShell handles /shell, /sh, /!
// Note: handleShell is defined in dispatch.go for simplicity (it's in the dispatch routing)
// but we keep a reference here for the /shell suggestion. The actual handler is in dispatch.go.
