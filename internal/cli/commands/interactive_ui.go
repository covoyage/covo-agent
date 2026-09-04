package commands

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"time"

	"github.com/covoyage/covonaut/tui"
	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/component"
	core "github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"

	"github.com/covoyage/covo-agent/internal/agent"
	runtimeapp "github.com/covoyage/covo-agent/internal/app"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/cli/commands/model"
	"github.com/covoyage/covo-agent/internal/cli/commands/prefs"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/promptqueue"
	"github.com/covoyage/covo-agent/internal/telemetry"
	agenttheme "github.com/covoyage/covo-agent/internal/theme"
	"github.com/covoyage/covo-agent/internal/tools"
	agentui "github.com/covoyage/covo-agent/internal/tui"
)

func (s *interactiveSession) openModelPicker() {
	if s.app == nil {
		return
	}
	model.ShowTUIModelPicker(s.app, s.providerType, s.model, s.cfg, s.switchProviderModel)
}

// openSettings opens the settings panel (via F2 or /settings).
func (s *interactiveSession) openSettings() {
	if s.app == nil {
		return
	}
	themeOpts := []component.SettingOption{{Value: "", Label: i18n.T("settings.opt_default")}}
	currentTheme, _ := prefs.ReadSkinTheme(s.homeDir)
	themeIdx := 0
	for _, p := range agenttheme.All() {
		themeOpts = append(themeOpts, component.SettingOption{Value: p.Name, Label: p.Name})
		if p.Name == currentTheme {
			themeIdx = len(themeOpts) - 1
		}
	}
	modeIdx := 0
	if s.mode == agent.ModeGeneral {
		modeIdx = 1
	}
	yoloIdx := 0
	if shared.RuntimeState.SessionYolo() {
		yoloIdx = 1
	}
	entries := []component.SettingEntry{
		{
			Key:         "mode",
			Label:       i18n.T("settings.label_mode"),
			Options:     []component.SettingOption{{Value: "code", Label: i18n.T("settings.opt_code")}, {Value: "general", Label: i18n.T("settings.opt_general")}},
			Current:     int64(modeIdx),
			Description: i18n.T("settings.desc_mode"),
		},
		{
			Key:         "theme",
			Label:       i18n.T("settings.label_theme"),
			Options:     themeOpts,
			Current:     int64(themeIdx),
			Description: i18n.T("settings.desc_theme"),
		},
		{
			Key:         "yolo",
			Label:       i18n.T("settings.label_yolo"),
			Options:     []component.SettingOption{{Value: "off", Label: i18n.T("settings.opt_off")}, {Value: "on", Label: i18n.T("settings.opt_on")}},
			Current:     int64(yoloIdx),
			Description: i18n.T("settings.desc_yolo"),
		},
	}
	agentui.ShowSettingsModal(loadUIBus(), entries, func(e component.SettingEntry) {
		value := func() string {
			if e.Current < 0 || int(e.Current) >= len(e.Options) {
				return ""
			}
			return e.Options[e.Current].Value
		}
		switch e.Key {
		case "mode":
			if m, ok := agent.ParseMode(value()); ok && m != s.mode {
				s.switchToMode(m)
				loadUIBus().PrintSystem(i18n.T("system.switched_mode", "mode", string(m)))
			}
		case "theme":
			v := value()
			if err := prefs.WriteSkinTheme(s.homeDir, v); err != nil {
				log.Printf("write skin: %v", err)
				return
			}
			if v == "" {
				shared.ApplySkinOverrides(s.homeDir, s.configTheme)
			} else {
				shared.ApplyNamedTheme(v)
			}
			loadUIBus().PrintSystem(i18n.T("system.theme_set", "name", v))
		case "yolo":
			on := value() == "on"
			shared.RuntimeState.SetSessionYolo(on)
			if ca := s.agentRuntime.Current(); ca != nil {
				if approvalSys := ca.ApprovalSystem(); approvalSys != nil {
					if on {
						approvalSys.EnableSessionYolo("settings")
					} else {
						approvalSys.DisableSessionYolo("settings")
					}
				}
			}
			if s.permissionGate != nil {
				s.permissionGate.YoloMode = on
			}
			if on {
				loadUIBus().PrintSystem(i18n.T("system.yolo_on"))
			} else {
				loadUIBus().PrintSystem(i18n.T("system.yolo_off"))
			}
		}
	})
}

