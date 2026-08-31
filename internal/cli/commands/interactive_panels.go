package commands

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/component"

	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/slashcmd"
	toolsplanning "github.com/covoyage/covo-agent/internal/tools/planning"
	agentui "github.com/covoyage/covo-agent/internal/tui"
	agentpanels "github.com/covoyage/covo-agent/internal/tui/panels"
)

func (s *interactiveSession) openSessions() {
	mgr := s.agentRuntime.Current()
	if mgr == nil {
		return
	}
	infos, _ := mgr.SessionManager().ListSessions(context.Background())
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
		loadUIBus().ClosePanel(ov)
	}
	refreshItems := func() {
		mgr := s.agentRuntime.Current()
		if mgr == nil {
			return
		}
		infos, _ := mgr.SessionManager().ListSessions(context.Background())
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
		loadUIBus().Host().RequestRender()
	}
	selector.SetOnCancel(closeOverlay)
	selector.SetOnSelect(func(item component.SessionItem) {
		closeOverlay()
		mgr := s.agentRuntime.Current()
		if mgr == nil {
			return
		}
		if err := mgr.SessionManager().ResumeSession(context.Background(), item.ID); err != nil {
			loadUIBus().PrintError(fmt.Errorf("resume session: %w", err))
			return
		}
		snap, _ := mgr.SessionManager().LoadSession(context.Background(), item.ID)
		mgr.Core().State().Restore(snap)
		shared.RestoreChatHistory(s.app, snap.Messages)
		loadUIBus().PrintSystem(i18n.T("system.session_resumed", "id", item.ID[:8]))
		loadUIBus().StatusBar().SetMode(i18n.T("app.session_title", "id", item.ID[:8]))
	})
	selector.SetOnDelete(func(item component.SessionItem) {
		mgr := s.agentRuntime.Current()
		if mgr == nil {
			return
		}
		if err := mgr.SessionManager().DeleteSession(context.Background(), item.ID); err != nil {
			loadUIBus().PrintError(fmt.Errorf("delete session: %w", err))
			return
		}
		loadUIBus().PrintSystem(i18n.T("system.session_deleted", "id", item.ID[:8]))
		refreshItems()
	})
	selector.SetOnRename(func(item component.SessionItem, newName string) {
		mgr := s.agentRuntime.Current()
		if mgr == nil {
			return
		}
		if err := mgr.SessionManager().RenameSession(context.Background(), item.ID, newName); err != nil {
			loadUIBus().PrintError(fmt.Errorf("rename session: %w", err))
			return
		}
		loadUIBus().PrintSystem(i18n.T("system.session_renamed", "id", item.ID[:8], "name", newName))
		refreshItems()
	})
	ov = loadUIBus().ShowPanel(selector, 80, 80)
}

func (s *interactiveSession) openTodos() {
	panel := component.NewTodoPanel()
	readItems := func() []component.TodoItem {
		mgr := s.agentRuntime.Current()
		if mgr == nil {
			return nil
		}
		todos := mgr.TodoStore().Read()
		items := make([]component.TodoItem, 0, len(todos))
		for _, t := range todos {
			items = append(items, component.TodoItem{ID: t.ID, Content: t.Content, Status: string(t.Status), Priority: t.Priority})
		}
		return items
	}
	panel.SetDataProvider(readItems)
	panel.SetItems(readItems())
	panel.SetOnInvalidate(loadUIBus().Host().RequestRender)
	panel.SetOnToggle(func(item component.TodoItem) {
		mgr := s.agentRuntime.Current()
		if mgr == nil {
			return
		}
		store := mgr.TodoStore()
		current := store.Read()
		for i, t := range current {
			if t.ID == item.ID {
				if t.Status == toolsplanning.TodoCompleted {
					current[i].Status = toolsplanning.TodoPending
				} else {
					current[i].Status = toolsplanning.TodoCompleted
				}
				store.Write(current, false)
				loadUIBus().PrintSystem(fmt.Sprintf("TODO %s: %s", current[i].Status, t.Content[:min(40, len(t.Content))]))
				return
			}
		}
	})
	agentui.NewUIBus(s.app).ShowPanel(panel, 80, 70)
}

