package slashcmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/component"

	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/safego"
)

// handleSession handles /session
func handleSession(sctx *SlashContext, parts []string) bool {
	if sctx.Runtime.Busy.Load() {
		sctx.UI.App.PrintSystem(i18n.T("system.busy"))
		return true
	}
	mgr := sctx.Runtime.Agents.Current()
	if mgr == nil {
		return true
	}
	infos, _ := mgr.SessionManager().ListSessions(sctx.Runtime.Context)
	currentID := mgr.SessionManager().CurrentID()
	var items []component.SessionItem
	for _, info := range infos {
		items = append(items, component.SessionItem{
			ID:            info.ID,
			Name:          info.Name,
			Label:         info.Label,
			ParentSession: info.ParentSession,
			Preview:       info.Summary,
			CreatedAt:     info.CreatedAt.Format("01/02 15:04"),
			UpdatedAt:     info.UpdatedAt.Format("01/02 15:04"),
			MsgCount:      info.MessageCount,
			IsCurrent:     info.ID == currentID,
		})
	}
	selector := component.NewSessionSelector()
	selector.SetItems(items)
	var ov chat.OverlayRef
	closeOverlay := func() {
		sctx.Runtime.State.UI().ClosePanel(ov)
	}
	refreshItems := func() {
		mgr := sctx.Runtime.Agents.Current()
		if mgr == nil {
			return
		}
		infos, _ := mgr.SessionManager().ListSessions(sctx.Runtime.Context)
		currentID := mgr.SessionManager().CurrentID()
		var newItems []component.SessionItem
		for _, info := range infos {
			newItems = append(newItems, component.SessionItem{
				ID:            info.ID,
				Name:          info.Name,
				Label:         info.Label,
				ParentSession: info.ParentSession,
				Preview:       info.Summary,
				CreatedAt:     info.CreatedAt.Format("01/02 15:04"),
				UpdatedAt:     info.UpdatedAt.Format("01/02 15:04"),
				MsgCount:      info.MessageCount,
				IsCurrent:     info.ID == currentID,
			})
		}
		selector.SetItems(newItems)
		sctx.Runtime.State.UI().Host().RequestRender()
	}
	selector.SetOnCancel(closeOverlay)
	selector.SetOnSelect(func(item component.SessionItem) {
		closeOverlay()
		mgr := sctx.Runtime.Agents.Current()
		if mgr == nil {
			return
		}
		if err := mgr.SessionManager().ResumeSession(sctx.Runtime.Context, item.ID); err != nil {
			sctx.Runtime.State.UI().PrintError(fmt.Errorf("resume session: %w", err))
			return
		}
		snap, _ := mgr.SessionManager().LoadSession(sctx.Runtime.Context, item.ID)
		mgr.Core().State().Restore(snap)
		sctx.UI.RestoreChatHistory(sctx.UI.App, snap.Messages)
		sctx.Runtime.State.UI().PrintSystem(i18n.T("system.session_resumed", "id", item.ID[:8]))
		sctx.Runtime.State.UI().StatusBar().SetMode(i18n.T("app.session_title", "id", item.ID[:8]))
	})
	selector.SetOnDelete(func(item component.SessionItem) {
		mgr := sctx.Runtime.Agents.Current()
		if mgr == nil {
			return
		}
		if err := mgr.SessionManager().DeleteSession(sctx.Runtime.Context, item.ID); err != nil {
			sctx.Runtime.State.UI().PrintError(fmt.Errorf("delete session: %w", err))
			return
		}
		sctx.Runtime.State.UI().PrintSystem(i18n.T("system.session_deleted", "id", item.ID[:8]))
		refreshItems()
	})
	selector.SetOnRename(func(item component.SessionItem, newName string) {
		mgr := sctx.Runtime.Agents.Current()
		if mgr == nil {
			return
		}
		if err := mgr.SessionManager().RenameSession(sctx.Runtime.Context, item.ID, newName); err != nil {
			sctx.Runtime.State.UI().PrintError(fmt.Errorf("rename session: %w", err))
			return
		}
		sctx.Runtime.State.UI().PrintSystem(i18n.T("system.session_renamed", "id", item.ID[:8], "name", newName))
		refreshItems()
	})
	ov = sctx.Runtime.State.UI().ShowPanel(selector, 80, 80)
	return true
}

