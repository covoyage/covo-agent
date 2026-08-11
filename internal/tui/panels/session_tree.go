package panels

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	covosession "github.com/covoyage/covonaut/session"
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

type sessionTreeNode struct {
	Info     covosession.Info
	Children []*sessionTreeNode
	Parent   *sessionTreeNode
	Depth    int
	Expanded bool
}

type sessionTreeTheme struct {
	Title         string
	ItemStyle     theme.Style
	SelectedStyle theme.Style
	CurrentStyle  theme.Style
	DimStyle      theme.Style
	AccentStyle   theme.Style
}

type SessionTree struct {
	mu         sync.RWMutex
	km         *terminal.KeybindingsManager
	roots      []*sessionTreeNode
	allNodes   []*sessionTreeNode
	flat       []*sessionTreeNode
	selected   int
	filter     string
	filterMode bool
	onSelect   func(string)
	onCancel   func()
	height     int
	theme      sessionTreeTheme
	currentID  string
}

func NewSessionTree() *SessionTree {
	km := terminal.NewKeybindingsManager(map[string]terminal.KeybindingDef{
		"tree.up":       {DefaultKeys: []terminal.KeyID{"up", "ctrl+p"}},
		"tree.down":     {DefaultKeys: []terminal.KeyID{"down", "ctrl+n"}},
		"tree.confirm":  {DefaultKeys: []terminal.KeyID{"enter"}},
		"tree.cancel":   {DefaultKeys: []terminal.KeyID{"esc"}},
		"tree.collapse": {DefaultKeys: []terminal.KeyID{"h", "left"}},
		"tree.expand":   {DefaultKeys: []terminal.KeyID{"l", "right"}},
		"tree.filter":   {DefaultKeys: []terminal.KeyID{"/"}},
	})
	pal := theme.CurrentPalette()
	return &SessionTree{
		km:     km,
		height: 24,
		theme: sessionTreeTheme{
			Title:         "Session Tree",
			ItemStyle:     pal.Assistant,
			SelectedStyle: pal.SelectHighlight,
			CurrentStyle:  pal.Success,
			DimStyle:      pal.Dim,
			AccentStyle:   pal.Accent,
		},
	}
}

func (t *SessionTree) SetCurrentID(id string) {
	t.mu.Lock()
	t.currentID = id
	t.mu.Unlock()
}

func (t *SessionTree) SetItems(infos []covosession.Info) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.roots = buildSessionTree(infos)
	t.allNodes = nil
	t.flattenTree(&t.allNodes, t.roots)
	t.applyFilter()
	t.selected = 0
}

func (t *SessionTree) SetOnSelect(fn func(string)) {
	t.mu.Lock()
	t.onSelect = fn
	t.mu.Unlock()
}

func (t *SessionTree) SetOnCancel(fn func()) {
	t.mu.Lock()
	t.onCancel = fn
	t.mu.Unlock()
}

func (t *SessionTree) Invalidate() {}

func (t *SessionTree) Update(msg core.Msg) core.Cmd {
	switch m := msg.(type) {
	case core.KeyMsg:
		t.processKeys(m.Data)
	}
	return nil
}

func (t *SessionTree) processKeys(data string) {
	t.mu.RLock()
	km := t.km
	fm := t.filterMode
	t.mu.RUnlock()

	if fm {
		t.handleFilterInput(data)
		return
	}

	switch {
	case km.Matches(data, "tree.up"):
		t.moveSelected(-1)
	case km.Matches(data, "tree.down"):
		t.moveSelected(1)
	case km.Matches(data, "tree.confirm"):
		t.confirm()
	case km.Matches(data, "tree.cancel"):
		t.cancel()
	case km.Matches(data, "tree.collapse"):
		t.toggleCollapse(false)
	case km.Matches(data, "tree.expand"):
		t.toggleCollapse(true)
	case km.Matches(data, "tree.filter"):
		t.startFilter()
	}
}

func (t *SessionTree) handleFilterInput(data string) {
	if terminal.MatchesKey(data, "enter") {
		t.filterMode = false
		t.confirm()
		return
	}
	if terminal.MatchesKey(data, "escape") {
		t.mu.Lock()
		t.filterMode = false
		t.filter = ""
		t.applyFilter()
		t.mu.Unlock()
		return
	}
	if terminal.MatchesKey(data, "backspace") {
		t.mu.Lock()
		if len(t.filter) > 0 {
			t.filter = t.filter[:len(t.filter)-1]
			t.applyFilter()
		}
		t.mu.Unlock()
		return
	}
	if len(data) == 1 && data[0] >= 32 && data[0] < 127 {
		t.mu.Lock()
		t.filter += data
		t.applyFilter()
		t.mu.Unlock()
	}
}

