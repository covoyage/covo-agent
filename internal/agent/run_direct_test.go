package agent

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covonaut/agentcore"
)

// streamMockProvider supports both Complete and Stream so a full agent turn
// (which always streams) can run in tests.
type streamMockProvider struct {
	calls int
}

func (m *streamMockProvider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	m.calls++
	return &agentcore.ProviderResponse{
		Content:      "direct-response",
		Usage:        agentcore.TokenUsage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
		FinishReason: "stop",
	}, nil
}

func (m *streamMockProvider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	m.calls++
	out := make(chan agentcore.StreamDelta)
	go func() {
		defer close(out)
		out <- agentcore.StreamDelta{Content: "direct-response"}
		out <- agentcore.StreamDelta{
			Usage:        &agentcore.TokenUsage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
			FinishReason: "stop",
			Done:         true,
		}
	}()
	return out, nil
}

func TestRunDirectDelegatesToCore(t *testing.T) {
	mock := &streamMockProvider{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelError)}))

	ca, err := NewCovoAgent(CovoAgentConfig{
		Mode:         ModeGeneral,
		Provider:     mock,
		ProviderName: "mock",
		Model:        "mock-model",
		WorkingDir:   t.TempDir(),
		HomeDir:      t.TempDir(),
		Logger:       logger,
		ToolProfile:  "minimal",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	defer ca.Close()

	result, err := ca.RunDirect(context.Background(), "hello")
	if err != nil {
		t.Fatalf("RunDirect: %v", err)
	}
	if result != "direct-response" {
		t.Fatalf("RunDirect result = %q, want %q", result, "direct-response")
	}
	if mock.calls == 0 {
		t.Fatal("expected the mock provider to be called")
	}
}
