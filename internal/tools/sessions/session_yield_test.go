package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/covoyage/covo-agent/internal/tools/subagent"
	"github.com/covoyage/covonaut/agentcore"
)

func TestSessionsYieldNoPendingChild(t *testing.T) {
	tool := BuildSessionsYieldTool(subagent.NewSubagentRegistry())
	_, err := tool.Func(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "no pending child completion owned by this turn" {
		t.Fatalf("err=%v", err)
	}
}

func TestSessionsYieldAfterBackgroundSpawn(t *testing.T) {
	reg := subagent.NewSubagentRegistry()
	unblock := make(chan struct{})
	started := make(chan struct{})
	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		close(started)
		<-unblock
		return "ok", nil
	}
	spawn := subagent.BuildSessionsSpawnTool(runner, nil, nil, nil, reg, nil)
	if _, err := spawn.Func(context.Background(), json.RawMessage(`{"task":"t","background":true}`)); err != nil {
		t.Fatal(err)
	}
	<-started
	tool := BuildSessionsYieldTool(reg)
	got, err := tool.Func(context.Background(), json.RawMessage(`{"acknowledgment":"waiting","message":"hold"}`))
	if !agentcore.IsInterrupt(err) {
		t.Fatalf("expected interrupt, got %v", err)
	}
	m := got.(map[string]any)
	if m["status"] != "yielded" || m["acknowledgment"] != "waiting" || m["message"] != "hold" {
		t.Fatalf("got %+v", m)
	}
	if !errors.Is(err, agentcore.ErrInterrupt) {
		t.Fatalf("unwrap=%v", err)
	}
	close(unblock)
}
