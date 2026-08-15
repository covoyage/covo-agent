package tui

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"

	"github.com/covoyage/covo-agent/internal/i18n"
)

// keybindingI18n maps built-in covonaut keybinding IDs to i18n keys, so the
// help panel renders localized descriptions for the library's bindings.
var keybindingI18n = map[string]string{
	"tui.editor.cursorUp":           "keybinding.cursor_up",
	"tui.editor.cursorDown":         "keybinding.cursor_down",
	"tui.editor.cursorLeft":         "keybinding.cursor_left",
	"tui.editor.cursorRight":        "keybinding.cursor_right",
	"tui.editor.cursorWordLeft":     "keybinding.word_left",
	"tui.editor.cursorWordRight":    "keybinding.word_right",
	"tui.editor.cursorLineStart":    "keybinding.line_start",
	"tui.editor.cursorLineEnd":      "keybinding.line_end",
	"tui.editor.pageUp":             "keybinding.page_up",
	"tui.editor.pageDown":           "keybinding.page_down",
	"tui.editor.deleteCharBackward": "keybinding.delete_backward",
	"tui.editor.deleteCharForward":  "keybinding.delete_forward",
	"tui.editor.deleteWordBackward": "keybinding.delete_word_backward",
	"tui.editor.deleteWordForward":  "keybinding.delete_word_forward",
	"tui.editor.deleteToLineStart":  "keybinding.delete_to_line_start",
	"tui.editor.deleteToLineEnd":    "keybinding.delete_to_line_end",
	"tui.editor.yank":               "keybinding.yank",
	"tui.editor.yankPop":            "keybinding.yank_pop",
	"tui.editor.undo":               "keybinding.undo",
	"tui.editor.selectAll":          "keybinding.select_all",
	"tui.input.newLine":             "keybinding.input_newline",
	"tui.input.submit":              "keybinding.input_submit",
	"tui.input.tab":                 "keybinding.input_tab",
	"tui.input.copy":                "keybinding.input_copy",
	"tui.select.up":                 "keybinding.sel_up",
	"tui.select.down":               "keybinding.sel_down",
	"tui.select.pageUp":             "keybinding.sel_page_up",
	"tui.select.pageDown":           "keybinding.sel_page_down",
	"tui.select.confirm":            "keybinding.sel_confirm",
	"tui.select.cancel":             "keybinding.sel_cancel",
}

// groupI18n maps keybinding group prefixes to i18n keys for localized group
// headers in the help panel.
var groupI18n = map[string]string{
	"app":        "keybinding.group_app",
	"slash":      "keybinding.group_slash",
	"tui.editor": "keybinding.group_editor",
	"tui.input":  "keybinding.group_input",
	"tui.select": "keybinding.group_select",
}

// helpRow is one reference line inside the help panel.
type helpRow struct {
	group string
	id    string
	label string
	keys  string
	desc  string
}

// HelpPanel renders a scrollable keybinding/slash-command reference with a
// fixed title bar. Unlike covonaut's KeyHelp — where the title scrolls away
// with the body — the title (and the scroll indicator) stay pinned while the
// body scrolls with ↑/↓/PgUp/PgDn. Pressing "/" enters filter mode; type to
// narrow the list, Backspace edits the query, and Esc exits filter mode (a
// second Esc closes the panel).
type HelpPanel struct {
	mu         sync.Mutex
	title      string
	km         *terminal.KeybindingsManager
	maxVisible int64

	rows      []helpRow
	filtered  []helpRow
	filtering bool
	query     string
	offset    int64
}

// NewHelpPanel creates a help panel with a fixed title bar and a scrollable
// body. maxVisible is the number of body lines that fit in the panel.
func NewHelpPanel(title string, km *terminal.KeybindingsManager, maxVisible int64) *HelpPanel {
	p := &HelpPanel{title: title, km: km}
	if maxVisible < 6 {
		maxVisible = 6
	}
	p.maxVisible = maxVisible
	p.rows = buildHelpRows(km)
	return p
}

