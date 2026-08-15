package main

import (
	"context"
	"sync/atomic"

	"github.com/covoyage/covonaut/tui/chat"

	"github.com/covoyage/covo-agent/internal/agent"
	runtimeapp "github.com/covoyage/covo-agent/internal/app"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/session"
	"github.com/covoyage/covo-agent/internal/slashcmd"
)

type slashCompositionConfig struct {
	App               *chat.ChatApp
	Busy              *atomic.Bool
	Agents            *runtimeapp.AgentRuntime
	State             *runtimeapp.RuntimeState
	ActiveMode        func() agent.AgentMode
	CreateAgent       func(agent.AgentMode) *agent.CovoAgent
	ReplaceAgent      func(agent.AgentMode, bool) *agent.CovoAgent
	SwitchToMode      func(agent.AgentMode)
	SwitchModel       func(string)
	SwitchProvider    func(string) error
	OpenModelPicker   func()
	OpenSettings      func()
	OpenPromptQueue   func()
	BackgroundManager slashcmd.BackgroundManager
	StatusLineManager slashcmd.StatusLineManager
	WorkingDir        string
	HomeDir           string
	ChangedFiles      *ChangedFilesTracker
}

func newSlashContextBuilder(config slashCompositionConfig) *slashcmd.ContextBuilder {
	return &slashcmd.ContextBuilder{
		Runtime: slashcmd.RuntimeDependencies{
			Busy:           config.Busy,
			Agents:         config.Agents,
			State:          config.State,
			ActiveMode:     config.ActiveMode,
			CreateAgent:    config.CreateAgent,
			ReplaceAgent:   config.ReplaceAgent,
			SwitchToMode:   config.SwitchToMode,
			SwitchModel:    config.SwitchModel,
			SwitchProvider: config.SwitchProvider,
			WorkingDir:     config.WorkingDir,
		},
		UI: slashcmd.UIDependencies{
			App:                config.App,
			StatusLineManager:  config.StatusLineManager,
			OpenModelPicker:    config.OpenModelPicker,
			RestoreChatHistory: restoreChatHistory,
			ShowStatsDialog:    showStatsDialog,
			ShowStatusInfo:     showStatusInfo,
			ShowRewindDialog:   showRewindDialog,
			ApplyNamedTheme:    applyNamedTheme,
			OpenChangedFiles: func() {
				openChangedFilesPanel(config.ChangedFiles, config.WorkingDir)
			},
			OpenMCPMarketplace: openMCPMarketplace,
			OpenSettings:       config.OpenSettings,
			OpenPromptQueue:    config.OpenPromptQueue,
		},
		IO: slashcmd.IODependencies{
			ExportSessionHTML:     exportSessionHTML,
			ExportTrajectoryJSONL: exportTrajectoryJSONL,
			ShareSessionAsGist:    shareSessionAsGist,
			CopyToClipboard:       cli.CopyToClipboard,
			ImportSessionFromJSONL: func(ctx context.Context, manager interface{}, path string) (string, int, error) {
				return importSessionFromJSONL(ctx, manager.(*session.Manager), path)
			},
		},
		Services: slashcmd.ServiceDependencies{
			BackgroundManager:   config.BackgroundManager,
			ExecuteShellCommand: executeShellCommand,
			HandleTmuxSlash:     cli.HandleTmuxSlash,
			HomeDir:             config.HomeDir,
			WriteSkinTheme:      writeSkinTheme,
			NotifyGatewayFooter: notifyGatewayFooter,
			ReadTemplate:        readTemplate,
			ExpandTemplateArgs:  expandTemplateArgs,
			TemplateList:        templateList,
			ResetChangedFiles: func() {
				if config.ChangedFiles != nil {
					config.ChangedFiles.Reset()
				}
			},
			Personalities: runtimeapp.Personalities(),
		},
	}
}
