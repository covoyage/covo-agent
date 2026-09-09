package slashcmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/safego"
)

// handleExport handles /export, /export-session
func handleExport(sctx *SlashContext, parts []string) bool {
	ag := sctx.Runtime.Agents.Core()
	if ag == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	exportPath := ""
	if len(parts) > 1 {
		exportPath = parts[1]
	}
	sctx.IO.ExportSessionHTML(sctx.UI.App, ag, exportPath)
	return true
}

// handleExportTrajectory handles /export-trajectory
func handleExportTrajectory(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	sctx.IO.ExportTrajectoryJSONL(sctx.UI.App, covoAgent)
	return true
}

// handleCopy handles /copy
func handleCopy(sctx *SlashContext, parts []string) bool {
	ag := sctx.Runtime.Agents.Core()
	if ag == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	n := 0
	if len(parts) > 1 {
		fmt.Sscanf(parts[1], "%d", &n)
	}
	msgs := ag.State().Messages()
	var text string
	found := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == agentcore.RoleAssistant {
			if found == n {
				text = msgs[i].Content
				break
			}
			found++
		}
	}
	if text == "" {
		sctx.UI.App.PrintSystem("(no assistant message to copy)")
		return true
	}
	sctx.IO.CopyToClipboard(text)
	preview := truncate(text, 60)
	sctx.UI.App.PrintSystem(fmt.Sprintf("copied to clipboard: %s", preview))
	return true
}

func cloneMessages(msgs []agentcore.Message) []agentcore.Message {
	out := make([]agentcore.Message, len(msgs))
	copy(out, msgs)
	return out
}

func appendSide(app *chat.ChatApp, role chat.ChatRole, text string) {
	if app == nil {
		return
	}
	app.History().Append(chat.ChatMessage{
		Role: role,
		Meta: "side",
		Text: text,
	})
}

func parseBtwRest(rest string) (persist bool, question string) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return false, ""
	}
	lower := strings.ToLower(rest)
	if lower == "persist" {
		return true, ""
	}
	if strings.HasPrefix(lower, "persist ") || strings.HasPrefix(lower, "persist\t") {
		return true, strings.TrimSpace(rest[len("persist"):])
	}
	return false, rest
}

func persistBtwToMain(core *agentcore.Agent, appendFn func(agentcore.Message), question, answer string) {
	if question == "" || answer == "" {
		return
	}
	user := agentcore.Message{Role: agentcore.RoleUser, Content: question, Name: "side"}
	asst := agentcore.Message{Role: agentcore.RoleAssistant, Content: answer, Name: "side"}
	if core != nil {
		core.State().AddMessage(user)
		core.State().AddMessage(asst)
	}
	if appendFn == nil {
		return
	}
	appendFn(user)
	appendFn(asst)
}

