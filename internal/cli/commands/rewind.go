package commands

import (
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/component"

	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/tui"
)

type userTurn struct {
	Index   int
	Content string
	Preview string
}

func extractUserTurns(messages []agentcore.Message) []userTurn {
	var turns []userTurn
	for i, msg := range messages {
		if msg.Role == agentcore.RoleUser {
			preview := msg.Content
			if len(preview) > 60 {
				preview = preview[:60] + "..."
			}
			preview = strings.ReplaceAll(preview, "\n", " ")
			turns = append(turns, userTurn{
				Index:   i,
				Content: msg.Content,
				Preview: preview,
			})
		}
	}
	return turns
}

func showRewindDialog(app *chat.ChatApp, snapshot func() agentcore.StateSnapshot,
	restore func(snap agentcore.StateSnapshot)) {

	turns := extractUserTurns(snapshot().Messages)
	if len(turns) == 0 {
		app.PrintSystem(i18n.T("system.no_rewind_turns"))
		return
	}

	items := make([]component.SelectItem, 0, len(turns))
	for i, t := range turns {
		label := fmt.Sprintf("Turn %d: %s", i+1, t.Preview)
		items = append(items, component.SelectItem{
			Value:       fmt.Sprintf("%d", i),
			Label:       label,
			Description: "",
		})
	}

	selector := component.NewSelectList(items)
	selector.SetMaxVisible(15)
	selector.OnSelect(func(si component.SelectItem) {
		var idx int
		fmt.Sscanf(si.Value, "%d", &idx)
		if idx < 0 || idx >= len(turns) {
			return
		}

		snap := snapshot()
		targetIdx := turns[idx].Index
		truncated := snap.Messages[:targetIdx]

		restore(agentcore.StateSnapshot{
			Messages: truncated,
		})

		shared.RestoreChatHistory(app, truncated)

		app.Host().RequestRender()
	})

	tui.NewUIBus(app).ShowPanel(selector, 60, 70)
}
