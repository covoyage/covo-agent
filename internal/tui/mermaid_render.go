package tui

import (
	"fmt"
	"strings"
)

// MermaidRenderer converts simple Mermaid flowchart syntax into an ASCII/Unicode
// diagram that can be displayed directly in the terminal. This is a lightweight
// renderer — it supports the most common flowchart constructs (graph TD/LR,
// nodes, edges, subgraphs) and falls back gracefully for unsupported syntax.
type MermaidRenderer struct {
	direction string // "TD" (top-down) or "LR" (left-right)
	nodes     []mermaidNode
	edges     []mermaidEdge
	subgraphs []mermaidSubgraph
}

type mermaidNode struct {
	id    string
	text  string
	shape string // "rect", "round", "diamond", "stadium", "cyl"
}

type mermaidEdge struct {
	from  string
	to    string
	label string
	style string // "-->", "---", "==>", "-.-"
}

type mermaidSubgraph struct {
	title string
	nodes []string
}

// RenderMermaid parses Mermaid flowchart or sequence-diagram text and
// returns an ASCII representation.
func RenderMermaid(input string) string {
	if isSequenceDiagram(input) {
		return renderSequenceDiagram(input)
	}
	if isPieChart(input) {
		return renderPieChart(input)
	}
	if isClassDiagram(input) {
		return renderClassDiagram(input)
	}
	if isStateDiagram(input) {
		return renderStateDiagram(input)
	}
	if isERDiagram(input) {
		return renderERDiagram(input)
	}
	r := &MermaidRenderer{}
	if err := r.parse(input); err != nil {
		return fmt.Sprintf("(mermaid parse error: %v)\n\n%s", err, input)
	}
	return r.render()
}

// parse reads Mermaid flowchart syntax.
func (r *MermaidRenderer) parse(input string) error {
	lines := strings.Split(input, "\n")
	currentSG := -1

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") || strings.HasPrefix(line, "%%{") {
			continue
		}

		lower := strings.ToLower(line)

		// Direction declaration. Compact one-liners are allowed:
		// `graph TD; A[Start] --> B[End]`
		if strings.HasPrefix(lower, "graph ") || strings.HasPrefix(lower, "flowchart ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				dir := parts[1]
				if i := strings.Index(dir, ";"); i >= 0 {
					dir = dir[:i]
				}
				r.direction = strings.ToUpper(dir)
			}
			if idx := strings.Index(line, ";"); idx >= 0 {
				rest := strings.TrimSpace(line[idx+1:])
				if rest != "" {
					r.parseLine(rest, currentSG)
				}
			}
			continue
		}

		// Subgraph
		if strings.HasPrefix(lower, "subgraph ") {
			title := mermaidSubgraphTitle(strings.TrimSpace(line[len("subgraph "):]))
			r.subgraphs = append(r.subgraphs, mermaidSubgraph{title: title})
			currentSG = len(r.subgraphs) - 1
			continue
		}
		if lower == "end" {
			currentSG = -1
			continue
		}

		// Try to parse as edge or node definition
		r.parseLine(line, currentSG)
	}

	if r.direction == "" {
		r.direction = "TD"
	}
	return nil
}

func mermaidSubgraphTitle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`)
	if i := strings.Index(s, "["); i >= 0 && strings.HasSuffix(s, "]") {
		inner := strings.TrimSpace(s[i+1 : len(s)-1])
		inner = strings.Trim(inner, `"'`)
		if inner != "" {
			return inner
		}
	}
	return s
}