// Render implements core.Component. Title + separator + bottom bar are
// pinned; only the body lines scroll.
func (p *HelpPanel) Render(width int64) []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	pal := theme.CurrentPalette()
	rows := p.currentRows()
	body := p.buildBody(width, rows)
	bodyLen := countBodyLines(rows)

	visible := p.visibleRows()
	overflow := bodyLen - visible
	if overflow < 0 {
		overflow = 0
	}
	p.clampOffset(bodyLen, visible)

	out := []string{
		pal.User.Render(p.title),
		pal.Dim.Render(strings.Repeat("─", int(width))),
	}

	start := p.offset
	stop := start + visible
	if stop > int64(len(body)) {
		stop = int64(len(body))
	}
	for _, ln := range body[start:stop] {
		out = append(out, ln)
	}

	if len(body) == 0 && p.filtering && p.query != "" {
		out = append(out, pal.Dim.Render("  "+i18n.T("keybinding.no_matches")))
	}

	rangeStr := ""
	if overflow > 0 {
		rangeStr = fmt.Sprintf("%d–%d / %d · ", start+1, stop, bodyLen)
	}
	if p.filtering {
		out = append(out, pal.Accent.Render("  / "+p.query+"▏"))
		out = append(out, pal.Dim.Render("  "+rangeStr+i18n.T("keybinding.filter_hint")))
	} else {
		out = append(out, pal.Dim.Render("  "+rangeStr+i18n.T("keybinding.scroll_hint")+" · "+i18n.T("keybinding.filter_action")))
	}

	return out
}

// Invalidate implements core.Component. Rendering is stateless per width.
func (p *HelpPanel) Invalidate() {}

// Update implements core.Updatable: ↑/↓/PgUp/PgDn scroll the body, "/" enters
// filter mode, and typed characters narrow the filtered list.
func (p *HelpPanel) Update(msg core.Msg) core.Cmd {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch m := msg.(type) {
	case core.KeyMsg:
		return p.updateKey(m.Data)
	case core.PasteMsg:
		if p.filtering && m.Text != "" {
			p.query += m.Text
			p.applyFilter()
		}
	}
	return nil
}

func (p *HelpPanel) updateKey(data string) core.Cmd {
	if p.filtering {
		switch {
		case data == "backspace" || data == "\x7f":
			if len(p.query) > 0 {
				p.query = trimLastRune(p.query)
				p.applyFilter()
			}
		case terminal.MatchesKey(data, "up"):
			p.offset--
		case terminal.MatchesKey(data, "down"):
			p.offset++
		case terminal.MatchesKey(data, "pgup"):
			p.offset -= p.maxVisible
		case terminal.MatchesKey(data, "pgdown"):
			p.offset += p.maxVisible
		default:
			if isPrintableKey(data) {
				p.query += data
				p.applyFilter()
			}
		}
		p.clampOffset(countBodyLines(p.currentRows()), p.visibleRows())
		return nil
	}
	switch {
	case data == "/":
		p.filtering = true
		p.query = ""
		p.filtered = nil
		p.offset = 0
	case terminal.MatchesKey(data, "up"):
		p.offset--
	case terminal.MatchesKey(data, "down"):
		p.offset++
	case terminal.MatchesKey(data, "pgup"):
		p.offset -= p.maxVisible
	case terminal.MatchesKey(data, "pgdown"):
		p.offset += p.maxVisible
	}
	p.clampOffset(countBodyLines(p.currentRows()), p.visibleRows())
	return nil
}

// HandleEsc implements escHandler: in filter mode Esc exits the filter and is
// consumed; otherwise the panel closes.
func (p *HelpPanel) HandleEsc() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.filtering {
		p.filtering = false
		p.query = ""
		p.filtered = nil
		p.offset = 0
		return true
	}
	return false
}

// currentRows returns the rows to render (filtered or full).
func (p *HelpPanel) currentRows() []helpRow {
	if p.filtering && p.query != "" {
		return p.filtered
	}
	return p.rows
}

