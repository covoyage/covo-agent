package slashcmd

import (
	"context"
	"sync/atomic"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"

	"github.com/covoyage/covo-agent/internal/agent"
	runtimeapp "github.com/covoyage/covo-agent/internal/app"
)

// BackgroundManager is the interface that SlashContext expects for background task operations.
type BackgroundManager interface {
	Start(input string, createAgent func() *agent.CovoAgent, notify func(string)) string
	List() []runtimeapp.TaskSummary
	Steer(id, instructions string) error
	Cancel(id string) error
	Get(id string) (runtimeapp.TaskSummary, bool)
	Logs(id string) (string, error)
	Respawn(id string, createAgent func() *agent.CovoAgent, notify func(string)) (string, error)
}

// StatusLineManager is the interface that SlashContext expects for status line operations.
type StatusLineManager interface {
	ShowDialog(app *chat.ChatApp)
}

// ImportSessionFunc is the signature for importing sessions from JSONL.
// The manager argument is kept as interface{} to avoid importing the session package.
type ImportSessionFunc func(ctx context.Context, mgr interface{}, path string) (string, int, error)

// RuntimeDependencies groups runtime controls and mutable runtime state.
type RuntimeDependencies struct {
	Context        context.Context
	Busy           *atomic.Bool
	Agents         *runtimeapp.AgentRuntime
	State          *runtimeapp.RuntimeState
	ActiveMode     func() agent.AgentMode
	CreateAgent    func(agent.AgentMode) *agent.CovoAgent
	ReplaceAgent   func(agent.AgentMode, bool) *agent.CovoAgent
	SwitchToMode   func(agent.AgentMode)
	SwitchModel    func(string)
	SwitchProvider func(string) error
	WorkingDir     string
	ProviderType   string
	Model          string
}

// UIDependencies groups UI callbacks and panel integrations.
type UIDependencies struct {
	App                *chat.ChatApp
	StatusLineManager  StatusLineManager
	OpenModelPicker    func()
	RestoreChatHistory func(app *chat.ChatApp, msgs []agentcore.Message)
	ShowStatsDialog    func(app *chat.ChatApp, ca *agent.CovoAgent)
	ShowStatusInfo     func(app *chat.ChatApp, ca *agent.CovoAgent, ag *agentcore.Agent)
	ShowRewindDialog   func(app *chat.ChatApp, snapshotFn func() agentcore.StateSnapshot, restoreFn func(agentcore.StateSnapshot))
	ApplyNamedTheme    func(name string)
	OpenChangedFiles   func()
	OpenMCPMarketplace func()
	OpenSettings       func()
	OpenPromptQueue    func()
}

// IODependencies groups import/export and clipboard integrations.
type IODependencies struct {
	ExportSessionHTML      func(app *chat.ChatApp, ag *agentcore.Agent, path string)
	ExportTrajectoryJSONL  func(app *chat.ChatApp, ca *agent.CovoAgent)
	ShareSessionAsGist     func(app *chat.ChatApp, ag *agentcore.Agent)
	CopyToClipboard        func(text string)
	ImportSessionFromJSONL ImportSessionFunc
}

// ServiceDependencies groups external service hooks and utility helpers.
type ServiceDependencies struct {
	BackgroundManager   BackgroundManager
	ExecuteShellCommand func(ctx context.Context, cmd string, cwd string, app *chat.ChatApp, busy *atomic.Bool)
	HandleTmuxSlash     func(sub string, rest []string) (string, error)
	HomeDir             string
	WriteSkinTheme      func(homeDir string, name string) error
	NotifyGatewayFooter func(homeDir string, enabled bool)
	ReadTemplate        func(homeDir string, name string) (string, error)
	ExpandTemplateArgs  func(content string, args []string) string
	TemplateList        func(homeDir string) string
	ResetChangedFiles   func()
	Personalities       map[string]runtimeapp.PersonalityDef
}

// SlashContext converges all the parameters and main-package function dependencies
// that the slash command handlers need.
type SlashContext struct {
	Input    string
	Runtime  RuntimeDependencies
	UI       UIDependencies
	IO       IODependencies
	Services ServiceDependencies
}

// ContextBuilder stores slash-command dependencies that are stable for the
// application lifetime and injects request-scoped values for each command.
type ContextBuilder struct {
	Runtime  RuntimeDependencies
	UI       UIDependencies
	IO       IODependencies
	Services ServiceDependencies
}

func (builder *ContextBuilder) Build(input string, ctx context.Context, providerType, model string) *SlashContext {
	runtime := builder.Runtime
	runtime.Context = ctx
	runtime.ProviderType = providerType
	runtime.Model = model
	return &SlashContext{
		Input:    input,
		Runtime:  runtime,
		UI:       builder.UI,
		IO:       builder.IO,
		Services: builder.Services,
	}
}

// truncate safely clips a string by rune count for slash command previews.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