func (r *MermaidRenderer) parseLine(line string, sg int) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if i := strings.Index(line, ";"); i >= 0 {
		r.parseLine(line[:i], sg)
		r.parseLine(line[i+1:], sg)
		return
	}

	// Try edge patterns first (they contain -->, ---, ==>, -.-)
	edgePatterns := []string{"-->", "==>", "===", "-.-", "---", "->>", "-->>"}
	for _, ep := range edgePatterns {
		if idx := strings.Index(line, ep); idx > 0 {
			rest := line[idx+len(ep):]
			left := strings.TrimSpace(line[:idx])
			label := ""
			if strings.HasPrefix(strings.TrimSpace(rest), "|") {
				rest = strings.TrimSpace(rest)
				end := strings.Index(rest[1:], "|")
				if end >= 0 {
					label = strings.TrimSpace(rest[1 : 1+end])
					rest = strings.TrimSpace(rest[1+end+1:])
				}
			}

			rightNode := strings.TrimSpace(rest)
			next := ""
			for _, ep2 := range edgePatterns {
				if idx2 := strings.Index(rightNode, ep2); idx2 > 0 {
					next = strings.TrimSpace(rightNode[idx2:])
					rightNode = strings.TrimSpace(rightNode[:idx2])
					break
				}
			}

			leftID, leftText, leftShape := parseNodeDef(left)
			rightID, rightText, rightShape := parseNodeDef(rightNode)

			r.addNode(leftID, leftText, leftShape)
			r.addNode(rightID, rightText, rightShape)
			r.edges = append(r.edges, mermaidEdge{
				from: leftID, to: rightID, label: label, style: ep,
			})

			if sg >= 0 && sg < len(r.subgraphs) {
				r.subgraphs[sg].nodes = append(r.subgraphs[sg].nodes, leftID, rightID)
			}
			if next != "" {
				r.parseLine(rightID+next, sg)
			}
			return
		}
	}

	// Standalone node definition
	id, text, shape := parseNodeDef(line)
	if id != "" {
		r.addNode(id, text, shape)
		if sg >= 0 && sg < len(r.subgraphs) {
			r.subgraphs[sg].nodes = append(r.subgraphs[sg].nodes, id)
		}
	}
}

// parseNodeDef parses a node definition like "A[Label]" or "B{Decision}" or just "A".
func parseNodeDef(s string) (id, text, shape string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", ""
	}

	// Check for shaped node: ID[text], ID(text), ID{text}, ID([text]), ID[(text)]
	// Multi-char patterns must be checked before single-char ones
	for _, sp := range []struct{ open, close, shape string }{
		{"([", "])", "stadium"},
		{"[(", ")]", "cyl"},
		{"[", "]", "rect"},
		{"(", ")", "round"},
		{"{", "}", "diamond"},
	} {
		if idx := strings.Index(s, sp.open); idx >= 0 && strings.HasSuffix(s, sp.close) {
			id = strings.TrimSpace(s[:idx])
			text = strings.TrimSpace(s[idx+len(sp.open) : len(s)-len(sp.close)])
			if id == "" {
				id = text
			}
			return id, text, sp.shape
		}
	}

	// Plain ID (no shape)
	return s, s, "rect"
}

func (r *MermaidRenderer) addNode(id, text, shape string) {
	for i := range r.nodes {
		if r.nodes[i].id == id {
			if text != id && text != "" {
				r.nodes[i].text = text
			}
			if shape != "rect" {
				r.nodes[i].shape = shape
			}
			return
		}
	}
	if text == "" {
		text = id
	}
	r.nodes = append(r.nodes, mermaidNode{id: id, text: text, shape: shape})
}

// render produces the ASCII diagram.
func (r *MermaidRenderer) render() string {
	if len(r.nodes) == 0 {
		return "(empty diagram)"
	}

	// Build adjacency for layout
	edges := make(map[string][]string)
	for _, e := range r.edges {
		edges[e.from] = append(edges[e.from], e.to)
	}

	// Simple layered layout
	layers := r.computeLayers()
	if len(layers) == 0 {
		// Fallback: all nodes in one row
		layers = [][]string{}
		placed := make(map[string]bool)
		for _, n := range r.nodes {
			if !placed[n.id] {
				layers = append(layers, []string{n.id})
				placed[n.id] = true
			}
		}
	}

	// Render each layer
	var b strings.Builder
	nodeMap := make(map[string]mermaidNode)
	for _, n := range r.nodes {
		nodeMap[n.id] = n
	}

	maxWidth := 0
	renderedLayers := make([][]string, len(layers))
	for i, layer := range layers {
		renderedLayers[i] = make([]string, len(layer))
		for j, id := range layer {
			renderedLayers[i][j] = renderBox(nodeMap[id])
			if len(renderedLayers[i][j]) > maxWidth {
				maxWidth = len(renderedLayers[i][j])
			}
		}
	}

	if r.direction == "LR" {
		r.renderLR(&b, renderedLayers, layers, edges, nodeMap)
	} else {
		r.renderTD(&b, renderedLayers, layers, edges, nodeMap)
	}
	r.renderSubgraphs(&b, nodeMap)
	return b.String()
}

