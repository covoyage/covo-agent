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
	// Priority orders segments when the line overflows: lower value = kept
	// longer. Segments are rendered in declared order but dropped from the
	// highest priority value first when they no longer fit.
	priority int
	render   func(palette *theme.Palette) string
}

// StatusLineManager owns status-line segments and their render functions.
type StatusLineManager struct {
	mu    sync.RWMutex
	items []*StatusLineItem
}

func NewStatusLineManager() *StatusLineManager {
	return &StatusLineManager{items: []*StatusLineItem{
		{ID: "mode", Label: "mode", Description: i18n.T("statusline.mode_desc"), Enabled: true, priority: 0},
		{ID: "git-branch", Label: "git-branch", Description: i18n.T("statusline.git_desc"), Enabled: true, priority: 2},
		{ID: "context-used", Label: "context-used", Description: i18n.T("statusline.tokens_desc"), Enabled: true, priority: 3},
		{ID: "cost", Label: "cost", Description: i18n.T("statusline.cost_desc"), Enabled: true, priority: 4},
		{ID: "activity", Label: "activity", Description: i18n.T("statusline.activity_desc"), Enabled: true, priority: 5},
		{ID: "queued", Label: "queued", Description: i18n.T("statusline.queued_desc"), Enabled: true, priority: 6},
		{ID: "shortcuts", Label: "shortcuts", Description: i18n.T("statusline.shortcuts_desc"), Enabled: true, priority: 7},
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

	type segment struct {
		priority int
		text     string
	}
	var segs []segment
	for _, item := range manager.items {
		if !item.Enabled || item.render == nil {
			continue
		}
		if text := item.render(palette); text != "" {
			segs = append(segs, segment{priority: item.priority, text: text})
		}
	}
	if len(segs) == 0 {
		return ""
	}

	// Priority-aware fit: emit segments in declared (visual) order but drop
	// the highest-priority-value segments first when the line overflows, and
	// hard-truncate only as a last resort. Keeps the shortcuts tail from
	// being silently cut by a long middle segment.
	fits := func(parts []string) bool {
		line := palette.Dim.Render(" ") + strings.Join(parts, " │ ")
		return int64(core.VisibleWidth(line)) <= width
	}
	for {
		parts := make([]string, len(segs))
		for i, sg := range segs {
			parts[i] = sg.text
		}
		if fits(parts) {
			return core.PadToWidth(palette.Dim.Render(" ")+strings.Join(parts, " │ "), width)
		}
		// Find the highest priority value (least important) segment.
		worst := -1
		worstPri := -1
		for i, sg := range segs {
			if sg.priority >= worstPri {
				worstPri = sg.priority
				worst = i
			}
		}
		if worst < 0 {
			break
		}
		if len(segs) == 1 {
			// Last segment standing: truncate it instead of dropping.
			return core.PadToWidth(core.TruncateToWidth(segs[0].text, width, ""), width)
		}
		segs = append(segs[:worst], segs[worst+1:]...)
	}
	// Unreachable, but keep a safe fallback.
	parts := make([]string, len(segs))
	for i, sg := range segs {
		parts[i] = sg.text
	}
	return core.PadToWidth(palette.Dim.Render(" ")+strings.Join(parts, " │ "), width)
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