func (t *SessionTree) startFilter() {
	t.mu.Lock()
	t.filterMode = true
	t.mu.Unlock()
}

func (t *SessionTree) moveSelected(delta int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.selected += delta
	if t.selected < 0 {
		t.selected = 0
	}
	if t.selected >= len(t.flat) {
		t.selected = len(t.flat) - 1
	}
}

func (t *SessionTree) toggleCollapse(expand bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.selected < 0 || t.selected >= len(t.flat) {
		return
	}
	node := t.flat[t.selected]
	if len(node.Children) == 0 {
		return
	}
	node.Expanded = expand
	t.allNodes = nil
	t.flattenTree(&t.allNodes, t.roots)
	t.applyFilter()
}

func (t *SessionTree) confirm() {
	t.mu.RLock()
	sel := t.selected
	nodes := t.flat
	fn := t.onSelect
	t.mu.RUnlock()
	if sel >= 0 && sel < len(nodes) && fn != nil {
		go fn(nodes[sel].Info.ID)
	}
}

func (t *SessionTree) cancel() {
	t.mu.RLock()
	fn := t.onCancel
	t.mu.RUnlock()
	if fn != nil {
		go fn()
	}
}

func (t *SessionTree) applyFilter() {
	if t.filter == "" {
		t.flat = make([]*sessionTreeNode, len(t.allNodes))
		copy(t.flat, t.allNodes)
	} else {
		lower := strings.ToLower(t.filter)
		t.flat = t.flat[:0]
		for _, n := range t.allNodes {
			if strings.Contains(strings.ToLower(n.Info.Name), lower) ||
				strings.Contains(strings.ToLower(n.Info.ID), lower) ||
				strings.Contains(strings.ToLower(n.Info.Label), lower) ||
				strings.Contains(strings.ToLower(n.Info.Summary), lower) {
				t.flat = append(t.flat, n)
			}
		}
	}
	if t.selected >= len(t.flat) {
		t.selected = len(t.flat) - 1
	}
	if t.selected < 0 && len(t.flat) > 0 {
		t.selected = 0
	}
}

func (t *SessionTree) Render(width int64) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if width < 1 {
		width = 1
	}

	var lines []string

	title := core.PadToWidth(t.theme.Title, width)
	lines = append(lines, t.theme.AccentStyle.Render(title))
	lines = append(lines, t.theme.DimStyle.Render(strings.Repeat("─", int(width))))

	if t.filterMode {
		searchText := t.filter
		if searchText == "" {
			searchText = "type to search..."
		}
		searchBar := core.PadToWidth("  / "+searchText+"█", width)
		lines = append(lines, t.theme.AccentStyle.Render(searchBar))
	} else if t.filter != "" {
		filterText := core.PadToWidth("  filter: "+t.filter, width)
		lines = append(lines, t.theme.DimStyle.Render(filterText))
	}

	countLine := fmt.Sprintf("  %d sessions", len(t.allNodes))
	if len(t.flat) != len(t.allNodes) {
		countLine = fmt.Sprintf("  %d of %d sessions", len(t.flat), len(t.allNodes))
	}
	countLine = core.PadToWidth(countLine, width)
	lines = append(lines, t.theme.DimStyle.Render(countLine))

	maxVisible := int(t.calcHeight() - 8)
	if maxVisible < 4 {
		maxVisible = 4
	}

	start, end := VisibleRange(t.selected, maxVisible, len(t.flat))
	for i := start; i < end; i++ {
		node := t.flat[i]
		prefix, conn := nodeConnector(node)
		name := node.Info.Name
		if name == "" {
			name = node.Info.ID
			if len(name) > 8 {
				name = name[:8]
			}
		}

		var buf string
		if node.Expanded {
			buf += "[-]"
		} else if len(node.Children) > 0 {
			buf += "[+]"
		} else {
			buf += "   "
		}
		buf += " " + prefix + conn + " " + name

		label := ""
		if node.Info.Label != "" {
			label = " [" + node.Info.Label + "]"
		}
		buf += label

		forkCount := len(node.Children)
		if forkCount > 0 {
			buf += fmt.Sprintf(" (%d forks)", forkCount)
		}

		full := core.PadToWidth(buf, width)
		if int64(core.VisibleWidth(buf)) > width {
			full = core.TruncateToWidth(buf, width, "…")
			full = core.PadToWidth(full, width)
		}

		if i == t.selected {
			lines = append(lines, t.theme.SelectedStyle.Render(full))
		} else if node.Info.ID == t.currentID {
			lines = append(lines, t.theme.CurrentStyle.Render(full))
		} else {
			lines = append(lines, t.theme.ItemStyle.Render(full))
		}

		if node.Info.Summary != "" {
			sm := node.Info.Summary
			smWidth := int64(core.VisibleWidth(sm))
			if smWidth > width-4 {
				sm = core.TruncateToWidth(sm, width-4, "…")
			}
			indent := "    " + prefix + "  "
			if node.Depth > 0 {
				indent = "    " + prefix + "  "
			}
			summaryLine := core.PadToWidth(indent+sm, width)
			lines = append(lines, t.theme.DimStyle.Render(summaryLine))
		}
	}

	if len(t.flat) == 0 {
		msg := core.PadToWidth("  No sessions found", width)
		lines = append(lines, t.theme.DimStyle.Render(msg))
	}

	for int64(len(lines)) < t.calcHeight() {
		lines = append(lines, "")
	}

	if len(lines) > 0 {
		lines = append(lines, t.theme.DimStyle.Render(strings.Repeat("─", int(width))))
		footerText := core.PadToWidth("/ search  ↑↓ nav  h/l coll/exp  Enter resume  Esc close", width)
		lines = append(lines, t.theme.DimStyle.Render("  "+footerText))
	}

	return lines
}

