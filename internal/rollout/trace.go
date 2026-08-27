package rollout

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// TraceGraph is the reduced semantic graph of one or more rollouts. It mirrors
// the concept of a trace-reduced session state: a directed graph of nodes
// (turns, interactions, compaction events, subagents, tool calls) connected by
// edges that capture sequencing, auxiliary-work association, and parent/child
// subagent linkage.
type TraceGraph struct {
	Version  int           `json:"version"`
	Rollouts []RolloutRef  `json:"rollouts"`
	Nodes    []TraceNode   `json:"nodes"`
	Edges    []TraceEdge   `json:"edges"`
	Stats    TraceStats    `json:"stats"`
}

// RolloutRef summarises a rollout that participated in the reduction.
type RolloutRef struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	ParentID  string `json:"parent_id,omitempty"`
	Model     string `json:"model"`
	Provider  string `json:"provider,omitempty"`
	TurnCount int    `json:"turn_count"`
}

// TraceNode is a single semantic node in the reduced graph.
type TraceNode struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // rollout | turn | interaction | tool
	RolloutID string `json:"rollout_id,omitempty"`
	Label     string `json:"label,omitempty"`
	Kind      string `json:"kind,omitempty"` // main | compression | title | review | aux | ...
	Tool      string `json:"tool,omitempty"`
	Tokens    int64  `json:"tokens,omitempty"`
	Error     string `json:"error,omitempty"`
}

// TraceEdge is a directed edge between two nodes.
type TraceEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind,omitempty"` // next | spawn | interaction | aux | compaction | tool | result
}

// TraceEdgeKind constants for the reduced graph's edge kinds.
const (
	TraceEdgeNext        = "next"
	TraceEdgeSpawn       = "spawn"
	TraceEdgeInteraction = "interaction"
	TraceEdgeAux         = "aux"
	TraceEdgeCompaction  = "compaction"
	TraceEdgeTool        = "tool"
)

// TraceStats aggregates counts across the reduced graph.
type TraceStats struct {
	TotalTurns        int `json:"total_turns"`
	TotalInteractions int `json:"total_interactions"`
	Compactions       int `json:"compactions"`
	Subagents         int `json:"subagents"`
	ToolCalls         int `json:"tool_calls"`
}

// nodeID uniquely identifies a node across multiple rollouts.
func nodeID(rolloutID, typ string, seq int) string {
	return fmt.Sprintf("%s.%s.%d", rolloutID, typ, seq)
}

// edgeKind normalizes a subagent Edge kind into a stable trace edge kind,
// mapping self-describing labels while retaining unknown kinds verbatim.
func edgeKind(kind string) string {
	switch kind {
	case SubagentEdgeSpawn:
		return TraceEdgeSpawn
	case SubagentEdgeResult, SubagentEdgeClose, SubagentEdgeTimeout, SubagentEdgeSend:
		return kind
	default:
		if kind == "" {
			return TraceEdgeSpawn
		}
		return kind
	}
}

