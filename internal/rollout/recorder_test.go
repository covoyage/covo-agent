package rollout

import (
	"context"
	"log/slog"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func modelCtx() context.Context {
	return agentcore.WithRunInfo(context.Background(), agentcore.RunInfo{Component: "model"})
}

// mockProvider is a minimal test provider supporting Complete and Stream.
type mockProvider struct {
	complete func(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error)
	stream   func(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error)
}

func (m *mockProvider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	if m.complete != nil {
		return m.complete(ctx, req)
	}
	return &agentcore.ProviderResponse{Content: "hello", FinishReason: "stop"}, nil
}

func (m *mockProvider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	if m.stream != nil {
		return m.stream(ctx, req)
	}
	out := make(chan agentcore.StreamDelta)
	close(out)
	return out, nil
}

// TestTwoLayerRecording verifies that main and aux interactions are grouped
// under a single logical turn and that tool results attach to the correct
// interaction by ID.
func TestTwoLayerRecording(t *testing.T) {
	inner := &mockProvider{
		complete: func(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
			if ctx == nil {
				return nil, nil
			}
			return &agentcore.ProviderResponse{
				Content: "tool call",
				ToolCalls: []agentcore.ToolCall{
					{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`},
				},
				FinishReason: "tool_calls",
				Usage:        agentcore.TokenUsage{PromptTokens: 100, CompletionTokens: 20},
			}, nil
		},
	}

	r := NewRecorder(RecorderConfig{Provider: "openai", Model: "gpt-5", CWD: "."})
	r.SetInner(inner)

	// Main agent turn begins.
	r.BeginTurn()

	// Aux compression call (no model run info in context => aux).
	ctxAux := context.Background()
	r.Complete(ctxAux, &agentcore.ProviderRequest{Model: "gpt-5", Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "compress"}}})

	// Main call (model run info in context => main).
	ctxMain := modelCtx()
	resp, err := r.Complete(ctxMain, &agentcore.ProviderRequest{
		Model: "gpt-5",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "read file"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	// Tool executes, result reported back.
	r.RecordToolResult(resp.ToolCalls[0].ID, "file contents", nil, 5)

	// Turn ends.
	r.CompleteTurn()

	ro := r.Rollout()
	if len(ro.Turns) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(ro.Turns))
	}
	turn := ro.Turns[0]
	if turn.Number != 1 {
		t.Fatalf("expected turn number 1, got %d", turn.Number)
	}
	if len(turn.Interactions) != 2 {
		t.Fatalf("expected 2 interactions (aux + main), got %d", len(turn.Interactions))
	}

	aux := turn.Interactions[0]
	main := turn.Interactions[1]
	if aux.Kind != "aux" {
		t.Errorf("interaction 0 kind = %q, want aux", aux.Kind)
	}
	if main.Kind != "main" {
		t.Errorf("interaction 1 kind = %q, want main", main.Kind)
	}

	// Aggregated tool calls on the turn should have the result filled.
	if len(turn.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(turn.ToolCalls))
	}
	if turn.ToolCalls[0].Result != "file contents" {
		t.Errorf("tool result = %q, want %q", turn.ToolCalls[0].Result, "file contents")
	}

	// The main interaction should also carry the tool call with result.
	if len(main.ToolCalls) != 1 || main.ToolCalls[0].Result != "file contents" {
		t.Errorf("main interaction tool call result mismatch: %+v", main.ToolCalls)
	}

	// MainInteraction helper should point at the main call.
	if mi := turn.MainInteraction(); mi == nil || mi.Kind != "main" {
		t.Errorf("MainInteraction did not return the main interaction")
	}
}

// TestTurnOutsideWindow verifies a call that fires outside a begin/end window
// still gets recorded with its own turn.
func TestTurnOutsideWindow(t *testing.T) {
	inner := &mockProvider{}
	r := NewRecorder(RecorderConfig{Provider: "openai", Model: "gpt-5"})
	r.SetInner(inner)

	_, err := r.Complete(context.Background(), &agentcore.ProviderRequest{
		Model: "gpt-5",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	ro := r.Rollout()
	if len(ro.Turns) != 1 {
		t.Fatalf("expected 1 auto-created turn, got %d", len(ro.Turns))
	}
	if len(ro.Turns[0].Interactions) != 1 {
		t.Fatalf("expected 1 interaction, got %d", len(ro.Turns[0].Interactions))
	}
}

// TestReplayUsesMainInteraction verifies replay drives the main interaction.
func TestReplayUsesMainInteraction(t *testing.T) {
	inner := &mockProvider{
		complete: func(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
			return &agentcore.ProviderResponse{
				Content:      "reply",
				FinishReason: "stop",
				ToolCalls: []agentcore.ToolCall{
					{ID: "c1", Name: "grep", Arguments: `{"q":"foo"}`},
				},
			}, nil
		},
	}
	r := NewRecorder(RecorderConfig{Provider: "openai", Model: "gpt-5"})
	r.SetInner(inner)

	r.BeginTurn()
	ctxMain := modelCtx()
	_, _ = r.Complete(ctxMain, &agentcore.ProviderRequest{
		Model:    "gpt-5",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "search"}},
	})
	r.RecordToolResult("c1", "matched", nil, 3)
	r.CompleteTurn()

	ro := r.Rollout()

	engine := NewReplayEngine(ReplayConfig{
		Provider: inner,
		Strategy: StrategyMock,
		Logger:   testLogger(t),
	})
	res, err := engine.Replay(context.Background(), ro)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(res.Rollout.Turns) != 1 {
		t.Fatalf("expected 1 replayed turn, got %d", len(res.Rollout.Turns))
	}
	if len(res.Rollout.Turns[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 replayed tool call, got %d", len(res.Rollout.Turns[0].ToolCalls))
	}
	if res.Rollout.Turns[0].ToolCalls[0].Result != "matched" {
		t.Errorf("mock replay did not inject recorded result, got %q", res.Rollout.Turns[0].ToolCalls[0].Result)
	}
}
