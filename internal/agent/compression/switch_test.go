package compression

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

// mockSwitchProvider is a minimal agentcore.Provider for testing the switch.
type mockSwitchProvider struct {
	mu      sync.Mutex
	content string
	calls   int
	model   string
}

var _ agentcore.Provider = (*mockSwitchProvider)(nil)

func (m *mockSwitchProvider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.model = req.Model
	return &agentcore.ProviderResponse{Content: m.content}, nil
}

func (m *mockSwitchProvider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	return nil, errors.New("not implemented")
}

func (m *mockSwitchProvider) callsCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *mockSwitchProvider) lastModel() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.model
}

func TestCompressionSwitch_NoAux_DelegatesToMain(t *testing.T) {
	main := &mockSwitchProvider{content: "main-response"}
	s := NewCompressionProviderSwitch(main)

	// No aux set — should always delegate to main.
	resp, err := s.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:     "gpt-4",
		Messages:  []agentcore.Message{{Role: agentcore.RoleUser, Content: "test"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "main-response" {
		t.Fatalf("expected main-response, got %s", resp.Content)
	}
	if main.callsCount() != 1 {
		t.Fatalf("expected 1 call on main, got %d", main.callsCount())
	}

	// Even when active, no aux → delegates to main.
	s.SetActive(true)
	resp2, _ := s.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "gpt-4",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "test"}},
	})
	if resp2.Content != "main-response" {
		t.Fatalf("expected main-response when active but no aux, got %s", resp2.Content)
	}
}

func TestCompressionSwitch_AuxActive_RoutesToAux(t *testing.T) {
	main := &mockSwitchProvider{content: "main-response"}
	aux := &mockSwitchProvider{content: "aux-response"}

	s := NewCompressionProviderSwitch(main)
	s.SetAux(aux, "gpt-5.6")

	// Inactive → delegates to main.
	resp, _ := s.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "gpt-4",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "test"}},
	})
	if resp.Content != "main-response" {
		t.Fatalf("expected main-response when inactive, got %s", resp.Content)
	}
	if aux.callsCount() != 0 {
		t.Fatalf("expected 0 calls on aux when inactive, got %d", aux.callsCount())
	}

	// Active → routes to aux with aux model.
	s.SetActive(true)
	resp2, _ := s.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "gpt-4",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "test"}},
	})
	if resp2.Content != "aux-response" {
		t.Fatalf("expected aux-response when active, got %s", resp2.Content)
	}
	if aux.callsCount() != 1 {
		t.Fatalf("expected 1 call on aux, got %d", aux.callsCount())
	}
	// Aux model should override the request model.
	if aux.lastModel() != "gpt-5.6" {
		t.Fatalf("expected aux model gpt-5.6, got %s", aux.lastModel())
	}

	// Deactivate → back to main.
	s.SetActive(false)
	resp3, _ := s.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "gpt-4",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "test"}},
	})
	if resp3.Content != "main-response" {
		t.Fatalf("expected main-response after deactivate, got %s", resp3.Content)
	}
}

func TestCompressionSwitch_HasAux(t *testing.T) {
	main := &mockSwitchProvider{content: "main"}
	s := NewCompressionProviderSwitch(main)

	if s.HasAux() {
		t.Fatal("expected HasAux=false before SetAux")
	}

	aux := &mockSwitchProvider{content: "aux"}
	s.SetAux(aux, "gpt-5.6")

	if !s.HasAux() {
		t.Fatal("expected HasAux=true after SetAux")
	}

	// SetAux with same provider as main → HasAux should be false.
	s.SetAux(main, "gpt-5.6")
	if s.HasAux() {
		t.Fatal("expected HasAux=false when aux==main")
	}
}

// TestCompressionSwitch_SelfReferenceGuard verifies that when the switch is
// given itself as the aux provider (which happens in the agent wiring for a
// model-only/absent auxiliary.compression override, where the resolved
// provider falls back to the switch) it routes to its main provider with the
// override model instead of recursing into itself forever.
func TestCompressionSwitch_SelfReferenceGuard(t *testing.T) {
	main := &mockSwitchProvider{content: "main-response"}
	s := NewCompressionProviderSwitch(main)

	// Simulate the agent wiring: SetAux with the switch itself + a model.
	s.SetAux(s, "gpt-5.6")
	s.SetActive(true)

	resp, err := s.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    "gpt-4",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "test"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "main-response" {
		t.Fatalf("expected main-response (no self-recursion), got %s", resp.Content)
	}
	// The override model should still be applied to the main call.
	if main.callsCount() != 1 {
		t.Fatalf("expected 1 call on main (no infinite recursion), got %d", main.callsCount())
	}
	if main.lastModel() != "gpt-5.6" {
		t.Fatalf("expected override model gpt-5.6 on main, got %s", main.lastModel())
	}
}

func TestCompressionSwitch_StreamAlwaysGoesToMain(t *testing.T) {
	main := &mockSwitchProvider{content: "main"}
	aux := &mockSwitchProvider{content: "aux"}

	s := NewCompressionProviderSwitch(main)
	s.SetAux(aux, "gpt-5.6")
	s.SetActive(true)

	// Stream should always go to main (compression is non-streaming).
	_, _ = s.Stream(context.Background(), &agentcore.ProviderRequest{
		Model:    "gpt-4",
		Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "test"}},
	})
	if aux.callsCount() != 0 {
		t.Fatalf("expected 0 calls on aux for Stream, got %d", aux.callsCount())
	}
}
