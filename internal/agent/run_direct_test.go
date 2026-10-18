package agent

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/covoyage/covo-agent/internal/cyber"
	"github.com/covoyage/covo-agent/internal/logutil"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
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
	if ca.lease.Busy() {
		t.Fatal("lease should be released after RunDirect")
	}
}

type cyberRoleRequestProvider struct {
	streamMockProvider
	tools []agentcore.ToolDefinition
}

func (provider *cyberRoleRequestProvider) Stream(ctx context.Context, request *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	provider.tools = append([]agentcore.ToolDefinition(nil), request.Tools...)
	return provider.streamMockProvider.Stream(ctx, request)
}

func TestCyberModelRequestUsesInstanceRole(t *testing.T) {
	for _, test := range []struct {
		name, role, contextRole string
		handsOn                 bool
		lateTools               bool
		lean                    bool
	}{
		{"implicit parent", "", "recon", false, false, false},
		{"parent", "parent", "recon", false, false, false},
		{"leaf", "leaf", "recon", false, false, false},
		{"recon", "recon", "parent", true, false, false},
		{"verifier", "verifier", "parent", true, false, false},
		{"late implicit parent", "", "recon", false, true, false},
		{"late parent", "parent", "recon", false, true, false},
		{"late leaf", "leaf", "recon", false, true, false},
		{"late recon", "recon", "parent", true, true, false},
		{"late verifier", "verifier", "parent", true, true, false},
		{"lean parent", "parent", "recon", false, false, true},
		{"late lean parent", "parent", "recon", false, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &cyberRoleRequestProvider{}
			var hooks []agentcore.LifecycleHook
			if test.lateTools {
				hooks = []agentcore.LifecycleHook{lateCyberToolsHook{}}
			}
			ca, err := NewCovoAgent(CovoAgentConfig{
				Mode: ModeCyber, SubagentRole: test.role,
				Provider: provider, ProviderName: "mock", Model: "mock-model",
				WorkingDir: t.TempDir(), HomeDir: t.TempDir(),
				ToolProfile: "full", ToolsetOverride: []string{"full"},
				LifecycleHooks: hooks,
				LeanTools:      test.lean,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { ca.Close() })
			ctx := toolssubagent.WithSubagentRole(context.Background(), test.contextRole)
			if _, err := ca.RunDirect(ctx, "Summarize the available tools without executing them."); err != nil {
				t.Fatal(err)
			}
			if provider.calls == 0 {
				t.Fatal("model request was not captured")
			}
			visible := make(map[string]bool)
			for _, tool := range provider.tools {
				visible[tool.Name] = true
				if !cyber.AllowOrchestratorTool(test.role, tool.Name) {
					t.Errorf("restricted tool %s exposed to role %q", tool.Name, test.role)
				}
			}
			for _, name := range []string{"bash", "scan", "execute_code"} {
				if visible[name] != test.handsOn {
					t.Errorf("tool %s visible=%v for instance role %q, context role %q", name, visible[name], test.role, test.contextRole)
				}
			}
			if !test.handsOn && test.role != "leaf" && !visible["sessions_spawn"] {
				t.Error("parent cannot delegate through sessions_spawn")
			}
		})
	}
}

type lateCyberToolsHook struct {
	agentcore.BaseLifecycleHook
}

func (lateCyberToolsHook) BeforeModelCall(_ context.Context, _ *agentcore.AgentRunContext, call *agentcore.ModelCallContext) error {
	call.Request.Tools = append(call.Request.Tools,
		agentcore.ToolDefinition{Name: "bash"},
		agentcore.ToolDefinition{Name: "scan"},
		agentcore.ToolDefinition{Name: "execute_code"},
	)
	return nil
}
