package slashcmd

import (
	"github.com/covoyage/covonaut/tui/core"

	"github.com/covoyage/covo-agent/internal/i18n"
)

// BuildSlashSuggestions returns the list of slash command suggestions for the UI.
func BuildSlashSuggestions() []core.Suggestion {
	return []core.Suggestion{
		{InsertText: "help", Label: "/help", Description: i18n.T("commands.help")},
		{InsertText: "clear", Label: "/clear", Description: i18n.T("commands.clear")},
		{InsertText: "new", Label: "/new", Description: i18n.T("commands.reset")},
		{InsertText: "mode general", Label: "/mode general", Description: i18n.T("commands.mode")},
		{InsertText: "mode code", Label: "/mode code", Description: i18n.T("commands.code_mode")},
		{InsertText: "provider openai", Label: "/provider <name>", Description: i18n.T("commands.provider")},
		{InsertText: "model", Label: "/model", Description: i18n.T("commands.model")},
		{InsertText: "yolo", Label: "/yolo", Description: i18n.T("commands.yolo")},
		{InsertText: "approve ", Label: "/approve <category>", Description: "Toggle per-category auto-approval"},
		{InsertText: "undo", Label: "/undo [N]", Description: i18n.T("commands.undo")},
		{InsertText: "fast", Label: "/fast", Description: i18n.T("commands.fast")},
		{InsertText: "status", Label: "/status", Description: i18n.T("commands.status")},
		{InsertText: "footer", Label: "/footer", Description: i18n.T("commands.footer")},
		{InsertText: "memory agent", Label: "/memory agent", Description: i18n.T("commands.memory")},
		{InsertText: "memory user", Label: "/memory user", Description: i18n.T("commands.profile")},
		{InsertText: "skill", Label: "/skill", Description: i18n.T("commands.skill")},
		{InsertText: "goal ", Label: "/goal", Description: i18n.T("commands.goal")},
		{InsertText: "session", Label: "/session", Description: i18n.T("commands.session")},
		{InsertText: "changes", Label: "/changes", Description: "Show changed files tree"},
		{InsertText: "mcp", Label: "/mcp", Description: "Browse MCP server marketplace"},
		{InsertText: "prune ", Label: "/prune <days>", Description: i18n.T("commands.prune")},
		{InsertText: "resume ", Label: "/resume <id>", Description: i18n.T("commands.resume")},
		{InsertText: "save", Label: "/save", Description: i18n.T("commands.save")},
		{InsertText: "title ", Label: "/title <name>", Description: i18n.T("commands.rename")},
		{InsertText: "label ", Label: "/label <tag>", Description: i18n.T("commands.label")},
		{InsertText: "branch", Label: "/branch", Description: i18n.T("commands.fork")},
		{InsertText: "history", Label: "/history", Description: i18n.T("commands.log")},
		{InsertText: "checkpoint", Label: "/checkpoint", Description: i18n.T("commands.rollback")},
		{InsertText: "snapshot", Label: "/snapshot", Description: "List/revert file-level snapshots"},
		{InsertText: "unrevert", Label: "/unrevert", Description: "Restore files after /snapshot revert"},
		{InsertText: "reasoning ", Label: "/reasoning <level>", Description: i18n.T("commands.reasoning")},
		{InsertText: "personality ", Label: "/personality <name>", Description: i18n.T("commands.personality")},
		{InsertText: "busy ", Label: "/busy <mode>", Description: i18n.T("commands.busy")},
		{InsertText: "copy", Label: "/copy [N]", Description: i18n.T("commands.copy")},
		{InsertText: "btw ", Label: "/btw <question>", Description: i18n.T("commands.ask")},
		{InsertText: "export", Label: "/export [path]", Description: i18n.T("commands.export")},
		{InsertText: "export-trajectory", Label: "/export-trajectory", Description: i18n.T("commands.trajectory")},
		{InsertText: "profile ", Label: "/profile <name>", Description: i18n.T("commands.tool_profile")},
		{InsertText: "curator", Label: "/curator", Description: i18n.T("commands.curator")},
		{InsertText: "distill", Label: "/distill", Description: i18n.T("commands.distill")},
		{InsertText: "dream", Label: "/dream", Description: i18n.T("commands.dream")},
		{InsertText: "consolidate", Label: "/consolidate", Description: i18n.T("commands.consolidate")},
		{InsertText: "compact", Label: "/compact [topic]", Description: i18n.T("commands.compact")},
		{InsertText: "retry", Label: "/retry", Description: i18n.T("commands.retry")},
		{InsertText: "background ", Label: "/background <task>", Description: i18n.T("commands.bg")},
		{InsertText: "queue", Label: "/queue", Description: i18n.T("commands.bg_list")},
		{InsertText: "steer ", Label: "/steer <id> <msg>", Description: i18n.T("commands.bg_steer")},
		{InsertText: "cancel ", Label: "/cancel <id>", Description: i18n.T("commands.bg_cancel")},
		{InsertText: "statusline", Label: "/statusline", Description: i18n.T("commands.statusline_cmd")},
		{InsertText: "rewind", Label: "/rewind", Description: i18n.T("commands.rewind")},
		{InsertText: "dashboard", Label: "/dashboard", Description: i18n.T("commands.dashboard")},
		{InsertText: "loop ", Label: "/loop <interval> <prompt>", Description: i18n.T("commands.loop")},
		{InsertText: "import-foreign", Label: "/import-foreign", Description: "Discover/import Claude Code or Codex sessions"},
		{InsertText: "mermaid ", Label: "/mermaid <syntax>", Description: "Render Mermaid flowchart as ASCII art in terminal"},
		{InsertText: "marketplace ", Label: "/marketplace", Description: "Browse, install, and manage plugins (skills, MCP, etc.)"},
		{InsertText: "plan", Label: "/plan", Description: "Enter Plan mode (read-only tools)"},
		{InsertText: "act", Label: "/act", Description: "Enter Act mode (all tools)"},
		{InsertText: "stats", Label: "/stats", Description: i18n.T("commands.stats")},
		{InsertText: "language ", Label: "/language <code>", Description: i18n.T("commands.language")},
		{InsertText: "theme ", Label: "/theme <name>", Description: i18n.T("commands.theme")},
		{InsertText: "settings", Label: "/settings", Description: "Open settings panel"},
		{InsertText: "prompts", Label: "/prompts", Description: "Show queued prompts"},
		{InsertText: "quit", Label: "/quit", Description: i18n.T("commands.quit")},
		{InsertText: "shell ", Label: "/shell <cmd>", Description: "Execute a shell command"},
		{InsertText: "share", Label: "/share", Description: "Share session as GitHub Gist"},
		{InsertText: "template ", Label: "/template <name>", Description: i18n.T("commands.template")},
		{InsertText: "tmux ", Label: "/tmux <op> [args...]", Description: "Control tmux sessions, windows, and panes"},
		{InsertText: "import ", Label: "/import <path>", Description: i18n.T("commands.import")},
		{InsertText: "skill:", Label: "/skill: <name>", Description: i18n.T("commands.skill_invoke")},
		{InsertText: "commit", Label: "/commit [dry|all]", Description: "Auto-generate commit message and commit"},
		{InsertText: "commitments", Label: "/commitments", Description: "View pending commitments"},
	}
}

// BuildAtSuggestions returns the list of @-reference suggestions for the UI.
func BuildAtSuggestions() []core.Suggestion {
	return []core.Suggestion{
		{InsertText: "diff", Label: "@diff", Description: i18n.T("refs.git_diff")},
		{InsertText: "staged", Label: "@staged", Description: i18n.T("refs.git_diff_staged")},
		{InsertText: "file:", Label: "@file:", Description: i18n.T("refs.file")},
		{InsertText: "folder:", Label: "@folder:", Description: i18n.T("refs.folder")},
		{InsertText: "git:", Label: "@git:", Description: i18n.T("refs.git_n")},
	}
}