func (t *SessionTree) calcHeight() int64 {
	h := int64(t.height)
	if h < 10 {
		h = 24
	}
	return h
}

// VisibleRange returns a centered viewport around the selected list item.
func VisibleRange(selected, visible, total int) (int, int) {
	if total <= visible {
		return 0, total
	}
	half := visible / 2
	start := selected - half
	if start < 0 {
		start = 0
	}
	end := start + visible
	if end > total {
		end = total
		start = end - visible
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

func buildSessionTree(infos []covosession.Info) []*sessionTreeNode {
	nodeMap := make(map[string]*sessionTreeNode, len(infos))
	for _, info := range infos {
		nodeMap[info.ID] = &sessionTreeNode{
			Info:     info,
			Children: nil,
			Depth:    0,
			Expanded: true,
		}
	}

	var roots []*sessionTreeNode
	for _, info := range infos {
		node := nodeMap[info.ID]
		if info.ParentSession == "" {
			roots = append(roots, node)
			continue
		}
		parent, ok := nodeMap[info.ParentSession]
		if !ok {
			roots = append(roots, node)
			continue
		}
		parent.Children = append(parent.Children, node)
		node.Parent = parent
	}

	for _, n := range nodeMap {
		sort.Slice(n.Children, func(i, j int) bool {
			return n.Children[i].Info.UpdatedAt.After(n.Children[j].Info.UpdatedAt)
		})
	}

	var setDepth func(node *sessionTreeNode, depth int)
	setDepth = func(node *sessionTreeNode, depth int) {
		node.Depth = depth
		for _, child := range node.Children {
			setDepth(child, depth+1)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].Info.UpdatedAt.After(roots[j].Info.UpdatedAt)
	})
	for _, root := range roots {
		setDepth(root, 0)
	}

	return roots
}

func (t *SessionTree) flattenTree(result *[]*sessionTreeNode, nodes []*sessionTreeNode) {
	for _, n := range nodes {
		*result = append(*result, n)
		if n.Expanded {
			t.flattenTree(result, n.Children)
		}
	}
}

func hasMoreSiblings(n *sessionTreeNode) bool {
	if n.Parent == nil {
		return false
	}
	for i, s := range n.Parent.Children {
		if s == n {
			return i < len(n.Parent.Children)-1
		}
	}
	return false
}

func nodeConnector(n *sessionTreeNode) (indent string, connector string) {
	if n.Depth == 0 {
		return "", ""
	}

	var levels []bool
	for p := n.Parent; p != nil && p.Parent != nil; p = p.Parent {
		levels = append(levels, hasMoreSiblings(p))
	}

	var parts []string
	for i := len(levels) - 1; i >= 0; i-- {
		if levels[i] {
			parts = append(parts, "│ ")
		} else {
			parts = append(parts, "  ")
		}
	}

	indent = strings.Join(parts, "")
	if hasMoreSiblings(n) {
		connector = "├─"
	} else {
		connector = "└─"
	}
	return indent, connector
}

var _ core.Component = (*SessionTree)(nil)
var _ core.Updatable = (*SessionTree)(nil)
