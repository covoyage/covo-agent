package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/agent"
)

type runtimeTestProvider struct{}

func (*runtimeTestProvider) Complete(context.Context, *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	return &agentcore.ProviderResponse{Content: "ok"}, nil
}

func (*runtimeTestProvider) Stream(context.Context, *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	return nil, errors.New("not implemented")
}

func TestAgentRuntimeReplacePreservesStateAndRunsHooksAfterPublication(t *testing.T) {
	homeDir := t.TempDir()
	provider := &runtimeTestProvider{}
	factory := NewAgentFactory(agent.CovoAgentConfig{
		HomeDir:    homeDir,
		WorkingDir: homeDir,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, NewRuntimeState())
	request := AgentRequest{Mode: agent.ModeGeneral, Provider: provider, ProviderName: "test", Model: "test"}
	initial, err := factory.New(request)
	if err != nil {
		t.Fatalf("create initial agent: %v", err)
	}
	initial.Core().State().AddMessage(agentcore.Message{Role: agentcore.RoleUser, Content: "preserve me"})

	runtime := NewAgentRuntime(factory, initial)
	t.Cleanup(runtime.Close)
	prepared := false
	runtime.SetPrepare(func(*agent.CovoAgent) { prepared = true })
	hookCalled := false
	runtime.OnReplace(func(replacement AgentReplacement) {
		hookCalled = true
		if runtime.Current() != replacement.Agent || runtime.Core() != replacement.Core {
			t.Error("replacement hook ran before publication")
		}
	})

	replacement, err := runtime.Replace(request, true)
	if err != nil {
		t.Fatalf("replace agent: %v", err)
	}
	if !prepared || !hookCalled {
		t.Fatalf("prepare=%v hook=%v", prepared, hookCalled)
	}
	if replacement.Snapshot == nil {
		t.Fatal("preserving replacement did not expose a snapshot")
	}
	messages := replacement.Core.State().Messages()
	found := false
	for _, message := range messages {
		found = found || message.Content == "preserve me"
	}
	if !found {
		t.Fatalf("replacement messages lost snapshot: %+v", messages)
	}
	if runtime.AgentPointer().Load() != replacement.Agent || runtime.CorePointer().Load() != replacement.Core {
		t.Fatal("compatibility pointers do not expose current replacement")
	}
}

func TestAgentRuntimeReplaceWithoutStateStartsFresh(t *testing.T) {
	homeDir := t.TempDir()
	provider := &runtimeTestProvider{}
	factory := NewAgentFactory(agent.CovoAgentConfig{HomeDir: homeDir, WorkingDir: homeDir}, NewRuntimeState())
	request := AgentRequest{Mode: agent.ModeGeneral, Provider: provider, ProviderName: "test", Model: "test"}
	initial, err := factory.New(request)
	if err != nil {
		t.Fatalf("create initial agent: %v", err)
	}
	initial.Core().State().AddMessage(agentcore.Message{Role: agentcore.RoleUser, Content: "discard me"})
	runtime := NewAgentRuntime(factory, initial)

	replacement, err := runtime.Replace(request, false)
	if err != nil {
		runtime.Close()
		t.Fatalf("replace agent: %v", err)
	}
	if replacement.Snapshot != nil {
		t.Fatal("fresh replacement unexpectedly included a snapshot")
	}
	for _, message := range replacement.Core.State().Messages() {
		if message.Content == "discard me" {
			t.Fatal("fresh replacement restored old conversation")
		}
	}
	runtime.Close()
	if runtime.Current() != nil || runtime.Core() != nil {
		t.Fatal("Close did not clear runtime pointers")
	}
}