// showPromptQueue lists the pending prompts (via Ctrl+Shift+P or /prompts).
func (s *interactiveSession) showPromptQueue() {
	if s.app == nil {
		return
	}
	queue := shared.RuntimeState.PromptQueue()
	pane := agentui.NewQueuePane(queue)
	var ov chat.OverlayRef
	closePane := func() {
		loadUIBus().ClosePanel(ov)
	}
	pane.SetOnClose(closePane)
	pane.SetOnRemove(func(entry promptqueue.Entry) {
		if queue != nil {
			queue.Remove(entry.ID)
		}
		loadUIBus().Host().RequestRender()
	})
	pane.SetOnSendNow(func(entry promptqueue.Entry) {
		if queue != nil {
			queue.Remove(entry.ID)
		}
		closePane()
		s.submitPrompt(context.Background(), entry.Text)
	})
	ov = loadUIBus().ShowPanel(pane, 80, 70)
}

// buildChatApp assembles the chat application around the submit handler.
func (s *interactiveSession) buildChatApp(slashSuggestions, atSuggestions []core.Suggestion) *chat.ChatApp {
	app := tui.NewChatApp(chat.ChatAppConfig{
		Title: fmt.Sprintf(
			"covo-agent · provider=%s model=%s mode=%s",
			s.providerType, s.model, s.mode,
		),
		ReasoningRenderer: &chat.DefaultReasoningRenderer{
			Show: s.showReasoning,
			Mode: s.thinkingMode,
		},
		ShowTimings:               true,
		ShowTurns:                 true,
		AltScreen:                 true,
		Scrollback:                s.historyMode == "scrollback",
		ProbeTerminal:             true,
		EditorPlaceholder:         i18n.T("app.composer_placeholder"),
		EditorStartingPlaceholder: i18n.T("system.starting"),
		EditorBusyPlaceholder:     i18n.T("app.composer_busy"),
		MouseMode:                 shared.DefaultMouseMode(),
		KittyKeyboardMode:         shared.DefaultKeyboardMode(),
		KittyKeyboardFlags:        shared.DefaultKeyboardFlags(),
		// Transcript display caps. Tune here (or surface in config) without
		// touching covonaut internals; zero values fall back to defaults.
		Limits: chat.DisplayLimits{
			ToolArgMaxRunes:    2000,
			ToolResultMaxLines: 400,
			ToolResultMaxBytes: 32 << 10,
			ToolStatusMaxWidth: 120,
		},
		Providers: []core.AutocompleteProvider{
			&component.StaticProvider{
				TriggerStr:  "/",
				Suggestions: slashSuggestions,
			},
			// File/folder providers — trigger on "@file" and "@folder" for
			// interactive filesystem navigation.
			agentui.NewFilePathBrowser("@file", s.workingDir, func() bool {
				return cli.IsEnabled("fuzzy-file-search")
			}),
			agentui.NewFilePathBrowser("@folder", s.workingDir, func() bool {
				return cli.IsEnabled("fuzzy-file-search")
			}),
			&component.StaticProvider{
				TriggerStr:  "@",
				Suggestions: atSuggestions,
			},
		},
		OnSubmit: s.handleSubmit,
		OnQueue:  s.handleQueue,
		ExpandSubmit: func(input string) string {
			if s.pasteStore == nil {
				return input
			}
			return s.pasteStore.Expand(input)
		},
		OnInterrupt: func() {
			if cf := s.cancelRun.Load(); cf != nil && *cf != nil {
				(*cf)()
				loadUIBus().PrintSystem(i18n.T("system.interrupted"))
			}
		},
		OnHistoryJump: s.handleHistoryJump,
		OnImagePaste: func() {
			s.handleImagePaste()
		},
		OnTextPaste: func(text string) (string, bool) {
			if ref, ok := agentui.FileRefFromPaste(text, s.workingDir); ok {
				return ref, true
			}
			if !agentui.ShouldChipPaste(text) {
				return "", false
			}
			if s.pasteStore == nil {
				s.pasteStore = agentui.NewPasteStore()
			}
			return s.pasteStore.Store(text), true
		},
		Filter: func(_ core.Component, msg core.Msg) core.Msg {
			if key, ok := msg.(core.KeyMsg); ok && terminal.MatchesKey(key.Data, "ctrl+k") {
				s.openCommandPalette()
				return nil
			}
			return msg
		},
		OnGhostRequest: agentui.NewAIGhostProvider(s.llm, shared.GhostModelFromConfig(s.cfg, s.providerType)).Handle,
	})
	if ed := app.Editor(); ed != nil {
		ed.SetTextFn(agentui.StylePasteChips)
	}
	return app
}

