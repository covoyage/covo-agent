package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
)

// DashboardSession is one row in the session dashboard.
type DashboardSession struct {
	ID        string
	Name      string
	Summary   string
	Status    string
	MsgCount  int64
	UpdatedAt time.Time
	IsCurrent bool
}

// DashboardTask is one background-task row.
type DashboardTask struct {
	ID          string
	Input       string
	Status      string
	Error       string
	Turns       int64
	CurrentTurn int64
	StartedAt   time.Time
}

// DashboardSubagent is one spawned sub-agent row.
type DashboardSubagent struct {
	ID        string
	Task      string
	Status    string
	Depth     int
	StartedAt time.Time
	EndedAt   time.Time
	Live      bool
}

// DashboardData is the snapshot shown by the dashboard picker.
type DashboardData struct {
	CurrentID     string
	CurrentStatus string
	Busy          bool
	MsgCount      int
	Sessions      []DashboardSession
	Tasks         []DashboardTask
	Subagents     []DashboardSubagent
}

// DashboardSort controls sub-agent row ordering.
type DashboardSort int

const (
	DashboardSortSpawn    DashboardSort = iota // spawn order (tree)
	DashboardSortDuration                      // slowest first
	DashboardSortStatus                        // running first, then failed, then completed
)

func (s DashboardSort) Label() string {
	switch s {
	case DashboardSortDuration:
		return "slowest"
	case DashboardSortStatus:
		return "status"
	default:
		return "spawn order"
	}
}

// DashboardFilter filters sub-agent rows.
type DashboardFilter int

const (
	DashboardFilterAll     DashboardFilter = iota
	DashboardFilterRunning                 // live only
	DashboardFilterFailed                  // failed only
)

func (f DashboardFilter) Label() string {
	switch f {
	case DashboardFilterRunning:
		return "running"
	case DashboardFilterFailed:
		return "failed"
	default:
		return "all"
	}
}

// DashboardListOptions tunes the sub-agent rows inside the picker.
type DashboardListOptions struct {
	SubagentSort   DashboardSort
	SubagentFilter DashboardFilter
}

// DashboardPickerItems converts a dashboard snapshot into picker rows.
func DashboardPickerItems(data DashboardData, opts ...DashboardListOptions) []PickerItem {
	items := make([]PickerItem, 0, len(data.Sessions)+len(data.Tasks)+1)
	currentLabel := data.CurrentID
	if len(currentLabel) > 8 {
		currentLabel = currentLabel[:8]
	}
	if currentLabel == "" {
		currentLabel = i18n.T("dashboard.no_session")
	}
	status := data.CurrentStatus
	if status == "" {
		if data.Busy {
			status = i18n.T("dashboard.status_working")
		} else {
			status = i18n.T("dashboard.status_idle")
		}
	}
	items = append(items, PickerItem{
		Value:       "current:" + data.CurrentID,
		Label:       i18n.T("dashboard.current", "id", currentLabel),
		Description: fmt.Sprintf("%s · %d msgs", status, data.MsgCount),
		Category:    i18n.T("dashboard.cat_current"),
		Selected:    true,
	})
	for _, task := range data.Tasks {
		age := time.Since(task.StartedAt).Truncate(time.Second)
		preview := strings.ReplaceAll(task.Input, "\n", " ")
		if len([]rune(preview)) > 60 {
			preview = string([]rune(preview)[:57]) + "..."
		}
		desc := fmt.Sprintf("%s · turns %d/%d · %s", task.Status, task.CurrentTurn, task.Turns, age)
		if task.Error != "" {
			desc += " · " + task.Error
		}
		items = append(items, PickerItem{
			Value:       "task:" + task.ID,
			Label:       task.ID + "  " + preview,
			Description: desc,
			Category:    i18n.T("dashboard.cat_tasks"),
		})
	}
	subs := make([]DashboardSubagent, len(data.Subagents))
	copy(subs, data.Subagents)
	var opt DashboardListOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	switch opt.SubagentFilter {
	case DashboardFilterRunning:
		kept := subs[:0]
		for _, sub := range subs {
			if sub.Live {
				kept = append(kept, sub)
			}
		}
		subs = kept
	case DashboardFilterFailed:
		kept := subs[:0]
		for _, sub := range subs {
			if sub.Status == "failed" {
				kept = append(kept, sub)
			}
		}
		subs = kept
	}
	switch opt.SubagentSort {
	case DashboardSortDuration:
		sort.SliceStable(subs, func(i, j int) bool {
			return subElapsed(subs[i]) > subElapsed(subs[j])
		})
	case DashboardSortStatus:
		rank := map[string]int{"running": 0, "failed": 1, "completed": 2}
		sort.SliceStable(subs, func(i, j int) bool {
			return rank[subs[i].Status] < rank[subs[j].Status]
		})
	}
	for _, sub := range subs {
		preview := strings.ReplaceAll(sub.Task, "\n", " ")
		if len([]rune(preview)) > 60 {
			preview = string([]rune(preview)[:57]) + "..."
		}
		age := time.Since(sub.StartedAt).Truncate(time.Second)
		desc := fmt.Sprintf("%s · depth %d · %s", sub.Status, sub.Depth, age)
		if !sub.Live && !sub.EndedAt.IsZero() {
			desc = fmt.Sprintf("%s · depth %d · done %s ago", sub.Status, sub.Depth, time.Since(sub.EndedAt).Truncate(time.Second))
		}
		// Tree rendering: indent by spawn depth (depth starts at 1).
		indent := ""
		if sub.Depth > 1 {
			indent = strings.Repeat("  ", sub.Depth-1) + "└ "
		}
		label := indent + sub.ID + "  " + preview
		if sub.Live {
			label = "● " + label
		}
		items = append(items, PickerItem{
			Value:       "subagent:" + sub.ID,
			Label:       label,
			Description: desc,
			Category:    "Subagents",
			Tag:         sub.Status,
		})
	}
	for _, session := range data.Sessions {
		shortID := session.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		name := session.Name
		if name == "" {
			name = shortID
		}
		summary := session.Summary
		if summary == "" {
			summary = i18n.T("dashboard.no_summary")
		}
		if len([]rune(summary)) > 60 {
			summary = string([]rune(summary)[:57]) + "..."
		}
		label := name
		if session.IsCurrent {
			label = "▶ " + label
		}
		items = append(items, PickerItem{
			Value:       "session:" + session.ID,
			Label:       label,
			Description: fmt.Sprintf("%s · %s · %d msgs", shortID, session.Status, session.MsgCount),
			Category:    i18n.T("dashboard.cat_sessions"),
			Selected:    session.IsCurrent,
			Tag:         session.Status,
		})
	}
	return items
}

