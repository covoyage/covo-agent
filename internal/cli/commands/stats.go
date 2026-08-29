package commands

import (
	"fmt"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/component"
	"github.com/covoyage/covonaut/tui/theme"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/tui"
)

func showStatsDialog(app *chat.ChatApp, covoAgent *agent.CovoAgent) {
	pal := theme.CurrentPalette()

	usage := covoAgent.CostTracker().CurrentUsage()
	cost := covoAgent.CostTracker().CurrentCost()
	provider := covoAgent.ProviderName()
	modelName := covoAgent.Model()

	var sb strings.Builder
	sb.WriteString(pal.Success.Render(i18n.T("system.stats_header")))
	sb.WriteString("\n\n")

	sb.WriteString(fmt.Sprintf("%s  %s/%s\n",
		pal.Dim.Render(i18n.T("system.stats_model")),
		provider, modelName))

	sb.WriteString(fmt.Sprintf("%s  %s\n",
		pal.Dim.Render("    Mode:"),
		string(covoAgent.Mode())))

	sb.WriteString("\n")

	sb.WriteString(pal.Dim.Render("── Token Usage ──\n"))

	sb.WriteString(fmt.Sprintf("%s  %s tokens\n",
		pal.Dim.Render("     Input:"),
		commaize(usage.InputTokens)))

	sb.WriteString(fmt.Sprintf("%s  %s tokens\n",
		pal.Dim.Render("    Output:"),
		commaize(usage.OutputTokens)))

	if usage.CacheReadTokens > 0 {
		sb.WriteString(fmt.Sprintf("%s  %s tokens\n",
			pal.Dim.Render(" Cache read:"),
			commaize(usage.CacheReadTokens)))
	}

	sb.WriteString(fmt.Sprintf("%s  %s tokens\n",
		pal.Accent.Render("     Total:"),
		commaize(usage.TotalTokens())))

	sb.WriteString("\n")

	sb.WriteString(pal.Dim.Render("── API Requests ──\n"))
	sb.WriteString(fmt.Sprintf("%s  %s\n",
		pal.Dim.Render("  Requests:"),
		commaize(int(usage.RequestCount))))

	if cost > 0 {
		sb.WriteString(fmt.Sprintf("%s  $%.4f\n",
			pal.Dim.Render("      Cost:"),
			cost))
	}

	sb.WriteString("\n")

	entries := covoAgent.Trajectory().Snapshot()
	userTurns := 0
	assistantMsgs := 0
	toolCallCount := 0

	for _, entry := range entries {
		switch entry.Role {
		case "user":
			userTurns++
		case "assistant":
			assistantMsgs++
			toolCallCount += len(entry.ToolCalls)
		}
	}

	sb.WriteString(pal.Dim.Render("── Session Activity ──\n"))
	sb.WriteString(fmt.Sprintf("%s  %d\n",
		pal.Dim.Render(" User turns:"),
		userTurns))

	sb.WriteString(fmt.Sprintf("%s  %d\n",
		pal.Dim.Render("    Replies:"),
		assistantMsgs))

	sb.WriteString(fmt.Sprintf("%s  %d\n",
		pal.Dim.Render(" Tool calls:"),
		toolCallCount))

	sb.WriteString("\n")

	content := sb.String()

	items := []component.SelectItem{{
		Value:       "close",
		Label:       i18n.T("system.stats_close"),
		Description: i18n.T("system.stats_close_hint"),
	}}

	selector := component.NewSelectList(items)
	selector.SetMaxVisible(1)

	infoDisplay := &statsDisplay{content: content, selector: selector}
	tui.NewUIBus(app).ShowPanel(infoDisplay, 60, 70)
}

type statsDisplay struct {
	content  string
	selector *component.SelectList
}

func (d *statsDisplay) Render(width int64) []string {
	lines := strings.Split(d.content, "\n")
	out := make([]string, 0, len(lines)+3)
	for _, line := range lines {
		out = append(out, line)
	}
	out = append(out, "")
	for _, line := range d.selector.Render(width) {
		out = append(out, line)
	}
	return out
}

func (d *statsDisplay) Update(_ ...interface{}) {
	d.selector.SetFocused(true)
}

func (d *statsDisplay) Focus()      {}
func (d *statsDisplay) Blur()       {}
func (d *statsDisplay) Invalidate() {}

func commaize(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%d,%03d", n/1000, n%1000)
}

// showStatusInfo prints a compact one-line session overview.
func showStatusInfo(app *chat.ChatApp, covoAgent *agent.CovoAgent, coreAgent *agentcore.Agent) {
	if covoAgent == nil {
		app.PrintSystem("(no active agent)")
		return
	}

	usage := covoAgent.CostTracker().CurrentUsage()
	provider := covoAgent.ProviderName()
	modelName := covoAgent.Model()

	var parts []string
	parts = append(parts, fmt.Sprintf("provider=%s", provider))
	parts = append(parts, fmt.Sprintf("model=%s", modelName))
	parts = append(parts, fmt.Sprintf("mode=%s", covoAgent.Mode()))

	// Token usage
	total := usage.TotalTokens()
	if total > 0 {
		parts = append(parts, fmt.Sprintf("tokens=%s (in %s / out %s)",
			commaize(total), commaize(usage.InputTokens), commaize(usage.OutputTokens)))
	}

	// Session ID
	sessionID := covoAgent.SessionManager().CurrentID()
	if sessionID != "" {
		parts = append(parts, fmt.Sprintf("session=%s", sessionID[:8]))
	}

	app.PrintSystem(i18n.T("system.stats_header"))
	app.PrintSystem(strings.Join(parts, "  │  "))

	// YOLO status
	if shared.RuntimeState.SessionYolo() {
		app.PrintSystem(i18n.T("system.stats_yolo_warn"))
	}
	if shared.RuntimeState.FastMode() {
		app.PrintSystem(i18n.T("system.stats_fast_warn"))
	}
}
