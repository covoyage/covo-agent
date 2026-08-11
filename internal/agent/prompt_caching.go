package agent

import (
	"context"

	"github.com/covoyage/covonaut/agentcore"
)

func ApplyPromptCaching(req *agentcore.ProviderRequest, cacheTTL string) {
	marker := buildCacheMarker(cacheTTL)
	if marker == nil {
		return
	}

	messages := req.Messages
	if len(messages) == 0 {
		return
	}

	breakpointsUsed := 0
	maxBreakpoints := 4

	if messages[0].Role == "system" {
		if messages[0].CacheControl == nil {
			messages[0].CacheControl = marker
		}
		breakpointsUsed++
	}

	remaining := maxBreakpoints - breakpointsUsed
	nonSys := make([]int, 0)
	for i := range messages {
		if messages[i].Role != "system" {
			nonSys = append(nonSys, i)
		}
	}

	start := 0
	if len(nonSys) > remaining {
		start = len(nonSys) - remaining
	}
	for _, idx := range nonSys[start:] {
		if messages[idx].CacheControl == nil {
			messages[idx].CacheControl = marker
		}
	}
}

func buildCacheMarker(ttl string) *agentcore.CacheControlMarker {
	if ttl == "" {
		ttl = "5m"
	}
	marker := &agentcore.CacheControlMarker{
		Type: "ephemeral",
	}
	if ttl == "1h" {
		marker.TTL = "1h"
	}
	return marker
}

type PromptCachingMiddleware struct {
	Enabled  bool
	CacheTTL string
	Provider agentcore.Provider
}

func (m *PromptCachingMiddleware) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	if m.Enabled {
		ApplyPromptCaching(req, m.CacheTTL)
	}
	return m.Provider.Complete(ctx, req)
}

func (m *PromptCachingMiddleware) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	if m.Enabled {
		ApplyPromptCaching(req, m.CacheTTL)
	}
	return m.Provider.Stream(ctx, req)
}
