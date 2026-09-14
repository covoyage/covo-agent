package tui

import (
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"
	core "github.com/covoyage/covonaut/tui/core"

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

// thinkingTailLimit 思考尾部缓冲上限（字节）。ticker 只展示最新结尾，
// 缓冲超出即从头丢弃，防长会话内存无界增长。
const thinkingTailLimit = 2048

type thinkingIndicator struct {
	mu            sync.Mutex
	activeCount   int
	currentStatus string
	tail          string // 最近思考文本缓冲（loader 行 ticker 数据源）
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

	// 思考流：thinking delta 追加进尾部缓冲，loader 行同行滚动展示
	// 最新内容。正文/工具 delta 不清缓冲——正文阶段与续跑 turn（此时
	// 可能尚未产出新思考，甚至不再产出思考）保留上一段思考作预览，
	// 否则「继续中...」会出现空窗口。清空只发生在 MessageReset（重试
	// 作废）与 AgentEnd（整场结束）。
	agent.On(agentcore.EventMessageDelta, func(event agentcore.Event) {
		delta, ok := event.(*agentcore.MessageDeltaEvent)
		if !ok || delta.Delta == "" || delta.Kind != agentcore.BlockKindThinking {
			return
		}
		indicator.appendTail(app, delta.Delta)
	})

	// 流式重试/失败切换：该 attempt 的所有 delta 作废，缓冲同步清空。
	agent.On(agentcore.EventMessageReset, func(agentcore.Event) {
		indicator.clearTail(app)
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
	indicator.tail = ""
	indicator.mu.Unlock()
	app.PrintStatus("")
	app.SetThinkingTail("")
}

// appendTail 把一段思考 delta 追加进尾部缓冲，随后推给 loader 行滚动。
func (indicator *thinkingIndicator) appendTail(app *chat.ChatApp, delta string) {
	indicator.mu.Lock()
	indicator.tail = mergeThinkingTail(indicator.tail, delta)
	tail := indicator.tail
	indicator.mu.Unlock()
	app.SetThinkingTail(tail)
}

// mergeThinkingTail 把一段思考 delta 并入缓冲：剥 ANSI、空白折叠成
// 单空格，超限从头丢弃（对齐 rune 边界）。
func mergeThinkingTail(buf, delta string) string {
	delta = core.StripAnsi(delta)
	var b strings.Builder
	prevSpace := buf == "" || strings.HasSuffix(buf, " ")
	for _, r := range delta {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	buf += b.String()
	// 纯空白 delta 会在尾部留下一个空格，修剪掉——下次追加时空白
	// 折叠逻辑仍会正确补分隔空格。
	buf = strings.TrimRight(buf, " ")
	if len(buf) > thinkingTailLimit {
		cut := buf[len(buf)-thinkingTailLimit:]
		for len(cut) > 0 && !utf8.RuneStart(cut[0]) {
			cut = cut[1:]
		}
		buf = cut
	}
	return buf
}

func (indicator *thinkingIndicator) clearTail(app *chat.ChatApp) {
	indicator.mu.Lock()
	if indicator.tail == "" {
		indicator.mu.Unlock()
		return
	}
	indicator.tail = ""
	indicator.mu.Unlock()
	app.SetThinkingTail("")
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