// visibleRows returns how many body lines fit, reserving one pinned line for
// the filter input while filtering.
func (p *HelpPanel) visibleRows() int64 {
	if p.filtering && p.maxVisible > 1 {
		return p.maxVisible - 1
	}
	return p.maxVisible
}

// applyFilter rebuilds the filtered row list from the current query.
func (p *HelpPanel) applyFilter() {
	q := strings.ToLower(p.query)
	if q == "" {
		p.filtered = nil
		p.offset = 0
		return
	}
	var out []helpRow
	for _, r := range p.rows {
		if strings.Contains(strings.ToLower(r.group), q) ||
			strings.Contains(strings.ToLower(r.label), q) ||
			strings.Contains(strings.ToLower(r.desc), q) ||
			strings.Contains(strings.ToLower(r.keys), q) {
			out = append(out, r)
		}
	}
	p.filtered = out
	p.offset = 0
}

// clampOffset keeps the scroll offset within the body's visible window.
func (p *HelpPanel) clampOffset(bodyLen, visible int64) {
	if p.offset > bodyLen-visible {
		p.offset = bodyLen - visible
	}
	if p.offset < 0 {
		p.offset = 0
	}
}

// buildBody renders every body line (group headers + entries) for the width.
func (p *HelpPanel) buildBody(width int64, rows []helpRow) []string {
	var keyCol int64
	for _, r := range rows {
		if w := core.VisibleWidth(r.keys); w > keyCol {
			keyCol = w
		}
	}
	if keyCol < 8 {
		keyCol = 8
	}
	if keyCol > width/2 {
		keyCol = width / 2
	}
	labelCol := width - keyCol - 3 // 3 = " • "
	if labelCol < 10 {
		labelCol = 10
	}

	pal := theme.CurrentPalette()
	var out []string
	lastGroup := ""
	for _, r := range rows {
		if r.group != lastGroup {
			if lastGroup != "" {
				out = append(out, "")
			}
			out = append(out, pal.Dim.Italic().Render(r.group))
			lastGroup = r.group
		}
		keys := core.PadToWidth(pal.User.Render(r.keys), keyCol)
		desc := r.desc
		if desc == "" {
			desc = r.label
		}
		out = append(out, keys+pal.Dim.Render(" • ")+core.TruncateToWidth(desc, labelCol, "…"))
	}
	return out
}

// buildHelpRows flattens the keybindings manager into sorted, grouped rows.
func buildHelpRows(km *terminal.KeybindingsManager) []helpRow {
	var rows []helpRow
	if km == nil {
		return rows
	}
	for id, keys := range km.All() {
		def := km.Definition(id)
		group, label := id, id
		if idx := strings.LastIndex(id, "."); idx >= 0 {
			group, label = id[:idx], id[idx+1:]
		}
		group = localizeGroup(group)
		desc := def.Description
		if key, ok := keybindingI18n[id]; ok {
			desc = i18n.T(key)
		}
		rows = append(rows, helpRow{
			group: group,
			id:    id,
			label: label,
			keys:  formatKeyList(keys),
			desc:  desc,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].group != rows[j].group {
			return rows[i].group < rows[j].group
		}
		return rows[i].id < rows[j].id
	})
	return rows
}

// localizeGroup renders a localized group header when a mapping exists,
// falling back to the raw group prefix otherwise.
func localizeGroup(group string) string {
	if key, ok := groupI18n[group]; ok {
		return i18n.T(key)
	}
	return group
}

// countBodyLines counts group headers + blank separators + entries.
func countBodyLines(rows []helpRow) int64 {
	var n int64
	lastGroup := ""
	for _, r := range rows {
		if r.group != lastGroup {
			if lastGroup != "" {
				n++
			}
			n++
			lastGroup = r.group
		}
		n++
	}
	return n
}

func formatKeyList(keys []terminal.KeyID) string {
	if len(keys) == 0 {
		return "—"
	}
	return strings.Join(keys, " / ")
}

// Ensure HelpPanel implements core.Component and core.Updatable.
var _ core.Component = (*HelpPanel)(nil)
var _ core.Updatable = (*HelpPanel)(nil)
