package rollout

import (
	"testing"
)

func buildTestRollout(id, session, parent string, turns []Turn) *Rollout {
	r := &Rollout{
		ID:        id,
		SessionID: session,
		ParentID:  parent,
		Provider:  "openai",
		Model:     "gpt-5",
		Turns:     turns,
	}
	return r
}

func TestReduceTraceParentLinkage(t *testing.T) {
	parent := buildTestRollout("R_parent", "sess_parent", "", []Turn{
		{
			Number: 1,
			Interactions: []Interaction{
				{Kind: "main"},
				{Kind: "compression"},
			},
			ToolCalls: []ToolCallSnapshot{{ID: "c1", Name: "read_file"}},
		},
	})
	child := buildTestRollout("R_child", "sess_child", "R_parent", []Turn{
		{
			Number: 1,
			Interactions: []Interaction{
				{Kind: "main"},
				{Kind: "title"},
			},
		},
	})

	g := ReduceTrace([]*Rollout{parent, child})
	if len(g.Rollouts) != 2 {
		t.Fatalf("expected 2 rollouts, got %d", len(g.Rollouts))
	}
	if g.Stats.Subagents != 1 {
		t.Errorf("expected 1 subagent, got %d", g.Stats.Subagents)
	}
	if g.Stats.Compactions != 1 {
		t.Errorf("expected 1 compaction, got %d", g.Stats.Compactions)
	}
	if g.Stats.TotalInteractions != 4 {
		t.Errorf("expected 4 interactions, got %d", g.Stats.TotalInteractions)
	}

	// Verify the spawn edge parent->child exists.
	spawnFound := false
	for _, e := range g.Edges {
		if e.Kind == "spawn" && e.From == "R_parent.rollout.0" && e.To == "R_child.rollout.0" {
			spawnFound = true
		}
	}
	if !spawnFound {
		t.Errorf("expected spawn edge from R_parent to R_child, edges: %+v", g.Edges)
	}

	// Verify a first-class compaction node is present with kind compression.
	compFound := false
	for _, n := range g.Nodes {
		if n.Kind == "compression" {
			if n.Type != "compaction" {
				t.Errorf("compression node type = %q, want compaction", n.Type)
			}
			compFound = true
		}
	}
	if !compFound {
		t.Errorf("expected a compression node in the trace")
	}

	// Verify a compaction edge kind is emitted.
	compEdge := false
	for _, e := range g.Edges {
		if e.Kind == "compaction" {
			compEdge = true
		}
	}
	if !compEdge {
		t.Errorf("expected a compaction edge in the trace")
	}

	// Determinism: reducing again yields identical JSON.
	data1, _ := g.Marshal()
	g2 := ReduceTrace([]*Rollout{parent, child})
	data2, _ := g2.Marshal()
	if string(data1) != string(data2) {
		t.Errorf("trace reduction is not deterministic")
	}
}

// TestReduceTracePerTurnEdges verifies per-turn subagent lifecycle edges
// recorded on a rollout surface in the reduced graph.
func TestReduceTracePerTurnEdges(t *testing.T) {
	parent := buildTestRollout("R_p", "sp", "", []Turn{{Number: 1, Interactions: []Interaction{{Kind: "main"}}}})
	parent.Edges = []InteractionEdge{
		{Kind: SubagentEdgeSpawn, ParentTurn: 1, ChildID: "R_c"},
		{Kind: SubagentEdgeResult, ParentTurn: 1, ChildID: "R_c", Message: "done"},
	}
	child := buildTestRollout("R_c", "sc", "R_p", []Turn{{Number: 1, Interactions: []Interaction{{Kind: "main"}}}})

	g := ReduceTrace([]*Rollout{parent, child})

	// The spawn edge should anchor at the parent's turn 1.
	spawnAtTurn := false
	for _, e := range g.Edges {
		if e.Kind == "spawn" && e.From == "R_p.turn.1" && e.To == "R_c.rollout.0" {
			spawnAtTurn = true
		}
	}
	if !spawnAtTurn {
		t.Errorf("expected per-turn spawn edge from R_p.turn.1 to R_c, edges: %+v", g.Edges)
	}

	// The result edge should be present with kind "result".
	resultFound := false
	for _, e := range g.Edges {
		if e.Kind == "result" && e.From == "R_p.turn.1" {
			resultFound = true
		}
	}
	if !resultFound {
		t.Errorf("expected result edge from R_p.turn.1, edges: %+v", g.Edges)
	}
}