// handleImagePaste reads an image from the clipboard and injects a reference
// into the editor.
func (s *interactiveSession) handleImagePaste() {
	path, err := cli.ClipboardImagePaste()
	if err != nil {
		if s.app != nil {
			loadUIBus().PrintSystem(i18n.T("system.no_image"))
		}
		return
	}
	if s.app != nil {
		// Inject image reference into editor
		ed := loadUIBus().Editor()
		ed.SetValue(ed.GetValue() + " [image:" + filepath.Base(path) + "]")
		loadUIBus().PrintSystem(i18n.T("system.image_pasted", "name", filepath.Base(path)))
		// Store the path for submit-time injection
		s.pendingImages.Store(filepath.Base(path), path)
	}
}

// wireFooter creates the sticky footer and status line, connects the slash
// context builder, and installs the status renderers.
func (s *interactiveSession) wireFooter() {
	s.stickyFooter = agentui.NewStickyFooter()
	s.stickyFooter.SetShortcuts(i18n.T("statusline.shortcuts"))
	s.stickyFooter.SetMode(string(s.mode))

	s.statusLineMgr = agentui.NewStatusLineManager()
	s.stickyFooter.SetStatusLineManager(s.statusLineMgr)
	s.slashContext = newSlashContextBuilder(slashCompositionConfig{
		App:           s.app,
		Busy:          &s.busy,
		Agents:        s.agentRuntime,
		State:         shared.RuntimeState,
		OpenDashboard: s.openDashboard,
		ActiveMode:    func() agent.AgentMode { return s.mode },
		CreateAgent:   s.createAgent,
		ReplaceAgent: func(mode agent.AgentMode, preserveState bool) *agent.CovoAgent {
			replacement, err := s.replaceAgent(s.requestFor(mode, s.llm, s.providerType, s.model), preserveState)
			if err != nil {
				log.Printf("replace agent: %v", err)
			}
			return replacement
		},
		SwitchToMode:         s.switchToMode,
		SwitchModel:          s.switchModel,
		SwitchProvider:       s.switchProvider,
		OpenModelPicker:      s.openModelPicker,
		OpenSettings:         s.openSettings,
		OpenPromptQueue:      s.showPromptQueue,
		StashComposerDraft:   s.stashComposerDraft,
		RestoreComposerDraft: s.restoreComposerDraft,
		BackgroundManager:    s.bgManager,
		StatusLineManager:    s.statusLineMgr,
		WorkingDir:           s.workingDir,
		HomeDir:              s.homeDir,
		ChangedFiles:         s.changedFiles,
	})
	s.statusLineMgr.SetRenderFn("mode", func(pal *theme.Palette) string {
		md := s.stickyFooter.Snapshot().Mode
		if md == "" {
			return ""
		}
		modeIcon := "◇"
		modeStyle := pal.Dim
		switch md {
		case "code":
			modeIcon = "⚙"
			modeStyle = pal.Accent
			md = i18n.T("statusline.mode_code")
		case "general":
			modeIcon = "◆"
			modeStyle = pal.Success
			md = i18n.T("statusline.mode_general")
		}
		// Append Plan/Act phase indicator when in Plan mode.
		if ca := s.agentRuntime.Current(); ca != nil && ca.IsPlanMode() {
			md = md + " │ " + pal.Accent.Render("📋 Plan")
		}
		return modeStyle.Render(fmt.Sprintf("%s %s", modeIcon, md))
	})
	s.statusLineMgr.SetRenderFn("bg-tasks", func(pal *theme.Palette) string {
		n := s.stickyFooter.Snapshot().BgTaskCount
		if n <= 0 {
			return ""
		}
		return pal.Accent.Render(i18n.T("statusline.bg_tasks", "count", fmt.Sprintf("%d", n)))
	})
	s.statusLineMgr.SetRenderFn("git-branch", func(pal *theme.Palette) string {
		b := s.stickyFooter.Snapshot().GitBranch
		if b == "" {
			return ""
		}
		return pal.Dim.Render(b)
	})
	s.statusLineMgr.SetRenderFn("context-used", func(pal *theme.Palette) string {
		snap := s.stickyFooter.Snapshot()
		if snap.ContextTotal > 0 {
			bar := agentui.RenderContextBar(snap.ContextTokens, snap.ContextTotal, agentui.DefaultContextBarConfig(), pal)
			if bar != "" {
				return bar
			}
		}
		if snap.ContextUsed == "" {
			return ""
		}
		style := pal.Dim
		if snap.ContextWarn {
			style = pal.Error
		}
		return style.Render(snap.ContextUsed)
	})
	s.statusLineMgr.SetRenderFn("cost", func(pal *theme.Palette) string {
		ca := s.agentRuntime.Current()
		if ca == nil {
			return ""
		}
		cost := ca.CostTracker().CurrentCost()
		if cost <= 0 {
			return ""
		}
		return pal.Dim.Render(fmt.Sprintf("$%.4f", cost))
	})
	s.statusLineMgr.SetRenderFn("queued", func(pal *theme.Palette) string {
		queue := shared.RuntimeState.PromptQueue()
		if queue == nil || queue.IsEmpty() {
			return ""
		}
		return pal.Accent.Render(i18n.T("statusline.queued_label", "count", strconv.Itoa(queue.Len())))
	})
	s.statusLineMgr.SetRenderFn("shortcuts", func(pal *theme.Palette) string {
		txt := s.stickyFooter.Snapshot().Shortcuts
		if txt == "" {
			return ""
		}
		return pal.Dim.Render(txt)
	})

	s.app.SetFooter(s.stickyFooter)
}

