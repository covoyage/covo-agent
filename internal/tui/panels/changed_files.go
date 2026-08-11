package panels

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

// FileChange is the presentation DTO for changed files shown in the panel.
type FileChange struct {
	Path   string
	Action string
	Tool   string
}

type fileTreeNode struct {
	Name     string
	Path     string
	IsDir    bool
	Children []*fileTreeNode
	Action   string
	Tool     string
}

// ChangedFilesPanel is a TUI component that displays all files modified
// during the session as a collapsible tree.
type ChangedFilesPanel struct {
	mu         sync.RWMutex
	km         *terminal.KeybindingsManager
	entries    func() []FileChange
	roots      []*fileTreeNode
	flat       []*fileTreeNode
	expanded   map[string]bool
	selected   int
	entryCount int
	workingDir string
	onCancel   func()
	height     int
}

var _ core.Component = (*ChangedFilesPanel)(nil)
var _ core.Updatable = (*ChangedFilesPanel)(nil)

// NewChangedFilesPanel creates a panel displaying files changed in the session.
func NewChangedFilesPanel(entries func() []FileChange, workingDir string) *ChangedFilesPanel {
	km := terminal.NewKeybindingsManager(map[string]terminal.KeybindingDef{
		"tree.up":       {DefaultKeys: []terminal.KeyID{"up", "ctrl+p"}},
		"tree.down":     {DefaultKeys: []terminal.KeyID{"down", "ctrl+n"}},
		"tree.confirm":  {DefaultKeys: []terminal.KeyID{"enter"}},
		"tree.cancel":   {DefaultKeys: []terminal.KeyID{"esc"}},
		"tree.collapse": {DefaultKeys: []terminal.KeyID{"h", "left"}},
		"tree.expand":   {DefaultKeys: []terminal.KeyID{"l", "right"}},
		"tree.refresh":  {DefaultKeys: []terminal.KeyID{"r"}},
	})
	if entries == nil {
		entries = func() []FileChange { return nil }
	}
	p := &ChangedFilesPanel{
		km:         km,
		entries:    entries,
		workingDir: workingDir,
		expanded:   make(map[string]bool),
		height:     24,
	}
	p.refresh()
	return p
}

func (p *ChangedFilesPanel) SetOnCancel(fn func()) {
	p.mu.Lock()
	p.onCancel = fn
	p.mu.Unlock()
}

func (p *ChangedFilesPanel) Invalidate() {}

func (p *ChangedFilesPanel) Update(msg core.Msg) core.Cmd {
	if m, ok := msg.(core.KeyMsg); ok {
		p.processKeys(m.Data)
	}
	return nil
}

func (p *ChangedFilesPanel) processKeys(data string) {
	p.mu.RLock()
	km := p.km
	p.mu.RUnlock()

	switch {
	case km.Matches(data, "tree.up"):
		p.moveSelected(-1)
	case km.Matches(data, "tree.down"):
		p.moveSelected(1)
	case km.Matches(data, "tree.confirm"):
		p.confirm()
	case km.Matches(data, "tree.cancel"):
		p.cancel()
	case km.Matches(data, "tree.collapse"):
		p.toggleCollapse(false)
	case km.Matches(data, "tree.expand"):
		p.toggleCollapse(true)
	case km.Matches(data, "tree.refresh"):
		p.refresh()
	}
}

func (p *ChangedFilesPanel) moveSelected(delta int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.selected += delta
	if p.selected < 0 {
		p.selected = 0
	}
	if p.selected >= len(p.flat) {
		p.selected = len(p.flat) - 1
	}
}

func (p *ChangedFilesPanel) toggleCollapse(expand bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.selected < 0 || p.selected >= len(p.flat) {
		return
	}
	node := p.flat[p.selected]
	if len(node.Children) == 0 {
		return
	}
	if expand {
		p.expanded[node.Path] = true
	} else {
		p.expanded[node.Path] = false
	}
	p.flatten()
}

func (p *ChangedFilesPanel) confirm() {
	// No-op for now; future behavior may open diff/file.
}

func (p *ChangedFilesPanel) cancel() {
	p.mu.RLock()
	fn := p.onCancel
	p.mu.RUnlock()
	if fn != nil {
		go fn()
	}
}

