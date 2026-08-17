package panels

import (
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"

	"github.com/covoyage/covo-agent/internal/i18n"
)

// AskUserPicker is an inline selector that presents a question with a list of
// suggested answers. The user picks one with ↑/↓ + Enter or a number key, or
// cancels with Esc. Free-form answers are handled by the caller (e.g. via
// stdin) when no options are supplied.
type AskUserPicker struct {
	mu       sync.RWMutex
	question string
	options  []string
	selected int
	onChoose func(string)
	onCancel func()
}

func NewAskUserPicker(question string, options []string, onChoose func(string), onCancel func()) *AskUserPicker {
	if options == nil {
		options = []string{}
	}
	return &AskUserPicker{question: question, options: options, onChoose: onChoose, onCancel: onCancel}
}

func (picker *AskUserPicker) Render(width int64) []string {
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
		borderLine(palette.Accent.Render(i18n.T("ask_user.title"))),
		borderLine(palette.Dim.Render(core.TruncateToWidth(picker.question, int64(panelWidth-4), "…"))),
		borderLine(""),
	}
	for index, option := range picker.options {
		radio := palette.Dim.Render("(○)")
		label := palette.Dim.Render(option)
		if index == picker.selected {
			radio = palette.Accent.Render("(●)")
			label = palette.Accent.Render(option)
		}
		body = append(body, borderLine("  "+radio+" "+label))
	}
	body = append(body,
		borderLine(""),
		borderLine(palette.Dim.Render(i18n.T("ask_user.navigation"))),
		borderBottom,
	)
	return body
}

func (*AskUserPicker) Invalidate() {}

func (picker *AskUserPicker) Update(message core.Msg) core.Cmd {
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
		}
		// Number-key shortcuts select a specific option (1-indexed).
		if len(key.Name) == 1 && key.Name[0] >= '1' && key.Name[0] <= '9' {
			idx := int(key.Name[0] - '1')
			picker.selectIndex(idx)
		}
	}
	return nil
}

func (picker *AskUserPicker) move(delta int) {
	picker.mu.Lock()
	defer picker.mu.Unlock()
	n := len(picker.options)
	if n == 0 {
		return
	}
	picker.selected += delta
	if picker.selected < 0 {
		picker.selected = n - 1
	}
	if picker.selected >= n {
		picker.selected = 0
	}
}

func (picker *AskUserPicker) selectIndex(idx int) {
	picker.mu.Lock()
	if idx < 0 || idx >= len(picker.options) {
		picker.mu.Unlock()
		return
	}
	picker.selected = idx
	picker.mu.Unlock()
	picker.confirm()
}

func (picker *AskUserPicker) confirm() {
	picker.mu.RLock()
	selected := picker.selected
	onChoose := picker.onChoose
	options := picker.options
	picker.mu.RUnlock()

	if onChoose != nil && selected >= 0 && selected < len(options) {
		onChoose(options[selected])
	}
}

var _ core.Component = (*AskUserPicker)(nil)
var _ core.Updatable = (*AskUserPicker)(nil)