// pumpFooterStatus periodically refreshes the footer: git branch, todo store,
// context usage, and background task count.
func (s *interactiveSession) pumpFooterStatus() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.app.Done():
			s.gitTracker.Stop()
			return
		case <-ticker.C:
			branch := s.gitTracker.Branch()
			s.stickyFooter.SetGitBranch(branch)
			s.stickyFooter.SetTodoStore(func() []agentui.TodoItem {
				ca := s.agentRuntime.Current()
				if ca == nil {
					return nil
				}
				todos := ca.TodoStore().Read()
				items := make([]agentui.TodoItem, 0, len(todos))
				for _, t := range todos {
					items = append(items, agentui.TodoItem{
						ID:       t.ID,
						Content:  t.Content,
						Status:   string(t.Status),
						Priority: t.Priority,
					})
				}
				return items
			})

			ca := s.agentRuntime.Current()
			if ca != nil {
				promptTokens := ca.CostTracker().LastPromptTokens()
				if promptTokens > 0 {
					// Compute context window usage percentage.
					ctxLen := int64(0)
					if ce := ca.Core().ContextEngine(); ce != nil {
						ctxLen = ce.ContextLength()
					}
					s.stickyFooter.SetContextTokens(promptTokens, ctxLen)
					if ctxLen > 0 {
						pct := promptTokens * 100 / ctxLen
						if pct > 999 {
							pct = 999
						}
						ctxK := promptTokens / 1024
						totalK := ctxLen / 1024
						s.stickyFooter.SetContextUsage(
							fmt.Sprintf("ctx: %dk/%dk (%d%%)", ctxK, totalK, pct))
						s.stickyFooter.SetContextWarn(pct >= 80)
					} else {
						s.stickyFooter.SetContextUsage(fmt.Sprintf("ctx: %d tokens", promptTokens))
						s.stickyFooter.SetContextWarn(false)
					}
				}
			}

			runningCount := 0
			if s.bgManager != nil {
				for _, t := range s.bgManager.List() {
					if t.Status == runtimeapp.TaskRunning {
						runningCount++
					}
				}
			}
			s.stickyFooter.SetBgTaskCount(runningCount)

			s.app.Host().RequestRender()
		}
	}
}