func (r *MermaidRenderer) renderSubgraphs(b *strings.Builder, nodeMap map[string]mermaidNode) {
	if len(r.subgraphs) == 0 {
		return
	}
	for _, sg := range r.subgraphs {
		title := strings.TrimSpace(sg.title)
		if title == "" {
			title = "subgraph"
		}
		seen := map[string]bool{}
		var members []string
		for _, id := range sg.nodes {
			if seen[id] {
				continue
			}
			seen[id] = true
			label := id
			if n, ok := nodeMap[id]; ok && n.text != "" {
				label = n.text
			}
			members = append(members, label)
		}
		if len(members) == 0 {
			continue
		}
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
		fmt.Fprintf(b, "┌ %s\n", title)
		for _, m := range members {
			fmt.Fprintf(b, "│ %s\n", m)
		}
		b.WriteString("└\n")
	}
}

// computeLayers does a simple topological sort to determine vertical layers.
func (r *MermaidRenderer) computeLayers() [][]string {
	inDegree := make(map[string]int)
	adj := make(map[string][]string)
	allNodes := make(map[string]bool)

	for _, n := range r.nodes {
		allNodes[n.id] = true
		inDegree[n.id] = 0
	}
	for _, e := range r.edges {
		if allNodes[e.from] && allNodes[e.to] {
			adj[e.from] = append(adj[e.from], e.to)
			inDegree[e.to]++
		}
	}

	var layers [][]string
	remaining := make(map[string]bool)
	for k := range allNodes {
		remaining[k] = true
	}

	for len(remaining) > 0 {
		var layer []string
		for node := range remaining {
			if inDegree[node] == 0 {
				layer = append(layer, node)
			}
		}
		if len(layer) == 0 {
			// Cycle — just add remaining nodes
			for node := range remaining {
				layer = append(layer, node)
			}
		}
		layers = append(layers, layer)
		for _, n := range layer {
			delete(remaining, n)
			for _, m := range adj[n] {
				if inDegree[m] > 0 {
					inDegree[m]--
				}
			}
		}
	}

	return layers
}

func (r *MermaidRenderer) renderTD(b *strings.Builder, renderedLayers [][]string, layers [][]string, edges map[string][]string, nodeMap map[string]mermaidNode) {
	for i, layer := range renderedLayers {
		// Render nodes side by side
		lines := make([]string, 3)
		for _, box := range layer {
			boxLines := strings.Split(box, "\n")
			for k := 0; k < 3 && k < len(boxLines); k++ {
				lines[k] += boxLines[k] + "   "
			}
		}
		for _, l := range lines {
			b.WriteString(l)
			b.WriteString("\n")
		}

		// Render edges to next layer
		if i < len(renderedLayers)-1 {
			b.WriteString("  │\n")
			// Edge labels
			for _, id := range layers[i] {
				for _, target := range edges[id] {
					for _, nextLayer := range layers[i+1:] {
						for _, nid := range nextLayer {
							if nid == target {
								edge := r.findEdge(id, target)
								if edge != nil && edge.label != "" {
									b.WriteString(fmt.Sprintf("  │ %s\n", edge.label))
								}
								break
							}
						}
					}
				}
			}
			b.WriteString("  ▼\n")
		}
	}
}

func (r *MermaidRenderer) renderLR(b *strings.Builder, renderedLayers [][]string, layers [][]string, edges map[string][]string, nodeMap map[string]mermaidNode) {
	maxBoxes := 0
	for _, l := range renderedLayers {
		if len(l) > maxBoxes {
			maxBoxes = len(l)
		}
	}

	colWidth := 14
	for _, layer := range renderedLayers {
		for _, box := range layer {
			if len(box) > colWidth {
				colWidth = len(box)
			}
		}
	}

	for row := 0; row < maxBoxes; row++ {
		var line strings.Builder
		for col, layer := range renderedLayers {
			if row < len(layer) {
				box := layer[row]
				line.WriteString(padString(box, colWidth))
			} else {
				line.WriteString(strings.Repeat(" ", colWidth))
			}
			if col < len(renderedLayers)-1 {
				if row < len(layer) {
					line.WriteString(" ──▶ ")
				} else {
					line.WriteString("     ")
				}
			}
		}
		b.WriteString(line.String())
		b.WriteByte('\n')
	}
}