// handlePrune handles /prune
func handlePrune(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	if sctx.Runtime.Busy.Load() {
		sctx.UI.App.PrintSystem(i18n.T("system.busy"))
		return true
	}
	days := 0
	if len(parts) >= 2 {
		days, _ = strconv.Atoi(parts[1])
	}
	if days < 0 {
		days = 0
	}
	ctx := sctx.Runtime.Context
	sctx.UI.App.PrintSystem(i18n.T("system.pruning_sessions"))
	deleted, err := covoAgent.SessionManager().PruneSessions(ctx, days)
	if err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("prune: %w", err))
		return true
	}
	if days == 0 {
		sctx.UI.App.PrintSystem(i18n.T("system.pruned_all", "count", fmt.Sprintf("%d", deleted)))
	} else {
		sctx.UI.App.PrintSystem(i18n.T("system.pruned_older", "count", fmt.Sprintf("%d", deleted), "days", fmt.Sprintf("%d", days)))
	}
	return true
}

// handleResume handles /resume
func handleResume(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	if sctx.Runtime.Busy.Load() {
		sctx.UI.App.PrintSystem(i18n.T("system.busy"))
		return true
	}
	if len(parts) < 2 {
		sctx.UI.App.PrintSystem("Usage: /resume <session_id>")
		return true
	}
	sessionID := parts[1]
	ctx := sctx.Runtime.Context
	if err := covoAgent.SessionManager().ResumeSession(ctx, sessionID); err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("resume session: %w", err))
		return true
	}
	fullID := covoAgent.SessionManager().CurrentID()
	sctx.UI.App.PrintSystem(i18n.T("system.resumed_session", "id", fullID[:8]))
	if ag := sctx.Runtime.Agents.Core(); ag != nil {
		snap, _ := covoAgent.SessionManager().LoadSession(ctx, fullID)
		ag.State().Restore(snap)
		if sctx.UI.App != nil {
			sctx.UI.RestoreChatHistory(sctx.UI.App, snap.Messages)
		}
	}
	return true
}

// handleImport handles /import
func handleImport(sctx *SlashContext, parts []string) bool {
	if sctx.Runtime.Busy.Load() {
		sctx.UI.App.PrintSystem(i18n.T("system.busy"))
		return true
	}
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	if len(parts) < 2 {
		sctx.UI.App.PrintSystem("Usage: /import <jsonl-path>")
		return true
	}
	path := parts[1]
	ctx := sctx.Runtime.Context
	sessionMgr := covoAgent.SessionManager()
	id, count, err := sctx.IO.ImportSessionFromJSONL(ctx, sessionMgr, path)
	if err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("import: %w", err))
		return true
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("imported %d messages as session %s", count, id[:8]))
	if ag := sctx.Runtime.Agents.Core(); ag != nil {
		snap, _ := covoAgent.SessionManager().LoadSession(ctx, id)
		ag.State().Restore(snap)
		if sctx.UI.App != nil {
			sctx.UI.RestoreChatHistory(sctx.UI.App, snap.Messages)
		}
	}
	return true
}

// handleSave handles /save
func handleSave(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	ctx := sctx.Runtime.Context
	ag := sctx.Runtime.Agents.Core()
	if ag == nil {
		sctx.UI.App.PrintSystem("(no active agent to save)")
		return true
	}
	snap := ag.State().Snapshot()
	sessionID := covoAgent.SessionManager().CurrentID()
	if sessionID == "" {
		sctx.UI.App.PrintSystem("(no active session — start a conversation first)")
		return true
	}
	if err := covoAgent.SessionManager().Store().Save(ctx, sessionID, snap); err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("save session: %w", err))
		return true
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("session %s saved (%d messages)", sessionID[:8], len(snap.Messages)))
	return true
}

// handleBranch handles /branch, /fork
func handleBranch(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	ctx := sctx.Runtime.Context
	newID, err := covoAgent.SessionManager().ForkSession(ctx)
	if err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("fork session: %w", err))
		return true
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("forked session %s — current path preserved", newID[:8]))
	return true
}

// handleTitle handles /title
func handleTitle(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		sctx.UI.App.PrintSystem("Usage: /title <session name>")
		return true
	}
	name := strings.Join(parts[1:], " ")
	ctx := sctx.Runtime.Context
	sessionID := covoAgent.SessionManager().CurrentID()
	if sessionID == "" {
		sctx.UI.App.PrintSystem("(no active session — start a conversation first)")
		return true
	}
	if err := covoAgent.SessionManager().RenameSession(ctx, sessionID, name); err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("rename session: %w", err))
		return true
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("session renamed to: %s", name))
	return true
}

