package compression

import (
	"context"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

// CompressionProviderSwitch is a provider wrapper that sits between
// agentcore's context engine and the main provider. When "compression mode"
// is active, it routes LLM calls to the auxiliary compression provider
// (if configured and different from the main provider). When inactive or
// when no auxiliary provider is configured, it transparently delegates to
// the main provider with all its middleware intact.
//
// This lets users configure auxiliary.compression.{provider,model} in
// config.yaml and have context compression use that provider instead of
// the main agent's provider — without modifying agentcore.
type CompressionProviderSwitch struct {
	mu          sync.Mutex
	main        agentcore.Provider
	auxProvider agentcore.Provider
	auxModel    string
	active      bool
}

// NewCompressionProviderSwitch creates a switch wrapping the given main
// provider. The auxiliary provider can be set later via SetAux.
func NewCompressionProviderSwitch(main agentcore.Provider) *CompressionProviderSwitch {
	return &CompressionProviderSwitch{main: main}
}

// SetAux configures the auxiliary compression provider and model. When
// auxProvider is nil or identical to main, the switch becomes a no-op
// (all calls go to main).
func (s *CompressionProviderSwitch) SetAux(auxProvider agentcore.Provider, auxModel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auxProvider = auxProvider
	s.auxModel = auxModel
}

// HasAux returns true when an auxiliary provider is configured and is
// different from the main provider.
func (s *CompressionProviderSwitch) HasAux() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.auxProvider != nil && s.auxProvider != s.main
}

// SetActive enables or disables compression routing. When active and an
// auxiliary provider is configured, Complete calls are routed to the aux
// provider. This is called by EnhancedContextEngine.Compress before
// delegating to the inner engine.
func (s *CompressionProviderSwitch) SetActive(active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = active
}

// Complete implements agentcore.Provider. When compression mode is active
// and an auxiliary provider is configured, routes to it; otherwise
// delegates to the main provider.
func (s *CompressionProviderSwitch) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	s.mu.Lock()
	aux := s.auxProvider
	auxModel := s.auxModel
	active := s.active
	s.mu.Unlock()

	if active && aux != nil {
		reqCopy := *req
		if auxModel != "" {
			reqCopy.Model = auxModel
		}
		// Guard against a self-referential aux. When only a model override is
		// configured (no separate provider), the resolved provider reuses this
		// switch; routing to it would recurse forever. Instead route to the
		// main provider with the override model.
		if aux == s {
			return s.main.Complete(ctx, &reqCopy)
		}
		return aux.Complete(ctx, &reqCopy)
	}
	return s.main.Complete(ctx, req)
}

// Stream implements agentcore.Provider by delegating to the main provider.
// Compression is a non-streaming operation, so streaming always goes to
// the main provider.
func (s *CompressionProviderSwitch) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	return s.main.Stream(ctx, req)
}