// NewDashboardPicker builds a searchable dashboard overlay.
func NewDashboardPicker(data DashboardData, onSelect func(PickerItem), onCancel func()) *Picker {
	picker := NewPicker(PickerConfig{
		Title:      i18n.T("dashboard.title"),
		PageSize:   14,
		Searchable: true,
		ShowCount:  true,
		Hint:       i18n.T("dashboard.hint"),
	})
	picker.SetItems(DashboardPickerItems(data))
	picker.OnSelect(onSelect)
	picker.OnCancel(onCancel)
	return picker
}

// subElapsed returns the total runtime of a sub-agent row.
func subElapsed(sub DashboardSubagent) time.Duration {
	if sub.EndedAt.IsZero() {
		return time.Since(sub.StartedAt)
	}
	return sub.EndedAt.Sub(sub.StartedAt)
}

// DashboardOverlay wraps the dashboard picker with sub-agent sort/filter
// cycling: "s" cycles sort mode, "f" cycles filter mode (inactive while the
// inline search is open). All other keys delegate to the picker.
type DashboardOverlay struct {
	picker *Picker
	dataFn func() DashboardData
	sort   DashboardSort
	filter DashboardFilter
}

// NewDashboardOverlay builds the dashboard overlay with live sort/filter.
func NewDashboardOverlay(dataFn func() DashboardData, onSelect func(PickerItem), onCancel func()) *DashboardOverlay {
	picker := NewPicker(PickerConfig{
		Title:      i18n.T("dashboard.title"),
		PageSize:   14,
		Searchable: true,
		ShowCount:  true,
		Hint:       "s sort · f filter · " + i18n.T("dashboard.hint"),
	})
	o := &DashboardOverlay{picker: picker, dataFn: dataFn}
	o.rebuild()
	picker.OnSelect(onSelect)
	picker.OnCancel(onCancel)
	return o
}

func (o *DashboardOverlay) rebuild() {
	o.picker.SetItems(DashboardPickerItems(o.dataFn(), DashboardListOptions{
		SubagentSort:   o.sort,
		SubagentFilter: o.filter,
	}))
}

// SortLabel returns the active sort mode label (for tests/debug).
func (o *DashboardOverlay) SortLabel() string { return o.sort.Label() }

// FilterLabel returns the active filter mode label.
func (o *DashboardOverlay) FilterLabel() string { return o.filter.Label() }

// Render 实现 core.Component。
func (o *DashboardOverlay) Render(width int64) []string { return o.picker.Render(width) }

// Invalidate 实现 core.Component。
func (o *DashboardOverlay) Invalidate() { o.picker.Invalidate() }

// Update 实现 core.Updatable。
func (o *DashboardOverlay) Update(msg core.Msg) core.Cmd {
	if keyMsg, ok := msg.(core.KeyMsg); ok {
		for _, key := range terminal.ParseKeys(keyMsg.Data) {
			if key.IsRelease() {
				continue
			}
			if !o.picker.Searching() {
				switch key.Name {
				case "s":
					o.sort = (o.sort + 1) % 3
					o.rebuild()
					return nil
				case "f":
					o.filter = (o.filter + 1) % 3
					o.rebuild()
					return nil
				}
			}
		}
	}
	return o.picker.Update(msg)
}