func (r *MermaidRenderer) findEdge(from, to string) *mermaidEdge {
	for i := range r.edges {
		if r.edges[i].from == from && r.edges[i].to == to {
			return &r.edges[i]
		}
	}
	return nil
}

func renderBox(n mermaidNode) string {
	text := n.text
	runes := []rune(text)
	const maxLabel = 24
	if len(runes) > maxLabel {
		text = string(runes[:maxLabel-1]) + "…"
		runes = []rune(text)
	}
	width := len(runes) + 2
	if width < 6 {
		width = 6
	}

	switch n.shape {
	case "diamond":
		return fmt.Sprintf("   ◇%s◇\n   %s\n   ◇%s◇", strings.Repeat("─", width-2), mermaidCenterText(text, width), strings.Repeat("─", width-2))
	case "round":
		return fmt.Sprintf("  ╭%s╮\n  │%s│\n  ╰%s╯", strings.Repeat("─", width-2), mermaidCenterText(text, width), strings.Repeat("─", width-2))
	case "stadium":
		return fmt.Sprintf("  (%s)\n  │%s│\n  (%s)", strings.Repeat("─", width-2), mermaidCenterText(text, width), strings.Repeat("─", width-2))
	case "cyl":
		return fmt.Sprintf("  ╔%s╗\n  ║%s║\n  ╚%s╝", strings.Repeat("═", width-2), mermaidCenterText(text, width), strings.Repeat("═", width-2))
	default:
		return fmt.Sprintf("  ┌%s┐\n  │%s│\n  └%s┘", strings.Repeat("─", width-2), mermaidCenterText(text, width), strings.Repeat("─", width-2))
	}
}

func mermaidCenterText(text string, width int) string {
	runes := []rune(text)
	if len(runes) >= width {
		return string(runes[:width])
	}
	left := (width - len(runes)) / 2
	right := width - len(runes) - left
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
}

func padString(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func isSequenceDiagram(input string) bool {
	for _, raw := range strings.Split(input, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "sequencediagram") {
			return true
		}
		return false
	}
	return false
}

type seqMsg struct {
	from, to, text string
	dashed, note   bool
}

