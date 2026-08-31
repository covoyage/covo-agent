package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/i18n"
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

// DashboardData is the snapshot shown by the dashboard picker.
type DashboardData struct {
	CurrentID     string
	CurrentStatus string
	Busy          bool
	MsgCount      int
	Sessions      []DashboardSession
	Tasks         []DashboardTask
}

// DashboardPickerItems converts a dashboard snapshot into picker rows.
func DashboardPickerItems(data DashboardData) []PickerItem {
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
