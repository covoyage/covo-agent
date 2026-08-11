package gateway

import "github.com/covoyage/covo-agent/internal/plugin"

func newGatewayFromConfig(cfg Config) *Gateway {
	platforms := append([]plugin.PlatformProvider(nil), cfg.Platforms...)
	cfg.Platforms = append([]plugin.PlatformProvider(nil), platforms...)
	return &Gateway{
		cfg:          cfg,
		cache:        NewAgentCache(cfg.AgentCacheSize, cfg.AgentIdleTTL, cfg.AgentFactory),
		pairing:      cfg.PairingStore,
		suspendStore: cfg.SuspendStore,
		platforms:    platforms,
	}
}

func (g *Gateway) configuredPlatforms() []plugin.PlatformProvider {
	return g.platforms
}
