// Package harness provides a test harness for multi-turn agent scenarios.
package harness

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covo-agent/internal/agent"
)

// MockTurn defines one LLM response in the agent loop.
type MockTurn struct {
	Content   string
	ToolCalls []agentcore.ToolCall
}

// ScenarioStep defines one user message and the expected agent behavior.
type ScenarioStep struct {
	// User message to send to the agent.
	User string
	// Mock sequence: the mock provider cycles through these for each LLM call
	// during this step. Agent calls LLM → gets tool calls → executes → calls LLM again → ...
	Mock []MockTurn
}

// Scenario describes a test scenario.
type Scenario struct {
	Name         string
	WorkingDir   string // optional; defaults to a temp dir
	SystemPrompt string
	Steps        []ScenarioStep
}

// RecordedCall captures a provider.Complete invocation.
type RecordedCall struct {
	Request  *agentcore.ProviderRequest
	Response *agentcore.ProviderResponse
}

// Harness runs agent scenarios in tests.
type Harness struct {
	t      *testing.T
	Agent  *agent.CovoAgent
	mock   *mockProvider
	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a Harness for the given scenario.
func New(t *testing.T, sc *Scenario) *Harness {
	t.Helper()

	workDir := sc.WorkingDir
	if workDir == "" {
		workDir = t.TempDir()
	}
	homeDir := t.TempDir()

	mock := newMockProvider(sc)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	covoAgent, err := agent.NewCovoAgent(agent.CovoAgentConfig{
		Mode:         agent.ModeGeneral,
		Provider:     mock,
		ProviderName: "mock",
		Model:        "mock-model",
		WorkingDir:   workDir,
		HomeDir:      homeDir,
		Logger:       logger,
		ToolProfile:  "minimal",
		SystemPrompt: sc.SystemPrompt,
	})
	if err != nil {
		t.Fatalf("harness: create agent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Harness{
		t:      t,
		Agent:  covoAgent,
		mock:   mock,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Close cleans up the agent.
func (h *Harness) Close() {
	h.cancel()
	h.Agent.Close()
}

// Run executes the scenario.
func (h *Harness) Run() {
	h.t.Helper()
	defer h.Close()

	for _, step := range h.mock.scenario.Steps {
		_, err := h.Agent.Core().Run(h.ctx, step.User)
		if err != nil {
			h.t.Errorf("agent run (user=%q): %v", truncate(step.User, 60), err)
			return
		}
	}
}

// RecordedCalls returns all LLM calls made during the run.
func (h *Harness) RecordedCalls() []RecordedCall {
	return h.mock.snapshot()
}

// RequireToolCalled fails the test if the named tool was not called in any LLM response.
func (h *Harness) RequireToolCalled(name string) {
	h.t.Helper()
	for _, rc := range h.RecordedCalls() {
		for _, tc := range rc.Response.ToolCalls {
			if tc.Name == name {
				return
			}
		}
	}
	h.t.Errorf("expected tool %q to be called, but it was not", name)
}

// RequirePromptContains fails if none of the LLM call requests contain the substring.
func (h *Harness) RequirePromptContains(sub string) {
	h.t.Helper()
	for _, rc := range h.RecordedCalls() {
		for _, msg := range rc.Request.Messages {
			if contains(msg.Content, sub) {
				return
			}
		}
	}
	h.t.Errorf("expected prompt to contain %q, but none did", sub)
}

// --- mock provider ---

type mockProvider struct {
	scenario *Scenario
	stepIdx  int
	turnIdx  int
	recorded []RecordedCall
	mu       sync.Mutex
}

func newMockProvider(sc *Scenario) *mockProvider {
	return &mockProvider{scenario: sc}
}

func (m *mockProvider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.stepIdx = min(m.stepIdx, len(m.scenario.Steps)-1)
	step := m.scenario.Steps[m.stepIdx]

	var turn MockTurn
	if m.turnIdx < len(step.Mock) {
		turn = step.Mock[m.turnIdx]
		m.turnIdx++
	} else if m.turnIdx == len(step.Mock) && len(step.Mock) > 0 {
		m.stepIdx++
		m.turnIdx = 0
		if m.stepIdx < len(m.scenario.Steps) {
			step = m.scenario.Steps[m.stepIdx]
			turn = step.Mock[m.turnIdx]
			m.turnIdx++
		}
	}

	resp := &agentcore.ProviderResponse{
		Content:   turn.Content,
		ToolCalls: turn.ToolCalls,
		Usage:     agentcore.TokenUsage{},
	}
	m.recorded = append(m.recorded, RecordedCall{Request: req, Response: resp})
	return resp, nil
}

func (m *mockProvider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	resp, err := m.Complete(ctx, req)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamDelta, 1)
	ch <- agentcore.StreamDelta{
		Content:   resp.Content,
		ToolCalls: toolCallsToDeltas(resp.ToolCalls),
		Done:      true,
		Usage:     &resp.Usage,
	}
	close(ch)
	return ch, nil
}

func (m *mockProvider) snapshot() []RecordedCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RecordedCall, len(m.recorded))
	copy(out, m.recorded)
	return out
}

func toolCallsToDeltas(tcs []agentcore.ToolCall) []agentcore.ToolCallDelta {
	deltas := make([]agentcore.ToolCallDelta, len(tcs))
	for i, tc := range tcs {
		deltas[i] = agentcore.ToolCallDelta{
			Index:     int64(i),
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.Arguments,
		}
	}
	return deltas
}

func contains(s, sub string) bool {
	return s != "" && sub != "" && len(s) >= len(sub) && strContains(s, sub)
}

func strContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var _ agentcore.Provider = (*mockProvider)(nil)
