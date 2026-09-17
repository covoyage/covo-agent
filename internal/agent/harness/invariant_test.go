package harness

import (
	"context"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

// msgKey identifies a conversation message for the invariant comparison.
type msgKey struct {
	Role    agentcore.Role
	Content string
}

// TestModelVisibleMessagesAreLogged enforces the durability invariant: every
// message that reaches a model request must be reconstructable from the
// SQLite session store. Catches "messages silently lost after
// compression/fork/persist" regressions at the session layer.
func TestModelVisibleMessagesAreLogged(t *testing.T) {
	sc := &Scenario{
		Name: "invariant",
		Steps: []ScenarioStep{
			{
				User: "check the session state",
				Mock: []MockTurn{
					{ToolCalls: []agentcore.ToolCall{
						{ID: "call_inv_1", Name: "get_goal", Arguments: `{}`},
					}},
					{Content: "state checked via tool."},
				},
			},
			{
				User: "thanks, done",
				Mock: []MockTurn{
					{Content: "all done."},
				},
			},
		},
	}

	h := New(t, sc)
	defer h.Close()
	ctx := context.Background()

	// Production flow creates the session before the first turn; the harness
	// runs the core directly, so create it here.
	h.Agent.SessionManager().EnsureCurrentSession(ctx, sc.WorkingDir)

	// Run both steps manually so the session is still open when we load it.
	for _, step := range sc.Steps {
		if _, err := h.Agent.Core().Run(ctx, step.User); err != nil {
			t.Fatalf("agent run (user=%q): %v", step.User, err)
		}
	}

	callsBeforeFork := len(h.RecordedCalls())
	if callsBeforeFork < 3 {
		t.Fatalf("expected at least 3 LLM calls (2 tool turns + final), got %d", callsBeforeFork)
	}

	// Load the persisted snapshot from SQLite.
	sessionID := h.Agent.SessionManager().CurrentID()
	if sessionID == "" {
		t.Fatal("no current session id after run")
	}
	snap, err := h.Agent.SessionManager().LoadSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	logged := map[msgKey]bool{}
	for _, m := range snap.Messages {
		logged[msgKey{Role: m.Role, Content: m.Content}] = true
	}
	if len(logged) == 0 {
		t.Fatal("persisted session has no messages")
	}

	// Invariant: every message in every model request must be in the log.
	checkRequests := func(calls []RecordedCall, label string) {
		t.Helper()
		for i, rc := range calls {
			for j, msg := range rc.Request.Messages {
				if msg.Role == agentcore.RoleSystem {
					continue // system prompt is configured, not conversational
				}
				key := msgKey{Role: msg.Role, Content: msg.Content}
				if !logged[key] {
					t.Errorf("%s: request %d message %d (%s, %.80q) reached the model but is not reconstructable from the session store",
						label, i, j, msg.Role, msg.Content)
				}
			}
		}
	}
	checkRequests(h.RecordedCalls(), "pre-fork")

	// Fork and continue: the forked session must independently satisfy the
	// same invariant for the requests served from the forked history.
	if _, err := h.Agent.SessionManager().ForkSession(ctx); err != nil {
		t.Fatalf("fork session: %v", err)
	}
	if _, err := h.Agent.Core().Run(ctx, "one more thing"); err != nil {
		t.Fatalf("post-fork run: %v", err)
	}

	forkID := h.Agent.SessionManager().CurrentID()
	forkSnap, err := h.Agent.SessionManager().LoadSession(ctx, forkID)
	if err != nil {
		t.Fatalf("load forked session: %v", err)
	}
	if len(forkSnap.Messages) == 0 {
		t.Fatal("forked session has no messages")
	}
	forkLogged := map[msgKey]bool{}
	for _, m := range forkSnap.Messages {
		forkLogged[msgKey{Role: m.Role, Content: m.Content}] = true
	}

	postForkCalls := h.RecordedCalls()[callsBeforeFork:]
	if len(postForkCalls) == 0 {
		t.Fatal("no LLM calls recorded after fork")
	}
	for i, rc := range postForkCalls {
		for j, msg := range rc.Request.Messages {
			if msg.Role == agentcore.RoleSystem {
				continue
			}
			key := msgKey{Role: msg.Role, Content: msg.Content}
			if !forkLogged[key] {
				t.Errorf("post-fork: request %d message %d (%s, %.80q) reached the model but is not reconstructable from the forked session store",
					i, j, msg.Role, msg.Content)
			}
		}
	}
}
