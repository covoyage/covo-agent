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

// PlanItem is the footer's presentation model for an agent plan step.
type PlanItem struct {
	Step   string
	Status string
}

// FooterSnapshot is a thread-safe copy of status values used by render functions.
type FooterSnapshot struct {
	GitBranch     string
	ContextUsed   string
	ContextWarn   bool
	ContextTokens int64
	ContextTotal  int64
	Shortcuts     string
	BgTaskCount   int
	Mode          string
}

// StickyFooter renders active TODOs, the current plan, and the configurable status line.
type StickyFooter struct {
	mu            sync.RWMutex
	todoStore     func() []TodoItem
	planStore     func() []PlanItem
	subagents     func() []FooterSubagent
	gitBranch     string
	contextUsed   string
	contextWarn   bool
	contextTokens int64
	contextTotal  int64
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

func (footer *StickyFooter) SetPlanStore(store func() []PlanItem) {
	footer.mu.Lock()
	footer.planStore = store
	footer.mu.Unlock()
}

// SetSubagentsProvider supplies the spawned sub-agent snapshot for the
// footer's live sub-agent bottom strip. Return
// nil/empty to hide the line.
func (footer *StickyFooter) SetSubagentsProvider(store func() []FooterSubagent) {
	footer.subagents = store
}

// FooterSubagent is one sub-agent row in the footer strip.
type FooterSubagent struct {
	ID     string
	Task   string
	Status string
	Live   bool
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

func (footer *StickyFooter) SetContextTokens(used, total int64) {
	footer.mu.Lock()
	footer.contextTokens = used
	footer.contextTotal = total
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
		GitBranch:     footer.gitBranch,
		ContextUsed:   footer.contextUsed,
		ContextWarn:   footer.contextWarn,
		ContextTokens: footer.contextTokens,
		ContextTotal:  footer.contextTotal,
		Shortcuts:     footer.shortcuts,
		BgTaskCount:   footer.bgTaskCount,
		Mode:          footer.mode,
	}
}

func (footer *StickyFooter) Invalidate() {}

func (footer *StickyFooter) Render(width int64) []string {
	footer.mu.RLock()
	todoStore := footer.todoStore
	planStore := footer.planStore
	subagents := footer.subagents
	statusLineManager := footer.statusLineMgr
	snapshot := FooterSnapshot{
		GitBranch:     footer.gitBranch,
		ContextUsed:   footer.contextUsed,
		ContextWarn:   footer.contextWarn,
		ContextTokens: footer.contextTokens,
		ContextTotal:  footer.contextTotal,
		Shortcuts:     footer.shortcuts,
		BgTaskCount:   footer.bgTaskCount,
		Mode:          footer.mode,
	}
	footer.mu.RUnlock()

	palette := theme.CurrentPalette()
	var lines []string
	if planStore != nil {
		if planLine := footer.renderPlanLine(planStore(), palette, width); planLine != "" {
			lines = append(lines, planLine)
		}
	}
	if todoStore != nil {
		active := filterActive(todoStore())
		if len(active) > 0 {
			lines = append(lines, footer.renderTodoLine(active, palette, width))
		}
	}
	var subagentEntries []FooterSubagent
	if subagents != nil {
		subagentEntries = subagents()
	}
	if line := footer.renderAsyncWorkLine(subagentEntries, snapshot.BgTaskCount, palette, width); line != "" {
		lines = append(lines, line)
	}
	if infoLine := footer.renderInfoLine(palette, width, statusLineManager, snapshot); infoLine != "" {
		lines = append(lines, infoLine)
	}
	return lines
}

func (footer *StickyFooter) renderPlanLine(items []PlanItem, palette *theme.Palette, width int64) string {
	if len(items) == 0 {
		return ""
	}
	done := 0
	var current string
	for _, item := range items {
		switch item.Status {
		case "completed":
			done++
		case "in_progress":
			current = item.Step
		}
	}
	label := palette.Dim.Render(fmt.Sprintf("PLAN %d/%d", done, len(items)))
	if current != "" {
		if int64(core.VisibleWidth(current)) > 48 {
			current = core.TruncateToWidth(current, 45, "")
		}
		label = label + "  " + palette.Accent.Render("◐ "+current)
	}
	// Leading space aligns with the status line below.
	label = " " + label
	if core.VisibleWidth(label) > width {
		label = core.TruncateToWidth(label, width, "")
	}
	return core.PadToWidth(label, width)
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
	// Leading space aligns with the status line below.
	line = " " + line
	if core.VisibleWidth(line) > width {
		line = core.TruncateToWidth(line, width, "")
	}
	return core.PadToWidth(line, width)
}

// renderAsyncWorkLine renders the unified background-work strip: live spawned
// sub-agents and background task count share one
// line, visible only while there is async work. The leading space aligns the
// strip with the status line below it.
func (footer *StickyFooter) renderAsyncWorkLine(entries []FooterSubagent, bgCount int, palette *theme.Palette, width int64) string {
	live := 0
	latest := ""
	for _, e := range entries {
		if !e.Live {
			continue
		}
		live++
		if latest == "" {
			latest = SubagentTaskPreview(e.Task, 40)
		}
	}
	if w := int64(core.VisibleWidth(latest)); w > width/3 {
		latest = core.TruncateToWidth(latest, width/3, "")
	}
	if live == 0 && bgCount == 0 {
		return ""
	}

	var parts []string
	if live > 0 {
		agents := "agents"
		if live == 1 {
			agents = "agent"
		}
		parts = append(parts, fmt.Sprintf("▸ %d live %s", live, agents))
	}
	if bgCount > 0 {
		bg := "tasks"
		if bgCount == 1 {
			bg = "task"
		}
		parts = append(parts, fmt.Sprintf("⚙ %d bg %s", bgCount, bg))
	}
	switch {
	case live > 0:
		parts = append(parts, latest, "alt+←/→")
	default:
		parts = append(parts, "/agents")
	}

	line := " " + palette.Accent.Render(strings.Join(parts, " · "))
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