// handleLabel handles /label
func handleLabel(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	sessionID := covoAgent.SessionManager().CurrentID()
	if sessionID == "" {
		sctx.UI.App.PrintSystem("(no active session — start a conversation first)")
		return true
	}

	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		infos, err := covoAgent.SessionManager().ListSessions(sctx.Runtime.Context)
		if err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("list sessions: %w", err))
			return true
		}
		for _, info := range infos {
			if info.ID == sessionID {
				if info.Label != "" {
					sctx.UI.App.PrintSystem(fmt.Sprintf("current label: %s", info.Label))
				} else {
					sctx.UI.App.PrintSystem("(no label set)")
				}
				break
			}
		}
		sctx.UI.App.PrintSystem("Usage: /label <tag>")
		return true
	}
	label := strings.Join(parts[1:], " ")
	if err := covoAgent.SessionManager().SetLabel(sctx.Runtime.Context, sessionID, label); err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("set label: %w", err))
		return true
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("session labeled: %s", label))
	return true
}

// handleHistory handles /history
func handleHistory(sctx *SlashContext, parts []string) bool {
	ag := sctx.Runtime.Agents.Core()
	if ag == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	msgs := ag.State().Messages()
	if len(msgs) == 0 {
		sctx.UI.App.PrintSystem("(no conversation history)")
		return true
	}
	count := len(msgs)
	if len(parts) > 1 {
		if n, err := fmt.Sscanf(parts[1], "%d", &count); err != nil || n != 1 || count <= 0 {
			count = len(msgs)
		}
		if count > len(msgs) {
			count = len(msgs)
		}
	}
	start := len(msgs) - count
	if start < 0 {
		start = 0
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("── Conversation (%d of %d messages) ──", count, len(msgs)))
	for _, msg := range msgs[start:] {
		roleStr := strings.ToUpper(string(msg.Role))
		preview := truncate(msg.Content, 120)
		sctx.UI.App.PrintSystem(fmt.Sprintf("  [%s] %s", roleStr, preview))
	}
	return true
}

// handleCheckpoint handles /checkpoint
func handleCheckpoint(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}

	sm := covoAgent.SnapshotManager()
	if sm == nil || !sm.Enabled() {
		sctx.UI.App.PrintSystem("(snapshots not available — git required)")
		return true
	}

	// /checkpoint restore <index> — restore both chat and workspace to a
	// snapshot entry.
	if len(parts) >= 3 && parts[1] == "restore" {
		idx, err := strconv.Atoi(parts[2])
		if err != nil {
			sctx.UI.App.PrintSystem(fmt.Sprintf("Invalid checkpoint index: %q (use a number)", parts[2]))
			return true
		}
		entry, ok := sm.Get(idx)
		if !ok {
			sctx.UI.App.PrintSystem(fmt.Sprintf("Checkpoint %d not found", idx))
			return true
		}

		// Restore workspace files.
		restoredFiles := false
		if err := sm.Restore(entry.Hash); err == nil {
			restoredFiles = true
		}

		// Restore chat history.
		ag := sctx.Runtime.Agents.Core()
		if ag != nil && entry.MessageIndex > 0 {
			snap := ag.State().Snapshot()
			if entry.MessageIndex < len(snap.Messages) {
				snap.Messages = snap.Messages[:entry.MessageIndex]
				ag.State().Restore(snap)
				sctx.UI.RestoreChatHistory(sctx.UI.App, snap.Messages)
			}
		}

		sctx.UI.App.PrintSystem(fmt.Sprintf("✅ Restored to checkpoint [%d]: %s (workspace: %v)",
			idx, entry.ToolName, restoredFiles))
		return true
	}

	// Default: list snapshots.
	sctx.UI.App.PrintSystem(sm.FormatList())
	return true
}

