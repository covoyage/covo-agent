package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

// mockAuxProvider is a minimal agentcore.Provider for testing.
type mockAuxProvider struct {
	mu      sync.Mutex
	content string
	calls   int
}

var _ agentcore.Provider = (*mockAuxProvider)(nil)

func (m *mockAuxProvider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	return &agentcore.ProviderResponse{Content: m.content}, nil
}

func (m *mockAuxProvider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	return nil, errors.New("not implemented")
}

func (m *mockAuxProvider) callsCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func TestAuxiliaryClient_FallbackToMain(t *testing.T) {
	main := &mockAuxProvider{content: "main-response"}
	ac := NewAuxiliaryClient(main, "gpt-4", nil, nil, nil)

	// Without any auxiliary config, all tasks should use the main provider.
	resp, err := ac.Complete(context.Background(), TaskTitle, "system", "user", 100, 0.3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "main-response" {
		t.Fatalf("expected main-response, got %s", resp)
	}
	if main.callsCount() != 1 {
		t.Fatalf("expected 1 call on main, got %d", main.callsCount())
	}
}

func TestAuxiliaryClient_ModelOnlyOverride(t *testing.T) {
	main := &mockAuxProvider{content: "main-response"}
	auxCfg := &AuxiliaryConfig{
		Title: &AuxiliaryModelConfig{Model: "gpt-5.6"},
	}
	ac := NewAuxiliaryClient(main, "gpt-4", auxCfg, nil, nil)

	// Title task should use the main provider but with the overridden model.
	if ac.Model(TaskTitle) != "gpt-5.6" {
		t.Fatalf("expected gpt-5.6, got %s", ac.Model(TaskTitle))
	}

	// Review task (not configured) should fall back to main provider+model.
	if ac.Model(TaskReview) != "gpt-4" {
		t.Fatalf("expected gpt-4, got %s", ac.Model(TaskReview))
	}

	// Complete on title should use main provider.
	resp, err := ac.Complete(context.Background(), TaskTitle, "system", "user", 100, 0.3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "main-response" {
		t.Fatalf("expected main-response, got %s", resp)
	}
}

func TestAuxiliaryClient_FullProviderOverride(t *testing.T) {
	main := &mockAuxProvider{content: "main-response"}
	aux := &mockAuxProvider{content: "aux-response"}

	builder := func(providerType, baseURL, apiKey string) (agentcore.Provider, error) {
		return aux, nil
	}
	auxCfg := &AuxiliaryConfig{
		Title: &AuxiliaryModelConfig{
			Provider: "openai",
			Model:    "gpt-5.6",
			BaseURL:  "http://localhost:8080",
			APIKey:   "test-key",
		},
	}
	ac := NewAuxiliaryClient(main, "gpt-4", auxCfg, builder, nil)

	// Title task should use the aux provider.
	if ac.Model(TaskTitle) != "gpt-5.6" {
		t.Fatalf("expected gpt-5.6, got %s", ac.Model(TaskTitle))
	}

	resp, err := ac.Complete(context.Background(), TaskTitle, "system", "user", 100, 0.3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "aux-response" {
		t.Fatalf("expected aux-response, got %s", resp)
	}
	if main.callsCount() != 0 {
		t.Fatalf("expected 0 calls on main, got %d", main.callsCount())
	}
	if aux.callsCount() != 1 {
		t.Fatalf("expected 1 call on aux, got %d", aux.callsCount())
	}

	// Review task (not configured) should fall back to main.
	resp2, err := ac.Complete(context.Background(), TaskReview, "system", "user", 100, 0.3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp2 != "main-response" {
		t.Fatalf("expected main-response, got %s", resp2)
	}
}

func TestAuxiliaryClient_SetMainProvider(t *testing.T) {
	main1 := &mockAuxProvider{content: "main1"}
	main2 := &mockAuxProvider{content: "main2"}

	auxCfg := &AuxiliaryConfig{
		Title: &AuxiliaryModelConfig{Model: "gpt-5.6"},
	}
	ac := NewAuxiliaryClient(main1, "gpt-4", auxCfg, nil, nil)

	// Initially title uses main1 with the configured lightweight model.
	if ac.Model(TaskTitle) != "gpt-5.6" {
		t.Fatalf("expected gpt-5.6, got %s", ac.Model(TaskTitle))
	}

	// Switch main provider.
	ac.SetMainProvider(main2, "gpt-4o")

	// Model-only override should be cleared, falling back to main2.
	resp, err := ac.Complete(context.Background(), TaskTitle, "system", "user", 100, 0.3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "main2" {
		t.Fatalf("expected main2 response, got %s", resp)
	}
	if ac.Model(TaskTitle) != "gpt-4o" {
		t.Fatalf("expected gpt-4o, got %s", ac.Model(TaskTitle))
	}
}

func TestAuxiliaryClient_NoProviderAvailable(t *testing.T) {
	ac := NewAuxiliaryClient(nil, "", nil, nil, nil)

	_, err := ac.Complete(context.Background(), TaskTitle, "system", "user", 100, 0.3)
	if err == nil {
		t.Fatal("expected error when no provider available")
	}
	if !strings.Contains(err.Error(), "no provider available") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestAuxiliaryClient_HasProvider(t *testing.T) {
	main := &mockAuxProvider{content: "main"}
	ac := NewAuxiliaryClient(main, "gpt-4", nil, nil, nil)

	if !ac.HasProvider(TaskTitle) {
		t.Fatal("expected HasProvider to return true when main provider is set")
	}

	ac2 := NewAuxiliaryClient(nil, "", nil, nil, nil)
	if ac2.HasProvider(TaskTitle) {
		t.Fatal("expected HasProvider to return false when no provider is set")
	}
}
