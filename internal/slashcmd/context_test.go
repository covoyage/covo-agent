package slashcmd

import (
	"context"
	"sync/atomic"
	"testing"

	runtimeapp "github.com/covoyage/covo-agent/internal/app"
)

func TestSlashContextRuntimeWiring(t *testing.T) {
	state := runtimeapp.NewRuntimeState()
	state.SetReasoningEffort("high")
	state.SetBusyInputMode("queue")

	agents := runtimeapp.NewAgentRuntime(nil, nil)
	var busy atomic.Bool
	busy.Store(true)

	sctx := &SlashContext{
		Input: "/status",
		Runtime: RuntimeDependencies{
			Context: context.Background(),
			Busy:    &busy,
			Agents:  agents,
			State:   state,
		},
	}

	if sctx.Runtime.Context == nil {
		t.Fatal("runtime context should be set")
	}
	if !sctx.Runtime.Busy.Load() {
		t.Fatal("runtime busy flag should be true")
	}
	if got := sctx.Runtime.State.ReasoningEffort(); got != "high" {
		t.Fatalf("reasoning effort mismatch: got %q want %q", got, "high")
	}
	if got := sctx.Runtime.State.BusyInputMode(); got != "queue" {
		t.Fatalf("busy mode mismatch: got %q want %q", got, "queue")
	}
	if sctx.Runtime.Agents.Current() != nil {
		t.Fatal("expected nil current covo agent for minimal runtime")
	}
	if sctx.Runtime.Agents.Core() != nil {
		t.Fatal("expected nil current core agent for minimal runtime")
	}
}

func TestContextBuilderInjectsRequestValuesWithoutMutatingTemplate(t *testing.T) {
	state := runtimeapp.NewRuntimeState()
	builder := &ContextBuilder{
		Runtime: RuntimeDependencies{
			State:        state,
			WorkingDir:   "/workspace",
			ProviderType: "template-provider",
			Model:        "template-model",
		},
	}
	ctx := context.WithValue(context.Background(), struct{}{}, "request")
	built := builder.Build("/status", ctx, "request-provider", "request-model")

	if built.Input != "/status" || built.Runtime.Context != ctx {
		t.Fatalf("request values were not injected: %+v", built)
	}
	if built.Runtime.ProviderType != "request-provider" || built.Runtime.Model != "request-model" {
		t.Fatalf("provider/model were not injected: %+v", built.Runtime)
	}
	if built.Runtime.WorkingDir != "/workspace" || built.Runtime.State != state {
		t.Fatal("stable runtime dependencies were not preserved")
	}
	if builder.Runtime.Context != nil || builder.Runtime.ProviderType != "template-provider" || builder.Runtime.Model != "template-model" {
		t.Fatalf("Build mutated the template: %+v", builder.Runtime)
	}
}
