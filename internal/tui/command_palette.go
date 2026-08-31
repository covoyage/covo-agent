package tui

import (
	"strings"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"

	"github.com/covoyage/covo-agent/internal/i18n"
)

// PaletteItem is one command-palette row.
type PaletteItem struct {
	ID          string
	Label       string
	Description string
	Category    string
	Run         func()
}

// PaletteAction is a slash or hotkey action shown in the palette.
type PaletteAction struct {
	ID          string
	Label       string
	Description string
	Category    string
}

// CommandPaletteItems builds picker rows from slash suggestions and actions.
func CommandPaletteItems(slash []core.Suggestion, actions []PaletteAction) []PickerItem {
	items := make([]PickerItem, 0, len(slash)+len(actions))
	seen := make(map[string]bool, len(slash)+len(actions))
	for _, action := range actions {
		id := strings.TrimSpace(action.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		label := action.Label
		if label == "" {
			label = id
		}
		items = append(items, PickerItem{
			Value:       "action:" + id,
			Label:       label,
			Description: action.Description,
			Category:    firstNonEmpty(action.Category, i18n.T("palette.cat_actions")),
		})
	}
	for _, sug := range slash {
		id := strings.TrimSpace(sug.InsertText)
		if id == "" {
			continue
		}
		value := "slash:" + id
		if seen[value] {
			continue
		}
		seen[value] = true
		label := sug.Label
		if label == "" {
			label = "/" + id
		}
		items = append(items, PickerItem{
			Value:       value,
			Label:       label,
			Description: sug.Description,
			Category:    i18n.T("palette.cat_slash"),
		})
	}
	return items
}

// DefaultPaletteActions returns the always-available palette rows.
func DefaultPaletteActions() []PaletteAction {
	return []PaletteAction{
		{ID: "help", Label: i18n.T("palette.help"), Description: i18n.T("keybinding.help"), Category: i18n.T("palette.cat_actions")},
		{ID: "sessions", Label: i18n.T("palette.sessions"), Description: i18n.T("keybinding.session"), Category: i18n.T("palette.cat_actions")},
		{ID: "todos", Label: i18n.T("palette.todos"), Description: i18n.T("keybinding.todo"), Category: i18n.T("palette.cat_actions")},
		{ID: "skills", Label: i18n.T("palette.skills"), Description: i18n.T("keybinding.skills"), Category: i18n.T("palette.cat_actions")},
		{ID: "model", Label: i18n.T("palette.model"), Description: i18n.T("keybinding.model_picker"), Category: i18n.T("palette.cat_actions")},
		{ID: "settings", Label: i18n.T("palette.settings"), Description: i18n.T("commands.settings"), Category: i18n.T("palette.cat_actions")},
		{ID: "queue", Label: i18n.T("palette.queue"), Description: i18n.T("commands.prompts"), Category: i18n.T("palette.cat_actions")},
		{ID: "dashboard", Label: i18n.T("palette.dashboard"), Description: i18n.T("commands.dashboard"), Category: i18n.T("palette.cat_actions")},
		{ID: "search", Label: i18n.T("palette.search"), Description: i18n.T("keybinding.search"), Category: i18n.T("palette.cat_actions")},
		{ID: "files", Label: i18n.T("palette.files"), Description: i18n.T("keybinding.changed_files"), Category: i18n.T("palette.cat_actions")},
	}
}

// NewCommandPalette builds a searchable picker pre-filled with palette items.
func NewCommandPalette(items []PickerItem, onSelect func(PickerItem), onCancel func()) *Picker {
	picker := NewPicker(PickerConfig{
		Title:      i18n.T("palette.title"),
		PageSize:   12,
		Searchable: true,
		ShowCount:  true,
		Hint:       i18n.T("palette.hint"),
	})
	picker.SetItems(items)
	picker.SetSearching(true)
	picker.OnSelect(onSelect)
	picker.OnCancel(onCancel)
	return picker
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// PaletteKeyID is the default command-palette chord.
const PaletteKeyID terminal.KeyID = "ctrl+k"