func renderSequenceDiagram(input string) string {
	var actors []string
	seen := map[string]bool{}
	alias := map[string]string{}
	var msgs []seqMsg
	addActor := func(name string) {
		name = strings.TrimSpace(name)
		if mapped, ok := alias[name]; ok {
			name = mapped
		}
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		actors = append(actors, name)
	}
	resolve := func(name string) string {
		name = strings.TrimSpace(name)
		if mapped, ok := alias[name]; ok {
			return mapped
		}
		return name
	}

	for _, raw := range strings.Split(input, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "sequencediagram") {
			continue
		}
		if strings.HasPrefix(lower, "title ") || strings.HasPrefix(lower, "loop ") || strings.HasPrefix(lower, "alt ") || strings.HasPrefix(lower, "else") || strings.HasPrefix(lower, "opt ") || lower == "end" {
			continue
		}
		if strings.HasPrefix(lower, "note ") {
			rest := strings.TrimSpace(line[5:])
			colon := strings.IndexByte(rest, ':')
			if colon < 0 {
				continue
			}
			whereRaw := strings.TrimSpace(rest[:colon])
			where := strings.ToLower(whereRaw)
			text := strings.TrimSpace(rest[colon+1:])
			actorsPart := whereRaw
			for _, p := range []string{"left of ", "right of ", "over "} {
				if strings.HasPrefix(where, p) {
					actorsPart = strings.TrimSpace(whereRaw[len(p):])
					break
				}
			}
			parts := strings.Split(actorsPart, ",")
			from, to := "", ""
			if len(parts) >= 1 {
				from = resolve(strings.TrimSpace(parts[0]))
			}
			if len(parts) >= 2 {
				to = resolve(strings.TrimSpace(parts[1]))
			} else {
				to = from
			}
			addActor(from)
			addActor(to)
			msgs = append(msgs, seqMsg{from: from, to: to, text: text, note: true})
			continue
		}
		if strings.HasPrefix(lower, "participant ") || strings.HasPrefix(lower, "actor ") {
			rest := strings.TrimSpace(line[strings.IndexByte(line, ' ')+1:])
			name := rest
			id := rest
			if i := strings.Index(strings.ToLower(rest), " as "); i >= 0 {
				id = strings.TrimSpace(rest[:i])
				as := strings.TrimSpace(rest[i+4:])
				if as != "" {
					name = as
				}
			}
			if id != "" && name != "" && id != name {
				alias[id] = name
			}
			addActor(name)
			continue
		}
		for _, a := range []string{"-->>", "->>", "-->", "->"} {
			idx := strings.Index(line, a)
			if idx <= 0 {
				continue
			}
			rest := strings.TrimSpace(line[idx+len(a):])
			if strings.HasPrefix(rest, "+") || strings.HasPrefix(rest, "-") {
				rest = strings.TrimSpace(rest[1:])
			}
			if !strings.Contains(rest, ":") {
				continue
			}
			left := resolve(strings.TrimSpace(line[:idx]))
			c := strings.IndexByte(rest, ':')
			to := resolve(strings.TrimSpace(rest[:c]))
			text := strings.TrimSpace(rest[c+1:])
			addActor(left)
			addActor(to)
			msgs = append(msgs, seqMsg{
				from:   left,
				to:     to,
				text:   text,
				dashed: strings.HasPrefix(a, "--"),
			})
			break
		}
	}

	if len(actors) == 0 {
		return "(empty diagram)"
	}

	colW := 14
	for _, a := range actors {
		if w := len(a) + 4; w > colW {
			colW = w
		}
	}
	if colW > 24 {
		colW = 24
	}

	idxOf := map[string]int{}
	for i, a := range actors {
		idxOf[a] = i
	}

	var b strings.Builder
	for i, a := range actors {
		if len(a) > colW-2 {
			a = a[:colW-3] + "…"
		}
		b.WriteString(padString(" "+a+" ", colW))
		if i < len(actors)-1 {
			b.WriteString(" ")
		}
	}
	b.WriteByte('\n')
	for i := range actors {
		b.WriteString(padString(" │", colW))
		if i < len(actors)-1 {
			b.WriteString(" ")
		}
	}
	b.WriteByte('\n')

	for _, m := range msgs {
		from, okF := idxOf[m.from]
		to, okT := idxOf[m.to]
		if !okF || !okT {
			continue
		}
		if m.note {
			label := m.text
			if label != "" {
				if len(label) > colW*2 {
					label = label[:colW*2-1] + "…"
				}
				start := from
				if to < from {
					start = to
				}
				b.WriteString(strings.Repeat(" ", start*(colW+1)))
				b.WriteString("✎ ")
				b.WriteString(label)
				b.WriteByte('\n')
			}
			continue
		}
		if m.text != "" {
			label := m.text
			if len(label) > colW*2 {
				label = label[:colW*2-1] + "…"
			}
			start := from
			if to < from {
				start = to
			}
			pad := start * (colW + 1)
			b.WriteString(strings.Repeat(" ", pad))
			b.WriteString(label)
			b.WriteByte('\n')
		}
		line := make([]rune, len(actors)*(colW+1))
		for i := range line {
			line[i] = ' '
		}
		for i := range actors {
			pos := i*(colW+1) + 1
			if pos < len(line) {
				line[pos] = '│'
			}
		}
		left, right := from, to
		if right < left {
			left, right = right, left
		}
		start := left*(colW+1) + 1
		end := right*(colW+1) + 1
		if end >= len(line) {
			end = len(line) - 1
		}
		fill := '─'
		if m.dashed {
			fill = '╌'
		}
		for i := start + 1; i < end; i++ {
			line[i] = fill
		}
		if to >= from {
			line[end] = '▶'
			line[start] = '│'
		} else {
			line[start] = '◀'
			line[end] = '│'
		}
		b.WriteString(string(line))
		b.WriteByte('\n')
	}

	return b.String()
}

func isPieChart(input string) bool {
	for _, raw := range strings.Split(input, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		lower := strings.ToLower(line)
		return strings.HasPrefix(lower, "pie")
	}
	return false
}

