package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/lifecycle"
)

func TestLifecycleContributorBridgeEmitsRunTurnToolAndErrorEvents(t *testing.T) {
	registry := lifecycle.NewRegistry()
	var events []lifecycle.Event
	var contexts []*lifecycle.HookContext
	registry.RegisterFunc("capture", func(event lifecycle.Event, hookContext *lifecycle.HookContext) {
		events = append(events, event)
		copied := *hookContext
		contexts = append(contexts, &copied)
	})
	bridge := newLifecycleContributorBridge(nil, registry)
	ctx := context.Background()
	run := &agentcore.AgentRunContext{Input: "hello", Turn: 2}

	if err := bridge.BeforeAgentRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := bridge.BeforeTurn(ctx, run); err != nil {
		t.Fatal(err)
	}
	tool := &agentcore.ToolExecutionContext{
		ToolCalls: []agentcore.ToolCall{{Name: "read", Arguments: `{"path":"a.go"}`}},
		Results:   []agentcore.ToolResult{{Result: "content", Err: errors.New("tool failed")}},
	}
	if err := bridge.BeforeToolExecution(ctx, run, tool); err != nil {
		t.Fatal(err)
	}
	bridge.AfterToolExecution(ctx, run, tool)
	bridge.AfterTurn(ctx, run, agentcore.TurnInfo{HadToolCalls: true})
	bridge.AfterAgentRun(ctx, run, "partial", errors.New("run failed"))

	want := []lifecycle.Event{
		lifecycle.EventOnSessionStart,
		lifecycle.EventBeforeTurn,
		lifecycle.EventBeforeToolCall,
		lifecycle.EventAfterToolCall,
		lifecycle.EventOnError,
		lifecycle.EventAfterTurn,
		lifecycle.EventOnError,
		lifecycle.EventOnSessionEnd,
	}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
	if contexts[2].ToolName != "read" || contexts[2].ToolInput == "" {
		t.Fatalf("before-tool context = %+v", contexts[2])
	}
	if contexts[3].ToolResult != "content" || contexts[3].Turn != 2 {
		t.Fatalf("after-tool context = %+v", contexts[3])
	}
}
