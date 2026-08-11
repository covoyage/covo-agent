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
	id   string
	text string
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

// RenderMermaid parses Mermaid flowchart text and returns an ASCII representation.
func RenderMermaid(input string) string {
	r := &MermaidRenderer{}
	if err := r.parse(input); err != nil {
		return fmt.Sprintf("(mermaid parse error: %v)\n\n%s", err, input)
	}
	return r.render()
}

// parse reads Mermaid flowchart syntax.
func (r *MermaidRenderer) parse(input string) error {
	lines := strings.Split(input, "\n")
	var currentSubgraph *mermaidSubgraph

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") || strings.HasPrefix(line, "%%{") {
			continue
		}

		lower := strings.ToLower(line)

		// Direction declaration
		if strings.HasPrefix(lower, "graph ") || strings.HasPrefix(lower, "flowchart ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				r.direction = strings.ToUpper(parts[1])
			}
			continue
		}

		// Subgraph
		if strings.HasPrefix(lower, "subgraph ") {
			title := strings.TrimSpace(line[len("subgraph "):])
			sg := mermaidSubgraph{title: title}
			r.subgraphs = append(r.subgraphs, sg)
			currentSubgraph = &r.subgraphs[len(r.subgraphs)-1]
			continue
		}
		if lower == "end" {
			currentSubgraph = nil
			continue
		}

		// Try to parse as edge or node definition
		r.parseLine(line, currentSubgraph)
	}

	if r.direction == "" {
		r.direction = "TD"
	}
	return nil
}

func (r *MermaidRenderer) parseLine(line string, sg *mermaidSubgraph) {
	// Try edge patterns first (they contain -->, ---, ==>, -.-)
	edgePatterns := []string{"-->", "==>", "===", "-.-", "---", "->>", "-->>"}
	for _, ep := range edgePatterns {
		if idx := strings.Index(line, ep); idx > 0 {
			rest := line[idx+len(ep):]
			left := strings.TrimSpace(line[:idx])
			label := ""
			// Check for edge label: -->|label|
			if strings.HasPrefix(rest, "|") {
				end := strings.Index(rest, "|")
				if end > 0 {
					label = strings.TrimSpace(rest[1:end])
					rest = strings.TrimSpace(rest[end+1:])
				}
			}

			// The right side might contain another node definition
			rightNode := strings.TrimSpace(rest)
			// Check if there's another edge in the rest
			for _, ep2 := range edgePatterns {
				if idx2 := strings.Index(rightNode, ep2); idx2 > 0 {
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

			if sg != nil {
				sg.nodes = append(sg.nodes, leftID, rightID)
			}
			return
		}
	}

	// Standalone node definition
	id, text, shape := parseNodeDef(line)
	if id != "" {
		r.addNode(id, text, shape)
		if sg != nil {
			sg.nodes = append(sg.nodes, id)
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
		if idx := strings.Index(s, sp.open); idx > 0 && strings.HasSuffix(s, sp.close) {
			id = strings.TrimSpace(s[:idx])
			text = strings.TrimSpace(s[idx+len(sp.open) : len(s)-len(sp.close)])
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

	// Layout
	if r.direction == "LR" {
		r.renderLR(&b, renderedLayers, layers, edges, nodeMap)
	} else {
		r.renderTD(&b, renderedLayers, layers, edges, nodeMap)
	}

	return b.String()
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
	// For LR, render each layer as a column
	maxBoxes := 0
	for _, l := range renderedLayers {
		if len(l) > maxBoxes {
			maxBoxes = len(l)
		}
	}

	// Calculate column width
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

			// Add arrow if not last column
			if col < len(renderedLayers)-1 {
				if row < len(layer) {
					line.WriteString(" ──▶ ")
				} else {
					line.WriteString("     ")
				}
			}
		}
		b.WriteString(line.String())
		b.WriteString("\n")
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

// renderBox renders a node as a 3-line box with border.
func renderBox(n mermaidNode) string {
	text := n.text
	if len(text) > 12 {
		text = text[:11] + "…"
	}
	width := len(text) + 2
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
	default: // rect
		return fmt.Sprintf("  ┌%s┐\n  │%s│\n  └%s┘", strings.Repeat("─", width-2), mermaidCenterText(text, width), strings.Repeat("─", width-2))
	}
}

func mermaidCenterText(text string, width int) string {
	if len(text) >= width {
		return text[:width]
	}
	left := (width - len(text)) / 2
	right := width - len(text) - left
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
}

func padString(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
