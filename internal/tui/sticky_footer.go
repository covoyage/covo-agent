package tui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/theme"
)

// TodoItem is the footer's presentation model for an agent TODO.
type TodoItem struct {
	ID       string
	Content  string
	Status   string
	Priority string
}

// FooterSnapshot is a thread-safe copy of status values used by render functions.
type FooterSnapshot struct {
	GitBranch   string
	ContextUsed string
	ContextWarn bool
	Shortcuts   string
	BgTaskCount int
	Mode        string
}

// StickyFooter renders active TODOs and the configurable status line.
type StickyFooter struct {
	mu            sync.RWMutex
	todoStore     func() []TodoItem
	gitBranch     string
	contextUsed   string
	contextWarn   bool
	shortcuts     string
	bgTaskCount   int
	mode          string
	statusLineMgr *StatusLineManager
}

func NewStickyFooter() *StickyFooter {
	return &StickyFooter{}
}

func (footer *StickyFooter) SetTodoStore(store func() []TodoItem) {
	footer.mu.Lock()
	footer.todoStore = store
	footer.mu.Unlock()
}

func (footer *StickyFooter) SetGitBranch(branch string) {
	footer.mu.Lock()
	footer.gitBranch = branch
	footer.mu.Unlock()
}

func (footer *StickyFooter) SetContextUsage(usage string) {
	footer.mu.Lock()
	footer.contextUsed = usage
	footer.mu.Unlock()
}

func (footer *StickyFooter) SetContextWarn(warn bool) {
	footer.mu.Lock()
	footer.contextWarn = warn
	footer.mu.Unlock()
}

func (footer *StickyFooter) SetShortcuts(shortcuts string) {
	footer.mu.Lock()
	footer.shortcuts = shortcuts
	footer.mu.Unlock()
}

func (footer *StickyFooter) SetBgTaskCount(count int) {
	footer.mu.Lock()
	footer.bgTaskCount = count
	footer.mu.Unlock()
}

func (footer *StickyFooter) SetMode(mode string) {
	footer.mu.Lock()
	footer.mode = mode
	footer.mu.Unlock()
}

func (footer *StickyFooter) SetStatusLineManager(manager *StatusLineManager) {
	footer.mu.Lock()
	footer.statusLineMgr = manager
	footer.mu.Unlock()
}

func (footer *StickyFooter) Snapshot() FooterSnapshot {
	footer.mu.RLock()
	defer footer.mu.RUnlock()
	return FooterSnapshot{
		GitBranch:   footer.gitBranch,
		ContextUsed: footer.contextUsed,
		ContextWarn: footer.contextWarn,
		Shortcuts:   footer.shortcuts,
		BgTaskCount: footer.bgTaskCount,
		Mode:        footer.mode,
	}
}

func (footer *StickyFooter) Invalidate() {}

func (footer *StickyFooter) Render(width int64) []string {
	footer.mu.RLock()
	todoStore := footer.todoStore
	statusLineManager := footer.statusLineMgr
	snapshot := FooterSnapshot{
		GitBranch:   footer.gitBranch,
		ContextUsed: footer.contextUsed,
		ContextWarn: footer.contextWarn,
		Shortcuts:   footer.shortcuts,
		BgTaskCount: footer.bgTaskCount,
		Mode:        footer.mode,
	}
	footer.mu.RUnlock()

	palette := theme.CurrentPalette()
	var lines []string
	if todoStore != nil {
		active := filterActive(todoStore())
		if len(active) > 0 {
			lines = append(lines, footer.renderTodoLine(active, palette, width))
		}
	}
	if infoLine := footer.renderInfoLine(palette, width, statusLineManager, snapshot); infoLine != "" {
		lines = append(lines, infoLine)
	}
	return lines
}

func (footer *StickyFooter) renderTodoLine(items []TodoItem, palette *theme.Palette, width int64) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		content := item.Content
		if int64(core.VisibleWidth(content)) > 40 {
			content = core.TruncateToWidth(content, 37, "")
		}
		parts = append(parts, fmt.Sprintf("%s%s", todoStatusIcon(item.Status), todoStatusStyle(item.Status, palette).Render(content)))
	}
	line := palette.Dim.Render("TODO ") + strings.Join(parts, "  ")
	if core.VisibleWidth(line) > width {
		line = core.TruncateToWidth(line, width, "")
	}
	return core.PadToWidth(line, width)
}

func (footer *StickyFooter) renderInfoLine(palette *theme.Palette, width int64, statusLineManager *StatusLineManager, snapshot FooterSnapshot) string {
	if statusLineManager != nil {
		return statusLineManager.BuildLine(palette, width)
	}
	if snapshot.Mode == "" && snapshot.GitBranch == "" && snapshot.ContextUsed == "" && snapshot.Shortcuts == "" && snapshot.BgTaskCount == 0 {
		return ""
	}
	var parts []string
	if snapshot.Mode != "" {
		modeIcon := "◇"
		modeStyle := palette.Dim
		switch snapshot.Mode {
		case "code":
			modeIcon = "⚙"
			modeStyle = palette.Accent
		case "general":
			modeIcon = "◆"
			modeStyle = palette.Success
		}
		parts = append(parts, modeStyle.Render(fmt.Sprintf("%s %s", modeIcon, snapshot.Mode)))
	}
	if snapshot.BgTaskCount > 0 {
		parts = append(parts, palette.Accent.Render(fmt.Sprintf("⚙ %d bg", snapshot.BgTaskCount)))
	}
	if snapshot.GitBranch != "" {
		parts = append(parts, palette.Dim.Render(snapshot.GitBranch))
	}
	if snapshot.ContextUsed != "" {
		style := palette.Dim
		if snapshot.ContextWarn {
			style = palette.Error
		}
		parts = append(parts, style.Render(snapshot.ContextUsed))
	}
	if snapshot.Shortcuts != "" {
		parts = append(parts, palette.Dim.Render(snapshot.Shortcuts))
	}
	line := palette.Dim.Render(" ") + strings.Join(parts, " │ ")
	if core.VisibleWidth(line) > width {
		line = core.TruncateToWidth(line, width, "")
	}
	return core.PadToWidth(line, width)
}

func todoStatusIcon(status string) string {
	switch status {
	case "pending":
		return "○ "
	case "in_progress":
		return "◐ "
	case "completed":
		return "● "
	case "cancelled":
		return "✗ "
	default:
		return "○ "
	}
}

func todoStatusStyle(status string, palette *theme.Palette) theme.Style {
	switch status {
	case "in_progress":
		return palette.Accent
	case "completed":
		return palette.Success
	case "cancelled":
		return palette.Dim
	default:
		return palette.Assistant
	}
}

func filterActive(items []TodoItem) []TodoItem {
	var active []TodoItem
	for _, item := range items {
		if item.Status == "pending" || item.Status == "in_progress" {
			active = append(active, item)
		}
	}
	return active
}
