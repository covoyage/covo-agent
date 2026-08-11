package slashcmd

import (
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/component"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/safego"
	agentui "github.com/covoyage/covo-agent/internal/tui"
)

// slashHandler is the unified signature for all slash command handlers.
type slashHandler func(sctx *SlashContext, parts []string) bool

// slashDispatch maps command names to handler functions.
var slashDispatch = map[string]slashHandler{
	"help":              handleHelp,
	"clear":             handleClear,
	"new":               handleClear,
	"reset":             handleClear,
	"mode":              handleMode,
	"model":             handleModel,
	"provider":          handleProvider,
	"memory":            handleMemory,
	"skill":             handleSkill,
	"quit":              handleQuit,
	"exit":              handleQuit,
	"q":                 handleQuit,
	"statusline":        handleStatusLine,
	"sl":                handleStatusLine,
	"stats":             handleStats,
	"language":          handleLanguage,
	"lang":              handleLanguage,
	"theme":             handleTheme,
	"goal":              handleGoal,
	"session":           handleSession,
	"prune":             handlePrune,
	"resume":            handleResume,
	"import":            handleImport,
	"save":              handleSave,
	"branch":            handleBranch,
	"fork":              handleBranch,
	"title":             handleTitle,
	"label":             handleLabel,
	"history":           handleHistory,
	"checkpoint":        handleCheckpoint,
	"snapshot":          handleSnapshot,
	"unrevert":          handleUnrevert,
	"reasoning":         handleReasoning,
	"personality":       handlePersonality,
	"busy":              handleBusy,
	"template":          handleTemplate,
	"copy":              handleCopy,
	"btw":               handleBtw,
	"side":              handleBtw,
	"export":            handleExport,
	"export-session":    handleExport,
	"export-trajectory": handleExportTrajectory,
	"profile":           handleProfile,
	"curator":           handleCurator,
	"distill":           handleDistill,
	"dream":             handleDream,
	"consolidate":       handleConsolidate,
	"compact":           handleCompact,
	"retry":             handleRetry,
	"undo":              handleRetry,
	"status":            handleStatus,
	"fast":              handleFast,
	"footer":            handleFooter,
	"rewind":            handleRewind,
	"plan":              handlePlan,
	"act":               handleAct,
	"background":        handleBackground,
	"bg":                handleBackground,
	"queue":             handleQueue,
	"jobs":              handleQueue,
	"bg-list":           handleQueue,
	"steer":             handleSteer,
	"cancel":            handleCancel,
	"share":             handleShare,
	"shell":             handleShell,
	"sh":                handleShell,
	"!":                 handleShell,
	"tmux":              handleTmux,
	"commit":            handleCommit,
	"commitments":       handleCommitments,
	"voice":             handleVoice,
	"ptt":               handlePTT,
	"listening":         handleVoice,
	"changes":           handleChanges,
	"mcp":               handleMCP,
	"dashboard":         handleDashboard,
	"loop":              handleLoop,
	"import-foreign":    handleImportForeign,
	"mermaid":           handleMermaid,
	"marketplace":       handleMarketplace,
}

// HandleSlashCommand is the main entry point for slash command processing.
// It replaces the original handleSlashCommand from cmd/covo-agent/slash.go.
func HandleSlashCommand(sctx *SlashContext) bool {
	parts := strings.Fields(sctx.Input)
	if len(parts) == 0 {
		return false
	}
	cmd := strings.TrimPrefix(parts[0], "/")

	// /skill:<name> auto-registered skill command
	if strings.HasPrefix(cmd, "skill:") {
		return handleSkillInvoke(sctx, parts)
	}

	handler, ok := slashDispatch[cmd]
	if !ok {
		return false
	}
	return handler(sctx, parts)
}