// handleBtw handles /btw, /side. The side agent clones the main transcript
// and can run while the main agent is busy. `/btw persist <q>` writes the
// exchange back to the main transcript on success.
func handleBtw(sctx *SlashContext, parts []string) bool {
	cmd := strings.TrimPrefix(parts[0], "/")
	rest := strings.TrimSpace(strings.TrimPrefix(sctx.Input, "/"+cmd))
	if strings.EqualFold(rest, "clear") || strings.EqualFold(rest, "cancel") {
		if sctx.Runtime.CancelSide != nil {
			sctx.Runtime.CancelSide()
		}
		if sctx.UI.App != nil {
			sctx.UI.App.PrintSystem(i18n.T("system.btw_cancelled"))
		}
		return true
	}
	persist, question := parseBtwRest(rest)
	if question == "" {
		if sctx.UI.App != nil {
			sctx.UI.App.PrintSystem(i18n.T("system.btw_usage"))
		}
		return true
	}
	if sctx.Runtime.CreateSideAgent == nil && sctx.Runtime.CreateAgent == nil {
		if sctx.UI.App != nil {
			sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		}
		return true
	}
	if sctx.Runtime.SideBusy != nil && !sctx.Runtime.SideBusy.CompareAndSwap(false, true) {
		if sctx.UI.App != nil {
			sctx.UI.App.PrintSystem(i18n.T("system.btw_busy"))
		}
		return true
	}

	parent := context.Background()
	if sctx.Runtime.Context != nil {
		parent = sctx.Runtime.Context
	}
	ctx, cancel := context.WithCancel(parent)
	if sctx.Runtime.SetSideCancel != nil {
		sctx.Runtime.SetSideCancel(cancel)
	}

	appendSide(sctx.UI.App, chat.RoleUser, i18n.T("system.btw_question", "text", question))
	pendingID := ""
	if sctx.UI.App != nil {
		pendingID = sctx.UI.App.History().Append(chat.ChatMessage{
			Role:    chat.RoleAssistant,
			Meta:    "side",
			Text:    i18n.T("system.btw_running"),
			Pending: true,
		})
	}

	var history []agentcore.Message
	var mainCore *agentcore.Agent
	var persistAppend func(agentcore.Message)
	if sctx.Runtime.Agents != nil {
		if ag := sctx.Runtime.Agents.Core(); ag != nil {
			mainCore = ag
			history = cloneMessages(ag.State().Messages())
		}
		if persist {
			if ca := sctx.Runtime.Agents.Current(); ca != nil {
				sm := ca.SessionManager()
				sessionID := sm.CurrentID()
				persistAppend = func(msg agentcore.Message) {
					if sessionID == "" {
						return
					}
					_ = sm.DB().AppendMessage(context.Background(), sessionID, msg)
				}
			}
		}
	}
	var mode agent.AgentMode
	if sctx.Runtime.ActiveMode != nil {
		mode = sctx.Runtime.ActiveMode()
	}
	create := sctx.Runtime.CreateSideAgent
	if create == nil {
		create = sctx.Runtime.CreateAgent
	}
	app := sctx.UI.App
	sideBusy := sctx.Runtime.SideBusy
	setSideCancel := sctx.Runtime.SetSideCancel

	safego.SafeGo(func() {
		defer cancel()
		defer func() {
			if setSideCancel != nil {
				setSideCancel(nil)
			}
			if sideBusy != nil {
				sideBusy.Store(false)
			}
		}()
		ca := create(mode)
		if ca == nil {
			if app != nil && pendingID != "" {
				app.History().PatchMessage(pendingID, func(m *chat.ChatMessage) {
					m.Pending = false
					m.Role = chat.RoleError
					m.Text = i18n.T("system.no_active_agent")
				})
			}
			return
		}
		defer ca.Close()
		core := ca.Core()
		if len(history) > 0 && core != nil {
			core.State().Restore(agentcore.StateSnapshot{Messages: history})
		}
		result, err := ca.RunDirect(ctx, question)
		text := strings.TrimSpace(result)
		if err != nil {
			if ctx.Err() != nil {
				text = i18n.T("system.btw_cancelled")
			} else {
				text = err.Error()
			}
		}
		if text == "" {
			text = i18n.T("system.btw_empty")
		}
		ok := err == nil && ctx.Err() == nil
		if persist && ok {
			persistBtwToMain(mainCore, persistAppend, question, text)
			if app != nil {
				app.PrintSystem(i18n.T("system.btw_persisted"))
			}
		}
		if app != nil && pendingID != "" {
			app.History().PatchMessage(pendingID, func(m *chat.ChatMessage) {
				m.Pending = false
				if err != nil && ctx.Err() == nil {
					m.Role = chat.RoleError
				} else {
					m.Role = chat.RoleAssistant
				}
				m.Meta = "side"
				m.Text = text
			})
			return
		}
		if app != nil {
			appendSide(app, chat.RoleAssistant, text)
		}
	}, nil)
	return true
}

// handleShell handles /shell, /sh, /!
// Note: handleShell is defined in dispatch.go for simplicity (it's in the dispatch routing)
// but we keep a reference here for the /shell suggestion. The actual handler is in dispatch.go.
