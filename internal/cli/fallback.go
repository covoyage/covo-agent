package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/covoyage/covonaut/agentcore"
)

type FallbackProvider struct {
	providers []agentcore.Provider
	names     []string
	logger    *slog.Logger
}

func NewFallbackProvider(providers []agentcore.Provider, names []string, logger *slog.Logger) *FallbackProvider {
	if logger == nil {
		logger = slog.Default()
	}
	return &FallbackProvider{
		providers: providers,
		names:     names,
		logger:    logger,
	}
}

func (fp *FallbackProvider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	var lastErr error
	for i, p := range fp.providers {
		name := fp.names[i]
		resp, err := p.Complete(ctx, req)
		if err == nil {
			if i > 0 {
				fp.logger.Info("fallback provider succeeded",
					"primary", fp.names[0],
					"fallback", name,
					"attempt", i+1,
				)
			}
			return resp, nil
		}
		// Caller-side cancellation (Ctrl+C, deadline) is request-scoped, not a
		// provider health issue. Do NOT try the next provider — the caller has
		// already given up, and falling through would burn quota and delay the
		// cancellation. Surface the original error immediately.
		if ctx.Err() != nil {
			return nil, err
		}
		fp.logger.Warn("provider failed, trying fallback",
			"provider", name,
			"attempt", i+1,
			"error", err,
		)
		lastErr = err
	}
	return nil, fmt.Errorf("all providers failed: %w", lastErr)
}

func (fp *FallbackProvider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	var lastErr error
	for i, p := range fp.providers {
		name := fp.names[i]
		ch, err := p.Stream(ctx, req)
		if err == nil {
			if i > 0 {
				fp.logger.Info("fallback provider streaming",
					"primary", fp.names[0],
					"fallback", name,
					"attempt", i+1,
				)
			}
			return ch, nil
		}
		// Same rationale as Complete: caller cancellation is not a provider
		// failure. Do not fall through to the next provider.
		if ctx.Err() != nil {
			return nil, err
		}
		fp.logger.Warn("provider stream failed, trying fallback",
			"provider", name,
			"attempt", i+1,
			"error", err,
		)
		lastErr = err
	}
	return nil, fmt.Errorf("all providers failed: %w", lastErr)
}

func (fp *FallbackProvider) PrimaryName() string {
	if len(fp.names) > 0 {
		return fp.names[0]
	}
	return ""
}