func handleSkillInvoke(sctx *SlashContext, parts []string) bool {
	cmd := strings.TrimPrefix(parts[0], "/")
	skillName := strings.TrimPrefix(cmd, "skill:")
	if skillName == "" && len(parts) > 1 {
		skillName = parts[1]
	}
	if skillName == "" {
		sctx.UI.App.PrintSystem("Usage: /skill:<name> — invoke a skill directly")
		return true
	}
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	content, err := covoAgent.SkillManager().ReadPreprocessed(skillName, evolution.PreprocessConfig{
		SessionID: covoAgent.SessionManager().CurrentID(),
	})
	if err != nil {
		sctx.UI.App.PrintSystem(fmt.Sprintf("Skill %q not found or cannot be read", skillName))
		return true
	}
	if ed := sctx.UI.App.Editor(); ed != nil {
		ed.SetValue(content)
	}
	if skill, ok := covoAgent.SkillManager().Find(skillName); ok {
		covoAgent.SkillUsage().RecordView(skill.ID)
	} else {
		covoAgent.SkillUsage().RecordView(skillName)
	}
	return true
}

func handleHelp(sctx *SlashContext, parts []string) bool {
	help := component.NewKeyHelp(sctx.UI.App.Keybindings())
	help.SetTitle("Keybindings — Esc to close")
	agentui.NewUIBus(sctx.UI.App).ShowPanel(help, 70, 70)
	return true
}

// handleClear handles /clear, /new, /reset
func handleClear(sctx *SlashContext, parts []string) bool {
	if sctx.Runtime.Busy.Load() {
		sctx.UI.App.PrintSystem(i18n.T("system.busy"))
		return true
	}
	// Reset the changed-files tracker for the new session.
	if sctx.Services.ResetChangedFiles != nil {
		sctx.Services.ResetChangedFiles()
	}
	newCA := sctx.Runtime.ReplaceAgent(sctx.Runtime.ActiveMode(), false)
	if newCA == nil {
		return true
	}
	if sctx.UI.App != nil {
		sctx.UI.App.History().Clear()
	}
	sctx.UI.App.PrintSystem(i18n.T("system.new_conversation"))
	return true
}

// handleQuit handles /quit, /exit, /q
func handleQuit(sctx *SlashContext, parts []string) bool {
	safego.SafeGo(func() { _ = sctx.UI.App.Stop() }, nil)
	return true
}

// handleStatusLine handles /statusline, /sl
func handleStatusLine(sctx *SlashContext, parts []string) bool {
	sctx.UI.StatusLineManager.ShowDialog(sctx.UI.App)
	return true
}

// handleStats handles /stats
func handleStats(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	sctx.UI.ShowStatsDialog(sctx.UI.App, covoAgent)
	return true
}

// handleStatus handles /status
func handleStatus(sctx *SlashContext, parts []string) bool {
	sctx.UI.ShowStatusInfo(sctx.UI.App, sctx.Runtime.Agents.Current(), sctx.Runtime.Agents.Core())
	return true
}

// handleRewind handles /rewind
func handleRewind(sctx *SlashContext, parts []string) bool {
	ag := sctx.Runtime.Agents.Core()
	if ag == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}

	// Get the CovoAgent for workspace restore.
	ca := sctx.Runtime.Agents.Current()

	sctx.UI.ShowRewindDialog(sctx.UI.App,
		func() agentcore.StateSnapshot { return ag.State().Snapshot() },
		func(snap agentcore.StateSnapshot) {
			// Restore chat history.
			ag.State().Restore(snap)

			// Restore workspace files to the corresponding snapshot.
			if ca == nil {
				return
			}
			sm := ca.SnapshotManager()
			if sm == nil || !sm.Enabled() {
				return
			}
			targetIdx := len(snap.Messages)
			entry, ok := sm.FindClosest(targetIdx)
			if !ok {
				return
			}
			if err := sm.Restore(entry.Hash); err == nil {
				sctx.UI.App.PrintSystem("✅ Workspace restored to snapshot: " + entry.ToolName)
			}
		})
	return true
}