type pieSlice struct {
	label string
	value float64
}

func renderPieChart(input string) string {
	var slices []pieSlice
	title := ""
	for _, raw := range strings.Split(input, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "pie") {
			if i := strings.Index(lower, "title "); i >= 0 {
				title = strings.TrimSpace(line[i+6:])
			}
			continue
		}
		if strings.HasPrefix(lower, "title ") {
			title = strings.TrimSpace(line[6:])
			continue
		}
		label, val, ok := parsePieSlice(line)
		if !ok {
			continue
		}
		slices = append(slices, pieSlice{label: label, value: val})
	}
	if len(slices) == 0 {
		return "(empty diagram)"
	}
	var total float64
	maxLabel := 0
	for _, s := range slices {
		total += s.value
		if n := len([]rune(s.label)); n > maxLabel {
			maxLabel = n
		}
	}
	if maxLabel > 18 {
		maxLabel = 18
	}
	barW := 20
	var b strings.Builder
	if title != "" {
		b.WriteString(title)
		b.WriteByte('\n')
	}
	for _, s := range slices {
		label := s.label
		runes := []rune(label)
		if len(runes) > maxLabel {
			label = string(runes[:maxLabel-1]) + "…"
		}
		pct := 0.0
		if total > 0 {
			pct = s.value / total * 100
		}
		filled := int(pct/100*float64(barW) + 0.5)
		if filled < 0 {
			filled = 0
		}
		if filled > barW {
			filled = barW
		}
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barW-filled)
		fmt.Fprintf(&b, "%-*s %s %5.1f%%\n", maxLabel, label, bar, pct)
	}
	return b.String()
}

func parsePieSlice(line string) (string, float64, bool) {
	colon := strings.LastIndexByte(line, ':')
	if colon < 0 {
		return "", 0, false
	}
	label := strings.TrimSpace(line[:colon])
	valStr := strings.TrimSpace(line[colon+1:])
	label = strings.Trim(label, `"'`)
	if label == "" || valStr == "" {
		return "", 0, false
	}
	var v float64
	if _, err := fmt.Sscanf(valStr, "%f", &v); err != nil {
		return "", 0, false
	}
	return label, v, true
}

func isClassDiagram(input string) bool {
	for _, raw := range strings.Split(input, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		return strings.HasPrefix(strings.ToLower(line), "classdiagram")
	}
	return false
}

type classInfo struct {
	name    string
	members []string
}

type classRel struct {
	from, to, kind string
}

