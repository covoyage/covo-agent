package tui

import (
	"sync"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"

	"github.com/covoyage/covo-agent/internal/i18n"
)

var toolStatusMessages = map[string]string{
	"grep":           "searching files...",
	"glob":           "finding files...",
	"read":           "reading file...",
	"read_file":      "reading file...",
	"write_file":     "writing file...",
	"edit_block":     "editing file...",
	"write":          "writing...",
	"edit":           "editing...",
	"bash":           "running command...",
	"process":        "running command...",
	"web_search":     "searching web...",
	"web_fetch":      "fetching web content...",
	"browser":        "browsing web...",
	"git_diff":       "checking git diff...",
	"git_log":        "checking git history...",
	"git_status":     "checking git status...",
	"git_commit":     "committing...",
	"lint":           "running linter...",
	"todo_write":     "updating task list...",
	"plan_create":    "creating plan...",
	"memory_write":   "updating memory...",
	"skill_create":   "creating skill...",
	"skill_update":   "updating skill...",
	"execute_code":   "executing code...",
	"search":         "searching...",
	"ls":             "listing directory...",
	"delete_file":    "deleting file...",
	"sessions_spawn": "spawning sub-agent...",
	"save":           "saving session...",
	"ask_user":       "asking for input...",
	"session_fts":    "searching sessions...",
	"monitor":        "monitoring process...",
	"send_message":   "sending notification...",
	"exit_plan_mode": "presenting plan for approval...",
}

type thinkingIndicator struct {
	mu            sync.Mutex
	activeCount   int
	currentStatus string
}

// BindThinkingIndicator connects agent lifecycle events to the chat status line.
func BindThinkingIndicator(app *chat.ChatApp, agent *agentcore.Agent) {
	indicator := &thinkingIndicator{}

	agent.On(agentcore.EventAgentStart, func(agentcore.Event) {
		indicator.set(app, i18n.T("thinking.default"))
	})

	agent.On(agentcore.EventAgentEnd, func(agentcore.Event) {
		indicator.clear(app)
	})

	agent.On(agentcore.EventTurnStart, func(event agentcore.Event) {
		turn := event.(*agentcore.TurnStartEvent)
		if turn.Turn > 1 {
			indicator.set(app, i18n.T("thinking.continuing"))
		}
	})

	agent.On(agentcore.EventToolCallStart, func(event agentcore.Event) {
		toolCall := event.(*agentcore.ToolCallStartEvent)
		indicator.mu.Lock()
		indicator.activeCount++
		indicator.mu.Unlock()
		indicator.set(app, toolStatus(toolCall.ToolCall.Name, toolCall.ToolCall.Arguments))
	})

	agent.On(agentcore.EventToolCallEnd, func(agentcore.Event) {
		indicator.mu.Lock()
		if indicator.activeCount > 0 {
			indicator.activeCount--
		}
		indicator.mu.Unlock()
		indicator.set(app, i18n.T("thinking.default"))
	})

	agent.On(agentcore.EventCompactionStart, func(agentcore.Event) {
		indicator.set(app, i18n.T("thinking.compacting"))
	})

	agent.On(agentcore.EventHandoffStart, func(event agentcore.Event) {
		handoff := event.(*agentcore.HandoffStartEvent)
		target := handoff.TargetAgent
		if target == "" {
			target = "sub-agent"
		}
		indicator.set(app, i18n.T("thinking.handing_off", "target", target))
	})

	agent.On(agentcore.EventHandoffEnd, func(agentcore.Event) {
		indicator.set(app, i18n.T("thinking.handoff_complete"))
	})
}

func (indicator *thinkingIndicator) set(app *chat.ChatApp, message string) {
	indicator.mu.Lock()
	indicator.currentStatus = message
	indicator.mu.Unlock()
	app.PrintStatus(message)
}

func (indicator *thinkingIndicator) clear(app *chat.ChatApp) {
	indicator.mu.Lock()
	indicator.currentStatus = ""
	indicator.mu.Unlock()
	app.PrintStatus("")
}

func toolStatus(name, arguments string) string {
	message, ok := toolStatusMessages[name]
	if !ok {
		message = i18n.T("thinking.running_tool", "name", name)
	}
	if preview := chat.ToolArgPreview(arguments, 40); preview != "" {
		return message + " (" + preview + ")"
	}
	return message
}