// handlePlan enters Plan execution phase — restricts the agent to read-only
// tools. Mutating tools (write_file, edit, bash, etc.) are blocked by the
// planModeGateBeforeHook and hidden from the LLM's tool list by the toolset
// filter.
func handlePlan(sctx *SlashContext, parts []string) bool {
	if sctx.Runtime.Busy.Load() {
		sctx.UI.App.PrintSystem(i18n.T("system.busy"))
		return true
	}
	ca := sctx.Runtime.Agents.Current()
	if ca == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	if ca.IsPlanMode() {
		sctx.UI.App.PrintSystem("Already in Plan mode — only read-only tools are available. Use /act to switch to Act mode.")
		return true
	}
	ca.SetExecutionPhase(agent.PhasePlan)
	sctx.UI.App.PrintSystem("📋 Entered Plan mode — only read-only tools are available. Present your plan and use exit_plan_mode to transition to Act mode, or use /act to switch manually.")
	return true
}

// handleAct exits Plan execution phase — restores full tool access.
func handleAct(sctx *SlashContext, parts []string) bool {
	ca := sctx.Runtime.Agents.Current()
	if ca == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	if !ca.IsPlanMode() {
		sctx.UI.App.PrintSystem("Already in Act mode — all tools are available. Use /plan to enter Plan mode.")
		return true
	}
	ca.SetExecutionPhase(agent.PhaseAct)
	sctx.UI.App.PrintSystem("⚡ Entered Act mode — all tools are now available.")
	return true
}

// handleShare handles /share
func handleShare(sctx *SlashContext, parts []string) bool {
	ag := sctx.Runtime.Agents.Core()
	if ag == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	sctx.IO.ShareSessionAsGist(sctx.UI.App, ag)
	return true
}

// handleCommitments handles /commitments
func handleCommitments(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	store := covoAgent.CommitmentStore()
	pending := store.ListPending()
	if len(pending) == 0 {
		sctx.UI.App.PrintSystem("No pending commitments.")
		return true
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("── Pending Commitments (%d) ──\n", len(pending)))
	for i, c := range pending {
		b.WriteString(fmt.Sprintf("  %d. [%s] %s\n", i+1, c.ID[:8], c.Description))
	}
	sctx.UI.App.PrintSystem(b.String())
	return true
}

// handleTmux handles /tmux
func handleTmux(sctx *SlashContext, parts []string) bool {
	var sub string
	var rest []string
	if len(parts) > 1 {
		sub = parts[1]
		rest = parts[2:]
	}
	result, err := sctx.Services.HandleTmuxSlash(sub, rest)
	if err != nil {
		sctx.UI.App.PrintSystem(fmt.Sprintf("tmux error: %v", err))
	} else {
		sctx.UI.App.PrintUser(result)
	}
	return true
}

// handleShell handles /shell, /sh, /!
func handleShell(sctx *SlashContext, parts []string) bool {
	cmd := strings.TrimPrefix(parts[0], "/")
	cmdStr := strings.TrimSpace(strings.TrimPrefix(sctx.Input, "/"+cmd))
	if cmdStr == "" {
		sctx.UI.App.PrintSystem("Usage: /shell <command>")
		return true
	}
	if sctx.Runtime.Busy.Load() {
		sctx.UI.App.PrintSystem(i18n.T("system.busy"))
		return true
	}
	sctx.Services.ExecuteShellCommand(sctx.Runtime.Context, cmdStr, sctx.Runtime.WorkingDir, sctx.UI.App, sctx.Runtime.Busy)
	return true
}

// handleChanges opens the changed-files tree overlay.
func handleChanges(sctx *SlashContext, parts []string) bool {
	if sctx.UI.OpenChangedFiles != nil {
		sctx.UI.OpenChangedFiles()
	} else {
		sctx.UI.App.PrintSystem("Changed files tracking is not available.")
	}
	return true
}

// handleMCP opens the MCP server marketplace overlay.
func handleMCP(sctx *SlashContext, parts []string) bool {
	if sctx.UI.OpenMCPMarketplace != nil {
		sctx.UI.OpenMCPMarketplace()
	} else {
		sctx.UI.App.PrintSystem("MCP marketplace is not available.")
	}
	return true
}