// printWelcome appends the welcome banner to the chat history.
func (s *interactiveSession) printWelcome() {
	welcomeInfo := agentui.WelcomeInfo{
		Provider:   s.providerType,
		Model:      s.model,
		Mode:       string(s.mode),
		WorkingDir: s.workingDir,
	}
	if agentCore := s.agent.Core(); agentCore != nil {
		welcomeInfo.ToolCount = len(agentCore.ToolNames())
	}
	welcomeInfo.SkillCount = len(s.agent.Config().SkillConfig.AvailableSkills)
	s.app.History().Append(chat.ChatMessage{
		Role: chat.RoleAssistant,
		Text: agentui.BuildWelcomeMessage(welcomeInfo),
	})
}

// registerKeybindings declares the global application keybindings.
func (s *interactiveSession) registerKeybindings() {
	s.app.Keybindings().Register("app.help", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+/"},
		Description: i18n.T("keybinding.help"),
	})
	s.app.Keybindings().Register("app.quit", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+q", "ctrl+d"},
		Description: i18n.T("keybinding.quit"),
	})
	s.app.Keybindings().Register("app.session", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+o"},
		Description: i18n.T("keybinding.session"),
	})
	s.app.Keybindings().Register("app.todo", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+t"},
		Description: i18n.T("keybinding.todo"),
	})
	s.app.Keybindings().Register("app.palette", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+k"},
		Description: i18n.T("keybinding.palette"),
	})
	s.app.Keybindings().Register("app.search", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+s"},
		Description: i18n.T("keybinding.search"),
	})
	s.app.Keybindings().Register("app.skills", terminal.KeybindingDef{
		Description: i18n.T("keybinding.skills"),
	})
	s.app.Keybindings().Register("app.interrupt", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+c"},
		Description: i18n.T("keybinding.interrupt"),
	})
	s.app.Keybindings().Register("app.model-picker", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+p"},
		Description: i18n.T("keybinding.model_picker"),
	})
	s.app.Keybindings().Register("app.editor", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+e"},
		Description: i18n.T("keybinding.editor"),
	})
	s.app.Keybindings().Register("app.session-tree", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+y"},
		Description: i18n.T("keybinding.session_tree"),
	})
	s.app.Keybindings().Register("app.changed-files", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+g"},
		Description: i18n.T("keybinding.changed_files"),
	})
}

// buildCronScheduler creates (but does not start) the cron scheduler that
// runs scheduled prompts through a throwaway agent.
func (s *interactiveSession) buildCronScheduler() *tools.CronScheduler {
	cronStore := tools.NewCronStore(s.homeDir)
	return tools.NewCronScheduler(cronStore, func(ctx context.Context, jobID, prompt string) (string, error) {
		if s.busy.Load() {
			return "", fmt.Errorf("busy — cron job %s skipped", jobID)
		}
		runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		ca := s.createAgent(s.mode)
		if ca == nil {
			return "", fmt.Errorf("failed to create agent for cron job %s", jobID)
		}
		defer ca.Close()
		// Flush this cron job's spans promptly; the interactive session keeps
		// running in this process, so do not shut down the pipeline.
		defer telemetry.FlushOtel(context.Background())
		output, err := ca.RunDirectWithSession(runCtx, prompt, "cron-"+jobID)
		if err != nil {
			return "", fmt.Errorf("cron job %s: %w", jobID, err)
		}
		return output, nil
	})
}
