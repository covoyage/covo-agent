package slashcmd

import (
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/tui/chat"

	"github.com/covoyage/covo-agent/internal/i18n"
)

// handleUsage handles /usage and /cost.
func handleUsage(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	tracker := covoAgent.CostTracker()
	if tracker == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.usage_unavailable"))
		return true
	}

	usage := tracker.CurrentUsage()
	var b strings.Builder
	b.WriteString(tracker.Summary())
	b.WriteByte('\n')

	promptTokens := usage.PromptTokens()
	if promptTokens > 0 && usage.CacheReadTokens > 0 {
		hit := float64(usage.CacheReadTokens) / float64(promptTokens) * 100
		b.WriteString(fmt.Sprintf("Cache hit: %.1f%% (%d / %d prompt tokens)\n",
			hit, usage.CacheReadTokens, promptTokens))
	} else if usage.CacheReadTokens == 0 && usage.RequestCount > 0 {
		b.WriteString("Cache hit: n/a (provider did not report cache tokens)\n")
	}

	lastPrompt := tracker.LastPromptTokens()
	ctxLen := int64(0)
	if core := sctx.Runtime.Agents.Core(); core != nil {
		if engine := core.ContextEngine(); engine != nil {
			ctxLen = engine.ContextLength()
		}
	}
	if lastPrompt > 0 {
		if ctxLen > 0 {
			pct := float64(lastPrompt) / float64(ctxLen) * 100
			b.WriteString(fmt.Sprintf("Last prompt: %d / %d tokens (%.1f%% of context)\n",
				lastPrompt, ctxLen, pct))
		} else {
			b.WriteString(fmt.Sprintf("Last prompt: %d tokens\n", lastPrompt))
		}
	}

	sctx.UI.App.PrintSystem(strings.TrimSpace(b.String()))
	return true
}

// handleVim toggles vim-style modal editing in the TUI editor.
func handleVim(sctx *SlashContext, parts []string) bool {
	ed := sctx.UI.App.Editor()
	if ed == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.editor_unavailable"))
		return true
	}
	on := !ed.VimMode()
	if len(parts) >= 2 {
		switch strings.ToLower(parts[1]) {
		case "on", "true", "1":
			on = true
		case "off", "false", "0":
			on = false
		default:
			sctx.UI.App.PrintSystem("Usage: /vim [on|off]")
			return true
		}
	}
	ed.SetVimMode(on)
	if on {
		sctx.UI.App.PrintSystem(i18n.T("system.vim_on"))
	} else {
		sctx.UI.App.PrintSystem(i18n.T("system.vim_off"))
	}
	return true
}

// handleRecap prints a local recap of the current session (no extra LLM call).
func handleRecap(sctx *SlashContext, parts []string) bool {
	if sctx.UI.App == nil || sctx.UI.App.History() == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_history"))
		return true
	}
	msgs := sctx.UI.App.History().Messages()
	sctx.UI.App.PrintSystem(buildSessionRecap(msgs))
	return true
}

func buildSessionRecap(msgs []chat.ChatMessage) string {
	var users, assistants []string
	for _, m := range msgs {
		text := strings.TrimSpace(m.Text)
		if text == "" {
			continue
		}
		switch m.Role {
		case chat.RoleUser:
			users = append(users, text)
		case chat.RoleAssistant:
			assistants = append(assistants, text)
		}
	}
	if len(users) == 0 && len(assistants) == 0 {
		return i18n.T("system.recap_empty")
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("── Recap (%d user / %d assistant) ──\n", len(users), len(assistants)))
	if len(users) > 0 {
		b.WriteString("Last user: ")
		b.WriteString(truncate(users[len(users)-1], 240))
		b.WriteByte('\n')
	}
	if len(assistants) > 0 {
		b.WriteString("Last assistant: ")
		b.WriteString(truncate(assistants[len(assistants)-1], 240))
		b.WriteByte('\n')
	}
	if len(users) > 1 {
		b.WriteString("Earlier: ")
		b.WriteString(truncate(users[0], 120))
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

// handleFocus focuses the editor (or history when requested).
func handleFocus(sctx *SlashContext, parts []string) bool {
	if sctx.UI.App == nil || sctx.UI.App.Host() == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.editor_unavailable"))
		return true
	}
	target := "editor"
	if len(parts) >= 2 {
		target = strings.ToLower(parts[1])
	}
	switch target {
	case "history", "chat":
		if hist := sctx.UI.App.History(); hist != nil {
			sctx.UI.App.Host().Focus(hist)
			sctx.UI.App.PrintSystem(i18n.T("system.focus_history"))
			return true
		}
	case "editor", "input":
		if ed := sctx.UI.App.Editor(); ed != nil {
			sctx.UI.App.Host().Focus(ed)
			sctx.UI.App.PrintSystem(i18n.T("system.focus_editor"))
			return true
		}
	default:
		sctx.UI.App.PrintSystem("Usage: /focus [editor|history]")
		return true
	}
	sctx.UI.App.PrintSystem(i18n.T("system.editor_unavailable"))
	return true
}

// handleEffort is an alias of /reasoning.
func handleEffort(sctx *SlashContext, parts []string) bool {
	return handleReasoning(sctx, parts)
}
