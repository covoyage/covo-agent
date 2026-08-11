package panels

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

// MCPEntry is the UI DTO for an MCP marketplace entry.
type MCPEntry struct {
	Name        string
	DisplayName string
	Description string
	Category    string
	Command     string
	Args        []string
	EnvVars     []string
}

// MCPMarketplacePanel is a TUI component for browsing and installing MCP servers.
type MCPMarketplacePanel struct {
	mu          sync.RWMutex
	km          *terminal.KeybindingsManager
	entries     []MCPEntry
	filtered    []MCPEntry
	selected    int
	filter      string
	filterMode  bool
	categoryIdx int // -1 = all categories
	categories  []string
	configured  map[string]bool // lowercased server names already in config
	onInstall   func(entry MCPEntry)
	onCancel    func()
	height      int
}

var _ core.Component = (*MCPMarketplacePanel)(nil)
var _ core.Updatable = (*MCPMarketplacePanel)(nil)

// NewMCPMarketplacePanel creates the marketplace panel.
func NewMCPMarketplacePanel(entries []MCPEntry, categories []string, configured map[string]bool) *MCPMarketplacePanel {
	km := terminal.NewKeybindingsManager(map[string]terminal.KeybindingDef{
		"mcp.up":       {DefaultKeys: []terminal.KeyID{"up", "ctrl+p"}},
		"mcp.down":     {DefaultKeys: []terminal.KeyID{"down", "ctrl+n"}},
		"mcp.confirm":  {DefaultKeys: []terminal.KeyID{"enter"}},
		"mcp.cancel":   {DefaultKeys: []terminal.KeyID{"esc"}},
		"mcp.filter":   {DefaultKeys: []terminal.KeyID{"/"}},
		"mcp.category": {DefaultKeys: []terminal.KeyID{"tab"}},
	})

	configuredCopy := make(map[string]bool)
	if configured != nil {
		for name, ok := range configured {
			configuredCopy[name] = ok
		}
	}

	p := &MCPMarketplacePanel{
		km:          km,
		entries:     copyMCPEntries(entries),
		categories:  copyCategories(categories),
		categoryIdx: -1,
		configured:  configuredCopy,
		height:      24,
	}
	p.refresh()
	return p
}

func copyMCPEntries(entries []MCPEntry) []MCPEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]MCPEntry, 0, len(entries))
	for _, e := range entries {
		copied := MCPEntry{
			Name:        e.Name,
			DisplayName: e.DisplayName,
			Description: e.Description,
			Category:    e.Category,
			Command:     e.Command,
			Args:        append([]string(nil), e.Args...),
			EnvVars:     append([]string(nil), e.EnvVars...),
		}
		out = append(out, copied)
	}
	return out
}

func copyCategories(categories []string) []string {
	if len(categories) == 0 {
		return nil
	}
	out := make([]string, len(categories))
	copy(out, categories)
	return out
}

func (p *MCPMarketplacePanel) SetOnInstall(fn func(entry MCPEntry)) {
	p.mu.Lock()
	p.onInstall = fn
	p.mu.Unlock()
}

func (p *MCPMarketplacePanel) SetOnCancel(fn func()) {
	p.mu.Lock()
	p.onCancel = fn
	p.mu.Unlock()
}

func (p *MCPMarketplacePanel) Invalidate() {}

func (p *MCPMarketplacePanel) Update(msg core.Msg) core.Cmd {
	if m, ok := msg.(core.KeyMsg); ok {
		p.processKeys(m.Data)
	}
	return nil
}

func (p *MCPMarketplacePanel) processKeys(data string) {
	p.mu.RLock()
	km := p.km
	fm := p.filterMode
	p.mu.RUnlock()

	if fm {
		p.handleFilterInput(data)
		return
	}

	switch {
	case km.Matches(data, "mcp.up"):
		p.moveSelected(-1)
	case km.Matches(data, "mcp.down"):
		p.moveSelected(1)
	case km.Matches(data, "mcp.confirm"):
		p.install()
	case km.Matches(data, "mcp.cancel"):
		p.cancel()
	case km.Matches(data, "mcp.filter"):
		p.startFilter()
	case km.Matches(data, "mcp.category"):
		p.cycleCategory()
	}
}

func (p *MCPMarketplacePanel) handleFilterInput(data string) {
	if terminal.MatchesKey(data, "enter") {
		p.mu.Lock()
		p.filterMode = false
		p.mu.Unlock()
		p.refresh()
		return
	}
	if terminal.MatchesKey(data, "escape") {
		p.mu.Lock()
		p.filterMode = false
		p.filter = ""
		p.mu.Unlock()
		p.refresh()
		return
	}
	if terminal.MatchesKey(data, "backspace") {
		p.mu.Lock()
		if len(p.filter) > 0 {
			p.filter = p.filter[:len(p.filter)-1]
		}
		p.mu.Unlock()
		p.refresh()
		return
	}
	if len(data) == 1 && data[0] >= 32 && data[0] < 127 {
		p.mu.Lock()
		p.filter += data
		p.mu.Unlock()
		p.refresh()
	}
}

func (p *MCPMarketplacePanel) startFilter() {
	p.mu.Lock()
	p.filterMode = true
	p.mu.Unlock()
}

func (p *MCPMarketplacePanel) cycleCategory() {
	p.mu.Lock()
	p.categoryIdx++
	if p.categoryIdx >= len(p.categories) {
		p.categoryIdx = -1 // wrap to "all"
	}
	p.mu.Unlock()
	p.refresh()
}

func (p *MCPMarketplacePanel) moveSelected(delta int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.selected += delta
	if p.selected < 0 {
		p.selected = 0
	}
	if p.selected >= len(p.filtered) {
		p.selected = len(p.filtered) - 1
	}
}

