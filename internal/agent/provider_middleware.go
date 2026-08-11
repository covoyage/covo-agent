package agent

import (
	"context"
	"log/slog"

	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covonaut/agentcore"
)

// ProviderMiddleware wraps an agentcore.Provider to add cross-cutting behavior.
type ProviderMiddleware func(agentcore.Provider) agentcore.Provider

// ApplyProviderMiddleware chains middlewares onto a provider.
// The first middleware wraps the outermost layer.
func ApplyProviderMiddleware(p agentcore.Provider, mws []ProviderMiddleware) agentcore.Provider {
	for _, mw := range mws {
		p = mw(p)
	}
	return p
}

// ---------- CostTrackingMiddleware ----------

type costTrackingMiddleware struct {
	tracker *CostTracker
	inner   agentcore.Provider
	logger  *slog.Logger
}

func (m *costTrackingMiddleware) recordUsage(usage *agentcore.TokenUsage) {
	if usage == nil || (usage.PromptTokens == 0 && usage.CompletionTokens == 0) {
		return
	}
	m.tracker.RecordUsage(&CanonicalUsage{
		InputTokens:      int(usage.PromptTokens),
		OutputTokens:     int(usage.CompletionTokens),
		CacheReadTokens:  0,
		CacheWriteTokens: 0,
		RequestCount:     1,
	})
	// Record raw prompt_tokens for context window percentage display.
	m.tracker.RecordPromptTokens(usage.PromptTokens)
}

func (m *costTrackingMiddleware) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	resp, err := m.inner.Complete(ctx, req)
	if err == nil && resp != nil {
		u := resp.Usage
		m.recordUsage(&u)
	}
	return resp, err
}

func (m *costTrackingMiddleware) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	stream, err := m.inner.Stream(ctx, req)
	if err != nil {
		return stream, err
	}

	out := make(chan agentcore.StreamDelta)
	safego.SafeGo(func() {
		defer close(out)
		var usage *agentcore.TokenUsage
		for delta := range stream {
			if delta.Usage != nil {
				usage = delta.Usage
			}
			out <- delta
		}
		m.recordUsage(usage)
	}, m.logger)
	return out, nil
}

func NewCostTrackingMiddleware(tracker *CostTracker, logger *slog.Logger) ProviderMiddleware {
	return func(inner agentcore.Provider) agentcore.Provider {
		return &costTrackingMiddleware{tracker: tracker, inner: inner, logger: logger}
	}
}

// ---------- RateLimitTrackingMiddleware ----------

type rateLimitTrackingMiddleware struct {
	state *RateLimitState
	inner agentcore.Provider
}

func (m *rateLimitTrackingMiddleware) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	return m.inner.Complete(ctx, req)
}

func (m *rateLimitTrackingMiddleware) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	return m.inner.Stream(ctx, req)
}

func NewRateLimitTrackingMiddleware(state *RateLimitState) ProviderMiddleware {
	return func(inner agentcore.Provider) agentcore.Provider {
		return &rateLimitTrackingMiddleware{state: state, inner: inner}
	}
}

// ---------- PromptCachingMiddleware (adapter) ----------

func NewPromptCachingMiddleware(enabled bool, cacheTTL string) ProviderMiddleware {
	return func(inner agentcore.Provider) agentcore.Provider {
		return &PromptCachingMiddleware{
			Enabled:  enabled,
			CacheTTL: cacheTTL,
			Provider: inner,
		}
	}
}
