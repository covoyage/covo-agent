package panels

import (
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"

	"github.com/covoyage/covo-agent/internal/i18n"
)

// ApprovalChoice is the presentation-layer result of an approval prompt.
type ApprovalChoice string

const (
	ChoiceOnce    ApprovalChoice = "once"
	ChoiceSession ApprovalChoice = "session"
	ChoiceAlways  ApprovalChoice = "always"
	ChoiceDeny    ApprovalChoice = "deny"
)

// ApprovalPicker is an inline selector for tool approval.
type ApprovalPicker struct {
	mu       sync.RWMutex
	prompt   string
	selected int
	onChoose func(ApprovalChoice)
	onCancel func()
}

type approvalOption struct {
	label string
	desc  string
}

func approvalOptions() []approvalOption {
	return []approvalOption{
		{label: i18n.T("approval.choose_short"), desc: i18n.T("approval.choose_short_desc")},
		{label: i18n.T("approval.choose_session"), desc: i18n.T("approval.choose_session_desc")},
		{label: i18n.T("approval.choose_always"), desc: i18n.T("approval.choose_always_desc")},
		{label: i18n.T("approval.deny"), desc: i18n.T("approval.deny_desc")},
	}
}

func NewApprovalPicker(prompt string, onChoose func(ApprovalChoice), onCancel func()) *ApprovalPicker {
	return &ApprovalPicker{prompt: prompt, onChoose: onChoose, onCancel: onCancel}
}

func (picker *ApprovalPicker) Render(width int64) []string {
	picker.mu.RLock()
	defer picker.mu.RUnlock()

	palette := theme.CurrentPalette()
	panelWidth := int(width)
	if panelWidth < 40 {
		panelWidth = 40
	}

	panelBackground := "\x1b[48;5;236m"
	backgroundReset := "\x1b[49m"
	borderTop := panelBackground + palette.BorderAccent.Render("╔"+strings.Repeat("═", panelWidth-2)+"╗") + backgroundReset
	borderBottom := panelBackground + palette.BorderAccent.Render("╚"+strings.Repeat("═", panelWidth-2)+"╝") + backgroundReset
	borderLine := func(text string) string {
		padding := panelWidth - int(core.VisibleWidth(text)) - 4
		if padding < 0 {
			padding = 0
		}
		return panelBackground + palette.BorderAccent.Render("║") + " " + text + strings.Repeat(" ", padding) + palette.BorderAccent.Render(" ║") + backgroundReset
	}

	body := []string{
		borderTop,
		borderLine(palette.Accent.Render(i18n.T("approval.required"))),
		borderLine(palette.Dim.Render(core.TruncateToWidth(picker.prompt, int64(panelWidth-4), "…"))),
		borderLine(""),
	}
	for index, option := range approvalOptions() {
		radio := palette.Dim.Render("(○)")
		label := palette.Dim.Render(option.label)
		if index == picker.selected {
			radio = palette.Accent.Render("(●)")
			label = palette.Accent.Render(option.label)
		}
		body = append(body, borderLine("  "+radio+" "+label+"  "+palette.Dim.Render(option.desc)))
	}
	body = append(body,
		borderLine(""),
		borderLine(palette.Dim.Render(i18n.T("approval.navigation"))),
		borderBottom,
	)
	return body
}

func (*ApprovalPicker) Invalidate() {}

func (picker *ApprovalPicker) Update(message core.Msg) core.Cmd {
	keyMessage, ok := message.(core.KeyMsg)
	if !ok {
		return nil
	}
	for _, key := range terminal.ParseKeys(keyMessage.Data) {
		switch key.Name {
		case "up":
			picker.move(-1)
		case "down":
			picker.move(1)
		case "enter":
			picker.confirm()
		case "escape":
			if picker.onCancel != nil {
				picker.onCancel()
			}
		case "y", "Y":
			picker.selectAndConfirm(0)
		case "s", "S":
			picker.selectAndConfirm(1)
		case "a", "A":
			picker.selectAndConfirm(2)
		case "n", "N", "d", "D":
			picker.selectAndConfirm(3)
		}
	}
	return nil
}

func (picker *ApprovalPicker) move(delta int) {
	picker.mu.Lock()
	defer picker.mu.Unlock()
	picker.selected += delta
	if picker.selected < 0 {
		picker.selected = len(approvalOptions()) - 1
	}
	if picker.selected >= len(approvalOptions()) {
		picker.selected = 0
	}
}

func (picker *ApprovalPicker) selectAndConfirm(index int) {
	picker.mu.Lock()
	picker.selected = index
	picker.mu.Unlock()
	picker.confirm()
}

func (picker *ApprovalPicker) confirm() {
	picker.mu.RLock()
	selected := picker.selected
	onChoose := picker.onChoose
	picker.mu.RUnlock()

	choices := []ApprovalChoice{ChoiceOnce, ChoiceSession, ChoiceAlways, ChoiceDeny}
	if onChoose != nil && selected >= 0 && selected < len(choices) {
		onChoose(choices[selected])
	}
}

var _ core.Component = (*ApprovalPicker)(nil)
var _ core.Updatable = (*ApprovalPicker)(nil)