func (p *ChangedFilesPanel) refresh() {
	entries := p.entries()
	roots := buildFileTree(entries, p.workingDir)
	expanded := make(map[string]bool)
	for _, r := range roots {
		expanded[r.Path] = true
		collectDirPaths(r, expanded)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.roots = roots
	p.expanded = expanded
	p.entryCount = len(entries)
	p.flatten()
	if p.selected >= len(p.flat) {
		p.selected = len(p.flat) - 1
	}
	if p.selected < 0 {
		p.selected = 0
	}
}

func collectDirPaths(node *fileTreeNode, expanded map[string]bool) {
	if node.IsDir {
		expanded[node.Path] = true
		for _, c := range node.Children {
			collectDirPaths(c, expanded)
		}
	}
}

func (p *ChangedFilesPanel) flatten() {
	p.flat = nil
	p.flattenNodes(p.roots)
}

func (p *ChangedFilesPanel) flattenNodes(nodes []*fileTreeNode) {
	for _, n := range nodes {
		p.flat = append(p.flat, n)
		if n.IsDir && p.expanded[n.Path] {
			p.flattenNodes(n.Children)
		}
	}
}

func (p *ChangedFilesPanel) Render(width int64) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if width < 1 {
		width = 1
	}
	pal := theme.CurrentPalette()

	var lines []string

	title := core.PadToWidth("📁 Changed Files — Session Modifications", width)
	lines = append(lines, pal.Accent.Render(title))
	lines = append(lines, pal.Dim.Render(strings.Repeat("─", int(width))))

	countLine := fmt.Sprintf("  %d file(s) modified in this session", p.entryCount)
	lines = append(lines, pal.Dim.Render(core.PadToWidth(countLine, width)))
	lines = append(lines, "")

	if len(p.flat) == 0 {
		msg := core.PadToWidth("  No files changed yet. File modifications will appear here.", width)
		lines = append(lines, pal.Dim.Render(msg))
	} else {
		maxVisible := int(p.calcHeight() - 8)
		if maxVisible < 4 {
			maxVisible = 4
		}
		start, end := VisibleRange(p.selected, maxVisible, len(p.flat))
		for i := start; i < end; i++ {
			node := p.flat[i]
			line := p.renderNode(node, width, pal)
			if i == p.selected {
				lines = append(lines, pal.SelectHighlight.Render(line))
			} else {
				lines = append(lines, line)
			}
		}
	}

	for int64(len(lines)) < p.calcHeight() {
		lines = append(lines, "")
	}

	if len(lines) > 0 {
		lines = append(lines, pal.Dim.Render(strings.Repeat("─", int(width))))
		footer := core.PadToWidth("  ↑↓ navigate  h/l collapse/expand  r refresh  Esc close", width)
		lines = append(lines, pal.Dim.Render(footer))
	}

	return lines
}

func (p *ChangedFilesPanel) renderNode(node *fileTreeNode, width int64, pal *theme.Palette) string {
	var buf strings.Builder

	if node.IsDir {
		if p.expanded[node.Path] {
			buf.WriteString("[-]")
		} else {
			buf.WriteString("[+]")
		}
	} else {
		buf.WriteString("   ")
	}

	if node.IsDir {
		buf.WriteString(" 📂")
	} else {
		switch node.Action {
		case "created":
			buf.WriteString(" ✨")
		case "deleted":
			buf.WriteString(" 🗑")
		default:
			buf.WriteString(" ✏️")
		}
	}

	buf.WriteString(" ")
	buf.WriteString(node.Name)

	if !node.IsDir && node.Action != "" {
		var tag string
		var style theme.Style
		switch node.Action {
		case "created":
			tag = "new"
			style = pal.Success
		case "deleted":
			tag = "deleted"
			style = pal.Error
		default:
			tag = "modified"
			style = pal.Accent
		}
		buf.WriteString(" ")
		buf.WriteString(style.Render("[" + tag + "]"))
	}

	result := core.PadToWidth(buf.String(), width)
	if int64(core.VisibleWidth(buf.String())) > width {
		result = core.PadToWidth(core.TruncateToWidth(buf.String(), width, "…"), width)
	}
	return result
}

func (p *ChangedFilesPanel) calcHeight() int64 {
	h := int64(p.height)
	if h < 10 {
		h = 24
	}
	return h
}

func buildFileTree(entries []FileChange, workingDir string) []*fileTreeNode {
	nodeMap := make(map[string]*fileTreeNode)

	relPath := func(p string) string {
		if workingDir != "" {
			if rel, err := filepath.Rel(workingDir, p); err == nil && !strings.HasPrefix(rel, "..") {
				return filepath.ToSlash(rel)
			}
		}
		return filepath.ToSlash(p)
	}

	for _, entry := range entries {
		rel := relPath(entry.Path)
		parts := strings.Split(rel, "/")

		currentPath := ""
		for i, part := range parts {
			if part == "" {
				continue
			}
			if i == len(parts)-1 {
				filePath := part
				if currentPath != "" {
					filePath = currentPath + "/" + part
				}
				nodeMap[filePath] = &fileTreeNode{
					Name:   part,
					Path:   filePath,
					IsDir:  false,
					Action: entry.Action,
					Tool:   entry.Tool,
				}
				parentKey := currentPath
				if parentKey == "" {
					parentKey = "."
				}
				if parent, ok := nodeMap[parentKey]; ok {
					appendUniqueChild(parent, nodeMap[filePath])
				}
			} else {
				if currentPath == "" {
					currentPath = part
				} else {
					currentPath = currentPath + "/" + part
				}
				if _, exists := nodeMap[currentPath]; !exists {
					nodeMap[currentPath] = &fileTreeNode{
						Name:  part,
						Path:  currentPath,
						IsDir: true,
					}
					parentKey := path.Dir(currentPath)
					if parentKey == "." || parentKey == "" {
						parentKey = "."
					}
					if parent, ok := nodeMap[parentKey]; ok && parentKey != "." {
						appendUniqueChild(parent, nodeMap[currentPath])
					}
				}
			}
		}
	}

	var roots []*fileTreeNode
	for path, node := range nodeMap {
		if !strings.Contains(path, "/") {
			roots = append(roots, node)
		}
	}

	sortRoots(roots)
	for _, node := range nodeMap {
		if node.IsDir {
			sortRoots(node.Children)
		}
	}

	return roots
}

func sortRoots(nodes []*fileTreeNode) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].IsDir != nodes[j].IsDir {
			return nodes[i].IsDir
		}
		return nodes[i].Name < nodes[j].Name
	})
}

func appendUniqueChild(parent, child *fileTreeNode) {
	for _, existing := range parent.Children {
		if existing.Path == child.Path {
			return
		}
	}
	parent.Children = append(parent.Children, child)
}