func (p *MCPMarketplacePanel) install() {
	p.mu.RLock()
	sel := p.selected
	entries := p.filtered
	fn := p.onInstall
	p.mu.RUnlock()

	if sel >= 0 && sel < len(entries) && fn != nil {
		entry := entries[sel]
		go fn(entry)
	}
}

func (p *MCPMarketplacePanel) cancel() {
	p.mu.RLock()
	fn := p.onCancel
	p.mu.RUnlock()
	if fn != nil {
		go fn()
	}
}

func (p *MCPMarketplacePanel) refresh() {
	p.mu.Lock()
	defer p.mu.Unlock()

	all := copyMCPEntries(p.entries)

	if p.categoryIdx >= 0 && p.categoryIdx < len(p.categories) {
		cat := p.categories[p.categoryIdx]
		filtered := make([]MCPEntry, 0, len(all))
		for _, e := range all {
			if e.Category == cat {
				filtered = append(filtered, e)
			}
		}
		all = filtered
	}

	if p.filter != "" {
		q := strings.ToLower(p.filter)
		filtered := make([]MCPEntry, 0, len(all))
		for _, e := range all {
			if strings.Contains(strings.ToLower(e.Name), q) ||
				strings.Contains(strings.ToLower(e.DisplayName), q) ||
				strings.Contains(strings.ToLower(e.Description), q) ||
				strings.Contains(strings.ToLower(e.Category), q) {
				filtered = append(filtered, e)
			}
		}
		all = filtered
	}

	sort.Slice(all, func(i, j int) bool {
		return strings.ToLower(all[i].Name) < strings.ToLower(all[j].Name)
	})

	p.filtered = all
	if p.selected >= len(p.filtered) {
		p.selected = len(p.filtered) - 1
	}
	if p.selected < 0 {
		p.selected = 0
	}
}

func (p *MCPMarketplacePanel) Render(width int64) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if width < 1 {
		width = 1
	}
	pal := theme.CurrentPalette()

	var lines []string

	title := core.PadToWidth("🔧 MCP Server Marketplace", width)
	lines = append(lines, pal.Accent.Render(title))
	lines = append(lines, pal.Dim.Render(strings.Repeat("─", int(width))))

	catLabel := "All"
	if p.categoryIdx >= 0 && p.categoryIdx < len(p.categories) {
		catLabel = p.categories[p.categoryIdx]
	}
	catLine := fmt.Sprintf("  Category: %s  (Tab to cycle)  |  %d servers available", catLabel, len(p.filtered))
	lines = append(lines, pal.Dim.Render(core.PadToWidth(catLine, width)))

	if p.filterMode {
		searchText := p.filter
		if searchText == "" {
			searchText = "type to search..."
		}
		searchBar := core.PadToWidth("  / "+searchText+"█", width)
		lines = append(lines, pal.Accent.Render(searchBar))
	} else if p.filter != "" {
		filterText := core.PadToWidth("  filter: "+p.filter, width)
		lines = append(lines, pal.Dim.Render(filterText))
	}

	lines = append(lines, pal.Dim.Render(strings.Repeat("─", int(width))))

	if len(p.filtered) == 0 {
		msg := core.PadToWidth("  No MCP servers found. Try a different filter.", width)
		lines = append(lines, pal.Dim.Render(msg))
	} else {
		maxVisible := int(p.calcHeight() - 8)
		if maxVisible < 4 {
			maxVisible = 4
		}
		start, end := VisibleRange(p.selected, maxVisible, len(p.filtered))
		for i := start; i < end; i++ {
			entry := p.filtered[i]
			line := p.renderEntry(entry, width, pal, i == p.selected)
			lines = append(lines, line)
		}
	}

	for int64(len(lines)) < p.calcHeight() {
		lines = append(lines, "")
	}

	if len(lines) > 0 {
		lines = append(lines, pal.Dim.Render(strings.Repeat("─", int(width))))
		footer := core.PadToWidth("  ↑↓ navigate  Tab category  / search  Enter install  Esc close", width)
		lines = append(lines, pal.Dim.Render(footer))
	}

	return lines
}

func (p *MCPMarketplacePanel) renderEntry(entry MCPEntry, width int64, pal *theme.Palette, selected bool) string {
	var buf strings.Builder

	isInstalled := p.configured[strings.ToLower(entry.Name)]
	if isInstalled {
		buf.WriteString(pal.Success.Render("✓"))
	} else {
		buf.WriteString("○")
	}
	buf.WriteString(" ")

	buf.WriteString(pal.Accent.Render(entry.DisplayName))
	buf.WriteString(pal.Dim.Render(fmt.Sprintf(" (%s)", entry.Name)))

	buf.WriteString(" ")
	buf.WriteString(pal.Dim.Render("[" + entry.Category + "]"))

	desc := entry.Description
	maxDescWidth := int(width) - int(core.VisibleWidth(buf.String())) - 4
	if maxDescWidth > 10 && len(desc) > maxDescWidth {
		desc = desc[:maxDescWidth-1] + "…"
	}
	if desc != "" {
		buf.WriteString("  ")
		buf.WriteString(desc)
	}

	result := core.PadToWidth(buf.String(), width)
	if int64(core.VisibleWidth(buf.String())) > width {
		result = core.PadToWidth(core.TruncateToWidth(buf.String(), width, "…"), width)
	}

	if selected {
		return pal.SelectHighlight.Render(result)
	}
	return result
}

func (p *MCPMarketplacePanel) calcHeight() int64 {
	h := int64(p.height)
	if h < 10 {
		h = 24
	}
	return h
}