func (s *interactiveSession) openSessionTree() {
	mgr := s.agentRuntime.Current()
	if mgr == nil {
		return
	}
	infos, _ := mgr.SessionManager().ListSessions(context.Background())
	currentID := mgr.SessionManager().CurrentID()
	tree := agentpanels.NewSessionTree()
	tree.SetCurrentID(currentID)
	tree.SetItems(infos)
	var ov chat.OverlayRef
	closeOverlay := func() {
		loadUIBus().ClosePanel(ov)
	}
	tree.SetOnCancel(closeOverlay)
	tree.SetOnSelect(func(sessionID string) {
		closeOverlay()
		mgr := s.agentRuntime.Current()
		if mgr == nil {
			return
		}
		if err := mgr.SessionManager().ResumeSession(context.Background(), sessionID); err != nil {
			loadUIBus().PrintError(fmt.Errorf("resume session: %w", err))
			return
		}
		snap, _ := mgr.SessionManager().LoadSession(context.Background(), sessionID)
		mgr.Core().State().Restore(snap)
		shared.RestoreChatHistory(s.app, snap.Messages)
		loadUIBus().PrintSystem(i18n.T("system.session_resumed", "id", sessionID[:8]))
		loadUIBus().StatusBar().SetMode(i18n.T("app.session_title", "id", sessionID[:8]))
		loadUIBus().Host().RequestRender()
	})
	ov = loadUIBus().ShowPanel(tree, 80, 80)
}

func (s *interactiveSession) openCommandPalette() {
	if s.app == nil {
		return
	}
	items := agentui.CommandPaletteItems(slashcmd.BuildSlashSuggestions(), agentui.DefaultPaletteActions())
	var ov chat.OverlayRef
	closeOverlay := func() {
		loadUIBus().ClosePanel(ov)
	}
	picker := agentui.NewCommandPalette(items, func(item agentui.PickerItem) {
		closeOverlay()
		s.runPaletteItem(item)
	}, closeOverlay)
	ov = loadUIBus().ShowPanel(picker, 70, 70)
}

func (s *interactiveSession) runPaletteItem(item agentui.PickerItem) {
	value := item.Value
	switch {
	case strings.HasPrefix(value, "slash:"):
		cmd := strings.TrimPrefix(value, "slash:")
		if cmd != "" && !strings.HasPrefix(cmd, "/") {
			cmd = "/" + cmd
		}
		if cmd != "" {
			s.handleSubmit(context.Background(), cmd)
		}
	case value == "action:help":
		s.openKeyHelp()
	case value == "action:sessions":
		s.openSessions()
	case value == "action:todos":
		s.openTodos()
	case value == "action:skills":
		s.openSkillCenter()
	case value == "action:model":
		s.openModelPicker()
	case value == "action:settings":
		s.openSettings()
	case value == "action:queue":
		s.showPromptQueue()
	case value == "action:dashboard":
		s.openDashboard()
	case value == "action:search":
		s.openHistorySearch()
	case value == "action:files":
		openChangedFilesPanel(s.changedFiles, s.workingDir)
	}
}

func (s *interactiveSession) openHistorySearch() {
	if s.app == nil {
		return
	}
	history := s.app.History()
	var ov chat.OverlayRef
	closeOverlay := func() {
		loadUIBus().ClosePanel(ov)
	}
	overlay := agentui.NewHistorySearchOverlay(history, func(match agentui.HistoryMatch) {
		if start, _, ok := history.MessageLineRange(match.MsgIndex); ok {
			history.JumpToAbsoluteLine(int64(start + match.LineIndex))
			loadUIBus().Host().RequestRender()
		}
	}, closeOverlay)
	ov = loadUIBus().ShowPanel(overlay, 80, 20)
}

func (s *interactiveSession) openDashboard() {
	if s.app == nil {
		return
	}
	data := s.dashboardData()
	var ov chat.OverlayRef
	closeOverlay := func() {
		loadUIBus().ClosePanel(ov)
	}
	picker := agentui.NewDashboardPicker(data, func(item agentui.PickerItem) {
		closeOverlay()
		s.runDashboardItem(item)
	}, closeOverlay)
	ov = loadUIBus().ShowPanel(picker, 80, 80)
}

func (s *interactiveSession) dashboardData() agentui.DashboardData {
	data := agentui.DashboardData{Busy: s.busy.Load()}
	mgr := s.agentRuntime.Current()
	if mgr != nil {
		data.CurrentID = mgr.SessionManager().CurrentID()
		if ag := mgr.Core(); ag != nil {
			data.MsgCount = len(ag.State().Messages())
		}
		if data.Busy {
			data.CurrentStatus = i18n.T("dashboard.status_working")
		} else {
			data.CurrentStatus = i18n.T("dashboard.status_idle")
		}
		infos, _ := mgr.SessionManager().ListSessions(context.Background())
		for _, info := range infos {
			status := i18n.T("dashboard.status_idle")
			if info.ID == data.CurrentID && data.Busy {
				status = i18n.T("dashboard.status_working")
			}
			data.Sessions = append(data.Sessions, agentui.DashboardSession{
				ID:        info.ID,
				Name:      info.Name,
				Summary:   info.Summary,
				Status:    status,
				MsgCount:  info.MessageCount,
				UpdatedAt: info.UpdatedAt,
				IsCurrent: info.ID == data.CurrentID,
			})
		}
	}
	if s.bgManager != nil {
		for _, task := range s.bgManager.List() {
			data.Tasks = append(data.Tasks, agentui.DashboardTask{
				ID:          task.ID,
				Input:       task.Input,
				Status:      string(task.Status),
				Error:       task.Error,
				Turns:       task.Turns,
				CurrentTurn: task.CurrentTurn,
				StartedAt:   task.StartedAt,
			})
		}
	}
	return data
}

