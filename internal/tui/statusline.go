package tui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/component"
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/theme"

	"github.com/covoyage/covo-agent/internal/i18n"
)

// StatusLineItem describes one configurable status-line segment.
type StatusLineItem struct {
	ID          string
	Label       string
	Description string
	Enabled     bool
	render      func(palette *theme.Palette) string
}

// StatusLineManager owns status-line segments and their render functions.
type StatusLineManager struct {
	mu    sync.RWMutex
	items []*StatusLineItem
}

func NewStatusLineManager() *StatusLineManager {
	return &StatusLineManager{items: []*StatusLineItem{
		{ID: "mode", Label: "mode", Description: i18n.T("statusline.mode_desc"), Enabled: true},
		{ID: "bg-tasks", Label: "bg-tasks", Description: i18n.T("statusline.bg_desc"), Enabled: true},
		{ID: "git-branch", Label: "git-branch", Description: i18n.T("statusline.git_desc"), Enabled: true},
		{ID: "context-used", Label: "context-used", Description: i18n.T("statusline.tokens_desc"), Enabled: true},
		{ID: "shortcuts", Label: "shortcuts", Description: i18n.T("statusline.shortcuts_desc"), Enabled: true},
	}}
}

func (manager *StatusLineManager) SetRenderFn(id string, render func(*theme.Palette) string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for _, item := range manager.items {
		if item.ID == id {
			item.render = render
			return
		}
	}
}

func (manager *StatusLineManager) Items() []*StatusLineItem {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	items := make([]*StatusLineItem, len(manager.items))
	copy(items, manager.items)
	return items
}

func (manager *StatusLineManager) EnabledIDs() []string {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	var ids []string
	for _, item := range manager.items {
		if item.Enabled {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func (manager *StatusLineManager) Toggle(id string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for _, item := range manager.items {
		if item.ID == id {
			item.Enabled = !item.Enabled
			return
		}
	}
}

func (manager *StatusLineManager) BuildLine(palette *theme.Palette, width int64) string {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	var parts []string
	for _, item := range manager.items {
		if !item.Enabled || item.render == nil {
			continue
		}
		if text := item.render(palette); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	line := palette.Dim.Render(" ") + strings.Join(parts, " │ ")
	if core.VisibleWidth(line) > width {
		line = core.TruncateToWidth(line, width, "")
	}
	return core.PadToWidth(line, width)
}

func (manager *StatusLineManager) ShowDialog(app *chat.ChatApp) {
	selector := component.NewSelectList(manager.selectItems())
	selector.SetMaxVisible(10)
	selector.OnSelect(func(selected component.SelectItem) {
		manager.Toggle(selected.Value)
		selector.SetItems(manager.selectItems())
		app.Host().RequestRender()
	})
	NewUIBus(app).ShowPanel(selector, 60, 70)
}

func (manager *StatusLineManager) selectItems() []component.SelectItem {
	items := manager.Items()
	selected := make([]component.SelectItem, 0, len(items))
	for _, item := range items {
		mark := "○"
		if item.Enabled {
			mark = "●"
		}
		selected = append(selected, component.SelectItem{
			Value:       item.ID,
			Label:       fmt.Sprintf("%s %s", mark, item.Label),
			Description: item.Description,
		})
	}
	return selected
}