func renderClassDiagram(input string) string {
	var classes []classInfo
	index := map[string]int{}
	var rels []classRel
	ensure := func(name string) int {
		name = strings.TrimSpace(name)
		if name == "" {
			return -1
		}
		if i, ok := index[name]; ok {
			return i
		}
		index[name] = len(classes)
		classes = append(classes, classInfo{name: name})
		return index[name]
	}

	current := -1
	inBody := false
	for _, raw := range strings.Split(input, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "classdiagram") {
			continue
		}
		if inBody {
			if line == "}" {
				inBody = false
				current = -1
				continue
			}
			if current >= 0 {
				classes[current].members = append(classes[current].members, line)
			}
			continue
		}
		if strings.HasPrefix(lower, "class ") {
			rest := strings.TrimSpace(line[5:])
			open := strings.HasSuffix(rest, "{")
			name := rest
			if open {
				name = strings.TrimSpace(rest[:len(rest)-1])
			}
			if i := strings.IndexAny(name, "[("); i >= 0 {
				name = strings.TrimSpace(name[:i])
			}
			current = ensure(name)
			inBody = open
			continue
		}
		if i := strings.Index(line, " : "); i >= 0 {
			name := strings.TrimSpace(line[:i])
			member := strings.TrimSpace(line[i+3:])
			idx := ensure(name)
			if idx >= 0 && member != "" {
				classes[idx].members = append(classes[idx].members, member)
			}
			continue
		}
		for _, kind := range []string{"<|--", "--|>", "<|..", "..|>", "*--", "--*", "o--", "--o", "<--", "-->", "<..", "..>", "..", "--"} {
			idx := strings.Index(line, kind)
			if idx <= 0 {
				continue
			}
			from := strings.TrimSpace(line[:idx])
			to := strings.TrimSpace(line[idx+len(kind):])
			if j := strings.IndexByte(to, ':'); j >= 0 {
				to = strings.TrimSpace(to[:j])
			}
			if from == "" || to == "" {
				continue
			}
			ensure(from)
			ensure(to)
			rels = append(rels, classRel{from: from, to: to, kind: kind})
			break
		}
	}

	if len(classes) == 0 {
		return "(empty diagram)"
	}

	var b strings.Builder
	for i, c := range classes {
		name := c.name
		maxW := len([]rune(name))
		for _, m := range c.members {
			if n := len([]rune(m)); n > maxW {
				maxW = n
			}
		}
		if maxW < 8 {
			maxW = 8
		}
		if maxW > 28 {
			maxW = 28
		}
		clip := func(s string) string {
			runes := []rune(s)
			if len(runes) > maxW {
				return string(runes[:maxW-1]) + "…"
			}
			return s
		}
		fmt.Fprintf(&b, "┌─%s┐\n", strings.Repeat("─", maxW))
		fmt.Fprintf(&b, "│ %-*s│\n", maxW, clip(name))
		if len(c.members) > 0 {
			fmt.Fprintf(&b, "├─%s┤\n", strings.Repeat("─", maxW))
			for _, m := range c.members {
				fmt.Fprintf(&b, "│ %-*s│\n", maxW, clip(m))
			}
		}
		fmt.Fprintf(&b, "└─%s┘\n", strings.Repeat("─", maxW))
		if i < len(classes)-1 {
			b.WriteByte('\n')
		}
	}
	if len(rels) > 0 {
		b.WriteByte('\n')
		for _, r := range rels {
			label := classRelLabel(r.kind)
			fmt.Fprintf(&b, "%s %s %s\n", r.from, label, r.to)
		}
	}
	return b.String()
}

func classRelLabel(kind string) string {
	switch kind {
	case "<|--", "--|>":
		return "◁──"
	case "<|..", "..|>":
		return "◁╌╌"
	case "*--", "--*":
		return "◆──"
	case "o--", "--o":
		return "◇──"
	case "-->", "<--":
		return "──▶"
	case "..>", "<..":
		return "╌╌▶"
	case "..":
		return "╌╌"
	default:
		return "──"
	}
}

func mermaidKindPrefix(input string, kinds ...string) bool {
	for _, raw := range strings.Split(input, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		lower := strings.ToLower(line)
		for _, k := range kinds {
			if strings.HasPrefix(lower, k) {
				return true
			}
		}
		return false
	}
	return false
}

func isStateDiagram(input string) bool {
	return mermaidKindPrefix(input, "statediagram-v2", "statediagram")
}

func isERDiagram(input string) bool {
	return mermaidKindPrefix(input, "erdiagram")
}

func renderStateDiagram(input string) string {
	type stateRel struct{ from, to, label string }
	var states []string
	seen := map[string]bool{}
	var rels []stateRel
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || name == "[*]" || seen[name] {
			return
		}
		seen[name] = true
		states = append(states, name)
	}
	for _, raw := range strings.Split(input, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "statediagram") || strings.HasPrefix(lower, "state ") || lower == "end" || strings.HasPrefix(lower, "note ") {
			continue
		}
		idx := strings.Index(line, "-->")
		if idx <= 0 {
			continue
		}
		from := strings.TrimSpace(line[:idx])
		rest := strings.TrimSpace(line[idx+3:])
		to, label := rest, ""
		if i := strings.IndexByte(rest, ':'); i >= 0 {
			to = strings.TrimSpace(rest[:i])
			label = strings.TrimSpace(rest[i+1:])
		}
		add(from)
		add(to)
		rels = append(rels, stateRel{from: from, to: to, label: label})
	}
	if len(states) == 0 && len(rels) == 0 {
		return "(empty diagram)"
	}
	var b strings.Builder
	for i, s := range states {
		if i > 0 {
			b.WriteString("  │\n  ▼\n")
		}
		w := len([]rune(s)) + 2
		if w < 8 {
			w = 8
		}
		if w > 24 {
			w = 24
			runes := []rune(s)
			if len(runes) > 22 {
				s = string(runes[:21]) + "…"
			}
		}
		fmt.Fprintf(&b, "  ╭%s╮\n", strings.Repeat("─", w))
		fmt.Fprintf(&b, "  │ %-*s│\n", w-1, s)
		fmt.Fprintf(&b, "  ╰%s╯\n", strings.Repeat("─", w))
	}
	for _, r := range rels {
		from, to := r.from, r.to
		if from == "[*]" {
			from = "●"
		}
		if to == "[*]" {
			to = "◉"
		}
		if r.label != "" {
			fmt.Fprintf(&b, "%s ──▶ %s : %s\n", from, to, r.label)
		} else {
			fmt.Fprintf(&b, "%s ──▶ %s\n", from, to)
		}
	}
	return b.String()
}