// ReduceTrace builds a semantic graph from a set of rollouts. Rollouts may be
// linked via ParentID (subagent spawn edges) and interactions carry a Kind
// (main/compression/title/review/...). The graph is fully deterministic given
// the same input rollouts.
func ReduceTrace(rollouts []*Rollout) *TraceGraph {
	// Deterministic ordering: by ID so output is stable across runs.
	sorted := make([]*Rollout, len(rollouts))
	copy(sorted, rollouts)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	g := &TraceGraph{Version: 1}

	turnTbl := map[string]map[int]string{} // rolloutID -> turnSeq -> nodeID
	toolTbl := map[string]string{}         // rolloutID+tool -> tool node id
	anchorTbl := map[string]string{}       // rolloutID -> anchor node id
	spawned := map[string]bool{}           // child rollout IDs already linked

	// Pass 1: register rollout metadata, per-rollout turn tables, and an
	// anchor node for every rollout. Anchors give spawn edges a stable From.
	for _, r := range sorted {
		g.Rollouts = append(g.Rollouts, RolloutRef{
			ID:        r.ID,
			SessionID: r.SessionID,
			ParentID:  r.ParentID,
			Model:     r.Model,
			Provider:  r.Provider,
			TurnCount: len(r.Turns),
		})
		anchor := nodeID(r.ID, "rollout", 0)
		anchorTbl[r.ID] = anchor
		g.Nodes = append(g.Nodes, TraceNode{
			ID:        anchor,
			Type:      "rollout",
			RolloutID: r.ID,
			Label:     fmt.Sprintf("rollout %s (model %s)", shortID(r.ID), r.Model),
		})
		turnTbl[r.ID] = map[int]string{}
		for _, t := range r.Turns {
			turnTbl[r.ID][t.Number] = nodeID(r.ID, "turn", t.Number)
		}
	}

	// Pass 2: build turns, interactions, tools, and spawn edges.
	for _, r := range sorted {
		// Subagent node: each spawned child gets a node and a spawn edge from
		// the parent's anchor to the child's anchor.
		if r.ParentID != "" && !spawned[r.ID] {
			g.Edges = append(g.Edges, TraceEdge{
				From: anchorTbl[r.ParentID],
				To:   nodeID(r.ID, "rollout", 0),
				Kind: TraceEdgeSpawn,
			})
			spawned[r.ID] = true
			g.Stats.Subagents++
		}

		// Surface subagent lifecycle edges recorded on this rollout (spawn,
		// send_message, result, close, timeout) at per-turn granularity,
		// linking the parent turn to the child's rollout node.
		for _, e := range r.Edges {
			from := anchorTbl[r.ID]
			if e.ParentTurn > 0 {
				if tn, ok := turnTbl[r.ID][e.ParentTurn]; ok {
					from = tn
				}
			}
			childNode := from
			if e.ChildID != "" {
				childNode = nodeID(e.ChildID, "rollout", 0)
			}
			g.Edges = append(g.Edges, TraceEdge{From: from, To: childNode, Kind: edgeKind(e.Kind)})
		}

		seq := map[string]int{}
		// Tool nodes and edges are built lazily on first use.
		ensureTool := func(tool string) string {
			key := r.ID + "|" + tool
			if id, ok := toolTbl[key]; ok {
				return id
			}
			id := nodeID(r.ID, "tool", seq["tool"])
			seq["tool"]++
			g.Nodes = append(g.Nodes, TraceNode{
				ID:        id,
				Type:      "tool",
				RolloutID: r.ID,
				Tool:      tool,
				Label:     tool,
			})
			g.Stats.ToolCalls++
			toolTbl[key] = id
			return id
		}

		var prevTurnNode string
		for _, t := range r.Turns {
			turnNode := turnTbl[r.ID][t.Number]
			g.Nodes = append(g.Nodes, TraceNode{
				ID:        turnNode,
				Type:      "turn",
				RolloutID: r.ID,
				Label:     fmt.Sprintf("turn %d", t.Number),
			})

			// Sequence edge from the previous turn.
			if prevTurnNode != "" {
				g.Edges = append(g.Edges, TraceEdge{From: prevTurnNode, To: turnNode, Kind: TraceEdgeNext})
			}

			// Anchor the first turn to the rollout anchor when this rollout was
			// itself spawned by a parent.
			if t.Number == 1 && r.ParentID != "" {
				g.Edges = append(g.Edges, TraceEdge{
					From: anchorTbl[r.ID],
					To:   turnNode,
					Kind: TraceEdgeInteraction,
				})
			}

			var mainNode string
			for i := range t.Interactions {
				in := &t.Interactions[i]
				interID := nodeID(r.ID, "interaction", t.Number*100+i)
				kind := in.Kind
				if kind == "" {
					kind = "main"
				}
				g.Nodes = append(g.Nodes, TraceNode{
					ID:        interID,
					Type:      "interaction",
					RolloutID: r.ID,
					Kind:      kind,
					Label:     fmt.Sprintf("turn %d %s (%s)", t.Number, kind, in.Request.Model),
					Tokens:    in.Response.TotalTokens,
					Error:     in.Error,
				})
				// A compression interaction is a first-class "compaction" node
				// (context was compacted at this point), matched with a dedicated
				// edge kind so it stands apart from routine auxiliary calls.
				isCompaction := kind == "compression"
				nodeType := "interaction"
				edgeKind := TraceEdgeAux
				if isCompaction {
					nodeType = "compaction"
					edgeKind = TraceEdgeCompaction
				}
				g.Nodes[len(g.Nodes)-1].Type = nodeType

				// Main interaction anchors the turn; aux/compaction interactions
				// attach to their turn via an aux/compaction edge.
				if kind == "main" {
					mainNode = interID
					g.Edges = append(g.Edges, TraceEdge{From: turnNode, To: interID, Kind: TraceEdgeInteraction})
				} else {
					g.Edges = append(g.Edges, TraceEdge{From: turnNode, To: interID, Kind: edgeKind})
					if isCompaction {
						g.Stats.Compactions++
					}
				}
				g.Stats.TotalInteractions++
			}

			// Tool calls made by the turn's main interaction.
			if mainNode != "" {
				for _, tc := range t.ToolCalls {
					toolNode := ensureTool(tc.Name)
					g.Edges = append(g.Edges, TraceEdge{From: mainNode, To: toolNode, Kind: TraceEdgeTool})
				}
			}

			prevTurnNode = turnNode
			g.Stats.TotalTurns++
		}
	}

	return g
}

// Marshal serializes the trace graph to indented JSON.
func (g *TraceGraph) Marshal() ([]byte, error) {
	return json.MarshalIndent(g, "", "  ")
}

// FormatTrace renders a human-readable, tree-like view of the reduced graph,
// grouping nodes under each rollout and indenting subagents.
func FormatTrace(g *TraceGraph) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Trace graph (%d rollout(s), %d nodes, %d edges):\n\n",
		len(g.Rollouts), len(g.Nodes), len(g.Edges))
	for _, r := range g.Rollouts {
		prefix := ""
		if r.ParentID != "" {
			prefix = "└ subagent → "
		}
		fmt.Fprintf(&b, "%s• rollout %s", prefix, r.ID)
		if r.Model != "" {
			fmt.Fprintf(&b, " [model %s]", r.Model)
		}
		if r.ParentID != "" {
			fmt.Fprintf(&b, " (parent %s)", shortID(r.ParentID))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Stats: %d turns, %d interactions, %d compaction(s), %d subagent(s), %d tool call(s)\n",
		g.Stats.TotalTurns, g.Stats.TotalInteractions, g.Stats.Compactions,
		g.Stats.Subagents, g.Stats.ToolCalls)
	b.WriteString("\nEdges:\n")
	for _, e := range g.Edges {
		fmt.Fprintf(&b, "  %-16s ─%s→ %s\n", trimNode(e.From), e.Kind, trimNode(e.To))
	}
	return b.String()
}

func trimNode(id string) string {
	// "rolloutid.turn.3" -> "turn.3"
	idx := strings.Index(id, ".")
	if idx < 0 {
		return id
	}
	return id[idx+1:]
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
