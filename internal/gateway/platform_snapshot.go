package gateway

import "github.com/covoyage/covo-agent/internal/plugin"

func newGatewayFromConfig(cfg Config) *Gateway {
	platforms := append([]plugin.PlatformProvider(nil), cfg.Platforms...)
	cfg.Platforms = append([]plugin.PlatformProvider(nil), platforms...)
	platformMap := make(map[string]plugin.PlatformProvider, len(platforms))
	for _, p := range platforms {
		platformMap[p.Name()] = p
	}
	return &Gateway{
		cfg:          cfg,
		cache:        NewAgentCache(cfg.AgentCacheSize, cfg.AgentIdleTTL, cfg.AgentFactory),
		pairing:      cfg.PairingStore,
		suspendStore: cfg.SuspendStore,
		platforms:    platforms,
		platformMap:  platformMap,
		events:       NewEventLog(),
		perms:        NewPermissionStore(),
	}
}

func (g *Gateway) configuredPlatforms() []plugin.PlatformProvider {
	return g.platforms
}