func (s *interactiveSession) runDashboardItem(item agentui.PickerItem) {
	value := item.Value
	switch {
	case strings.HasPrefix(value, "session:"):
		sessionID := strings.TrimPrefix(value, "session:")
		mgr := s.agentRuntime.Current()
		if mgr == nil || sessionID == "" {
			return
		}
		if err := mgr.SessionManager().ResumeSession(context.Background(), sessionID); err != nil {
			loadUIBus().PrintError(fmt.Errorf("resume session: %w", err))
			return
		}
		snap, _ := mgr.SessionManager().LoadSession(context.Background(), sessionID)
		mgr.Core().State().Restore(snap)
		shared.RestoreChatHistory(s.app, snap.Messages)
		shortID := sessionID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		loadUIBus().PrintSystem(i18n.T("system.session_resumed", "id", shortID))
		loadUIBus().StatusBar().SetMode(i18n.T("app.session_title", "id", shortID))
	case strings.HasPrefix(value, "task:"):
		loadUIBus().PrintSystem(i18n.T("dashboard.task_selected", "id", strings.TrimPrefix(value, "task:")))
	}
}

func (s *interactiveSession) openSkillCenter() {
	mgr := s.agentRuntime.Current()
	if mgr == nil {
		return
	}
	inventory, err := mgr.SkillManager().List()
	if err != nil {
		loadUIBus().PrintError(fmt.Errorf("list skills: %w", err))
		return
	}
	items, skillPaths := buildSkillCenterData(mgr.Config().SkillConfig.AvailableSkills, inventory, mgr.SkillUsage())
	center := component.NewSkillCenter()
	center.SetItems(items)
	center.SetOnInvalidate(loadUIBus().Host().RequestRender)
	center.SetOnSelect(func(item component.SkillItem) {
		content, err := os.ReadFile(skillPaths[item.ID])
		if err != nil {
			if fallback, readErr := mgr.SkillManager().Read(item.ID); readErr == nil {
				content = []byte(fallback)
				err = nil
			}
		}
		if err == nil {
			loadUIBus().PrintSystem(fmt.Sprintf("── Skill: %s ──\n%s", item.Name, content))
		} else {
			loadUIBus().PrintError(fmt.Errorf("failed to read skill %s: %w", item.Name, err))
		}
	})
	agentui.NewUIBus(s.app).ShowPanel(center, 80, 80)
}

func (s *interactiveSession) openExternalEditorPanel() {
	ed := loadUIBus().Editor()
	openExternalEditor(ed)
}

func (s *interactiveSession) openKeyHelp() {
	visibleRows := int64(18)
	if _, rows := s.app.TerminalSize(); rows > 0 {
		if n := rows*70/100 - 3; n >= 6 {
			visibleRows = n
		}
	}
	help := agentui.NewHelpPanel(
		i18n.T("help.title"),
		s.app.Keybindings(),
		visibleRows,
	)
	agentui.NewUIBus(s.app).ShowPanel(help, 70, 70)
}

// installHotkeyRouter wires the global hotkeys (Ctrl+O/T/K/Y/G/R/P/E…) to the
// corresponding panel openers.
func (s *interactiveSession) installHotkeyRouter() {
	s.app.Host().AddChild(agentui.NewHotkeyRouter(agentui.HotkeyRouterConfig{
		Stop:               s.app.Stop,
		PrintSystem:        s.app.PrintSystem,
		OpenSessions:       s.openSessions,
		OpenSessionTree:    s.openSessionTree,
		OpenTodos:          s.openTodos,
		OpenSkillCenter:    s.openSkillCenter,
		OpenCommandPalette: s.openCommandPalette,
		OpenHistorySearch:  s.openHistorySearch,
		OpenDashboard:      s.openDashboard,
		OpenKeyHelp:        s.openKeyHelp,
		OpenModelPicker:    s.openModelPicker,
		OpenEditor:         s.openExternalEditorPanel,
		OpenChangedFiles:   func() { openChangedFilesPanel(s.changedFiles, s.workingDir) },
		OpenSettings:       s.openSettings,
		OpenPromptQueue:    s.showPromptQueue,
		ToggleVerbose: func() {
			on := s.app.History().ToggleVerbose()
			if on {
				s.app.PrintSystem(i18n.T("system.verbose_on"))
			} else {
				s.app.PrintSystem(i18n.T("system.verbose_off"))
			}
			loadUIBus().Host().RequestRender()
		},
		Suggestions: s.suggestionsMgr,
	}))
}