// handleSnapshot handles /snapshot
func handleSnapshot(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	mgr := covoAgent.SnapshotManager()
	if mgr == nil || !mgr.Enabled() {
		sctx.UI.App.PrintSystem("(file snapshots not available — git required)")
		return true
	}
	if len(parts) < 2 || parts[1] == "list" {
		sctx.UI.App.PrintSystem(mgr.FormatList())
		return true
	}
	switch parts[1] {
	case "diff":
		// /snapshot diff [N] — diff between snapshot N and current working tree.
		// /snapshot diff N M — diff between snapshots N and M.
		if len(parts) >= 4 {
			fromIdx, err1 := strconv.Atoi(parts[2])
			toIdx, err2 := strconv.Atoi(parts[3])
			if err1 != nil || err2 != nil {
				sctx.UI.App.PrintSystem(fmt.Sprintf("Invalid indices: %q %q (use numbers)", parts[2], parts[3]))
				return true
			}
			diff, err := mgr.DiffBetween(fromIdx, toIdx)
			if err != nil {
				sctx.UI.App.PrintError(fmt.Errorf("diff: %w", err))
				return true
			}
			if diff == "" {
				sctx.UI.App.PrintSystem("(no differences between snapshots)")
			} else {
				sctx.UI.App.PrintSystem(fmt.Sprintf("── Diff: snapshot [%d] → [%d] ──\n%s", fromIdx, toIdx, diff))
			}
			return true
		}
		idx := -1 // default: diff last snapshot vs working tree
		if len(parts) >= 3 {
			n, err := strconv.Atoi(parts[2])
			if err != nil {
				sctx.UI.App.PrintSystem(fmt.Sprintf("Invalid snapshot index: %q (use a number)", parts[2]))
				return true
			}
			idx = n
		}
		diff, err := mgr.Diff(idx)
		if err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("diff: %w", err))
			return true
		}
		label := "last snapshot"
		if idx >= 0 {
			label = fmt.Sprintf("snapshot [%d]", idx)
		}
		if diff == "" {
			sctx.UI.App.PrintSystem(fmt.Sprintf("(no changes since %s)", label))
		} else {
			sctx.UI.App.PrintSystem(fmt.Sprintf("── Diff: %s → working tree ──\n%s", label, diff))
		}
		return true
	case "revert":
		if mgr.List() == nil || len(mgr.List()) < 2 {
			sctx.UI.App.PrintSystem("(not enough snapshots to revert — need at least 2)")
			return true
		}
		if len(parts) < 3 {
			n, err := mgr.Undo()
			if err != nil {
				sctx.UI.App.PrintError(fmt.Errorf("undo: %w", err))
				return true
			}
			sctx.UI.App.PrintSystem(fmt.Sprintf("✓ Undid last step (%d files reverted). Use /unrevert to restore.", n))
			return true
		}
		idx, err := strconv.Atoi(parts[2])
		if err != nil {
			sctx.UI.App.PrintSystem(fmt.Sprintf("Invalid snapshot index: %q (use a number)", parts[2]))
			return true
		}
		n, err := mgr.RevertTo(idx)
		if err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("revert to %d: %w", idx, err))
			return true
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("✓ Reverted to snapshot [%d] (%d files restored). Use /unrevert to undo.", idx, n))
	default:
		sctx.UI.App.PrintSystem("Usage: /snapshot [list | diff [N] [N M] | revert [N]]")
	}
	return true
}

// handleUnrevert handles /unrevert
func handleUnrevert(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	mgr := covoAgent.SnapshotManager()
	if mgr == nil || !mgr.Enabled() {
		sctx.UI.App.PrintSystem("(file snapshots not available — git required)")
		return true
	}
	if err := mgr.Unrevert(); err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("unrevert: %w", err))
		return true
	}
	sctx.UI.App.PrintSystem("✓ Restored working tree to pre-revert state")
	return true
}

// handleRetry handles /retry, /undo
func handleRetry(sctx *SlashContext, parts []string) bool {
	cmd := strings.TrimPrefix(parts[0], "/")
	if sctx.Runtime.Busy.Load() {
		sctx.UI.App.PrintSystem(i18n.T("system.busy"))
		return true
	}
	ag := sctx.Runtime.Agents.Core()
	if ag == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	undoCount := 1
	if cmd == "undo" && len(parts) > 1 {
		if n, err := fmt.Sscanf(parts[1], "%d", &undoCount); err != nil || n != 1 {
			undoCount = 1
		}
		if undoCount < 1 {
			undoCount = 1
		}
		if undoCount > 20 {
			undoCount = 20
		}
	}

	snap := ag.State().Snapshot()
	msgs := snap.Messages
	usersFound := 0
	cutIdx := len(msgs)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == agentcore.RoleUser {
			usersFound++
			if usersFound == undoCount {
				cutIdx = i
				break
			}
		}
	}
	if cutIdx >= len(msgs) {
		sctx.UI.App.PrintSystem("(no previous message to retry)")
		return true
	}

	lastUserText := msgs[cutIdx].Content
	snap.Messages = msgs[:cutIdx]
	ag.State().Restore(snap)
	if sctx.UI.App != nil {
		sctx.UI.RestoreChatHistory(sctx.UI.App, snap.Messages)
	}
	if undoCount == 1 {
		sctx.UI.App.PrintSystem(fmt.Sprintf("retrying: %s", truncate(lastUserText, 60)))
	} else {
		sctx.UI.App.PrintSystem(fmt.Sprintf("undone %d exchanges, retrying: %s", undoCount, truncate(lastUserText, 60)))
	}
	ctx, cancel := context.WithCancel(sctx.Runtime.Context)
	sctx.Runtime.Busy.Store(true)
	safego.SafeGo(func() {
		defer sctx.Runtime.Busy.Store(false)
		defer cancel()
		ag.Run(ctx, lastUserText)
	}, nil)
	return true
}