func renderERDiagram(input string) string {
	type erRel struct{ left, right, card, label string }
	var entities []string
	seen := map[string]bool{}
	attrs := map[string][]string{}
	var rels []erRel
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		entities = append(entities, name)
	}
	current := ""
	inBody := false
	for _, raw := range strings.Split(input, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "erdiagram") {
			continue
		}
		if inBody {
			if line == "}" {
				inBody = false
				current = ""
				continue
			}
			if current != "" {
				attrs[current] = append(attrs[current], line)
			}
			continue
		}
		if strings.HasSuffix(line, "{") {
			name := strings.TrimSpace(strings.TrimSuffix(line, "{"))
			add(name)
			current = name
			inBody = true
			continue
		}
		if i := strings.Index(line, "||--"); i > 0 || strings.Contains(line, "}o--") || strings.Contains(line, "}o..") || strings.Contains(line, "||..") || strings.Contains(line, "|o--") || strings.Contains(line, "}|--") {
			card := ""
			for _, k := range []string{"||--o{", "||--|{", "}|--||", "}|--o{", "}o--||", "}o--o{", "||..o{", "||..|{", "}o..||"} {
				if idx := strings.Index(line, k); idx > 0 {
					left := strings.TrimSpace(line[:idx])
					rest := strings.TrimSpace(line[idx+len(k):])
					right, label := rest, ""
					if j := strings.IndexByte(rest, ':'); j >= 0 {
						right = strings.TrimSpace(rest[:j])
						label = strings.TrimSpace(rest[j+1:])
					}
					add(left)
					add(right)
					rels = append(rels, erRel{left: left, right: right, card: k, label: label})
					card = k
					break
				}
			}
			if card != "" {
				continue
			}
		}
		add(line)
	}
	if len(entities) == 0 {
		return "(empty diagram)"
	}
	var b strings.Builder
	for i, e := range entities {
		name := e
		maxW := len([]rune(name))
		for _, a := range attrs[e] {
			if n := len([]rune(a)); n > maxW {
				maxW = n
			}
		}
		if maxW < 8 {
			maxW = 8
		}
		if maxW > 28 {
			maxW = 28
		}
		clip := func(s string) string {
			runes := []rune(s)
			if len(runes) > maxW {
				return string(runes[:maxW-1]) + "…"
			}
			return s
		}
		fmt.Fprintf(&b, "┌─%s┐\n", strings.Repeat("─", maxW))
		fmt.Fprintf(&b, "│ %-*s│\n", maxW, clip(name))
		if ms := attrs[e]; len(ms) > 0 {
			fmt.Fprintf(&b, "├─%s┤\n", strings.Repeat("─", maxW))
			for _, m := range ms {
				fmt.Fprintf(&b, "│ %-*s│\n", maxW, clip(m))
			}
		}
		fmt.Fprintf(&b, "└─%s┘\n", strings.Repeat("─", maxW))
		if i < len(entities)-1 {
			b.WriteByte('\n')
		}
	}
	if len(rels) > 0 {
		b.WriteByte('\n')
		for _, r := range rels {
			arrow := "──"
			switch {
			case strings.Contains(r.card, "o{"):
				arrow = "──◁"
			case strings.Contains(r.card, "|{"):
				arrow = "──◁"
			}
			if r.label != "" {
				fmt.Fprintf(&b, "%s %s %s : %s\n", r.left, arrow, r.right, r.label)
			} else {
				fmt.Fprintf(&b, "%s %s %s\n", r.left, arrow, r.right)
			}
		}
	}
	return b.String()
}
