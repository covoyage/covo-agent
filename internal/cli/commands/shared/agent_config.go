package shared

import (
	"os"
	"strings"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/evolution"
	agenttools "github.com/covoyage/covo-agent/internal/tools"
)

func MCPAgentConfig(cfg *cli.Config) map[string]agenttools.MCPConfig {
	if cfg == nil || cfg.MCPServers == nil {
		return nil
	}
	m := make(map[string]agenttools.MCPConfig, len(cfg.MCPServers))
	for name, srv := range cfg.MCPServers {
		m[name] = agenttools.MCPConfig{
			Command: srv.Command,
			Args:    srv.Args,
			Env:     srv.Env,
			Timeout: srv.Timeout,
		}
	}
	return m
}

// AuxiliaryConfigFromCLI converts cli.AuxiliaryConfig to agent.AuxiliaryConfig.
// Returns nil when no auxiliary config is set, so the agent falls back to the
// main provider/model for all auxiliary tasks.
func AuxiliaryConfigFromCLI(cfg *cli.Config) *agent.AuxiliaryConfig {
	if cfg == nil || cfg.Auxiliary == nil {
		return nil
	}
	return &agent.AuxiliaryConfig{
		Compression: convertAuxModel(cfg.Auxiliary.Compression),
		Vision:      convertAuxModel(cfg.Auxiliary.Vision),
		WebExtract:  convertAuxModel(cfg.Auxiliary.WebExtract),
		Title:       convertAuxModel(cfg.Auxiliary.Title),
		Review:      convertAuxModel(cfg.Auxiliary.Review),
	}
}

func convertAuxModel(m *cli.AuxiliaryModelConfig) *agent.AuxiliaryModelConfig {
	if m == nil {
		return nil
	}
	return &agent.AuxiliaryModelConfig{
		Model:    m.Model,
		Provider: m.Provider,
		BaseURL:  m.BaseURL,
		APIKey:   m.APIKey,
	}
}

func Truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

func CuratorConfig(cfg *cli.Config) evolution.CuratorConfig {
	if cfg.Curator != nil {
		return evolution.CuratorConfig{
			Enabled:          cfg.Curator.Enabled,
			IntervalHours:    cfg.Curator.IntervalHours,
			StaleAfterDays:   cfg.Curator.StaleAfterDays,
			ArchiveAfterDays: cfg.Curator.ArchiveAfterDays,
		}
	}
	return evolution.CuratorConfig{
		Enabled:          true,
		IntervalHours:    168,
		StaleAfterDays:   30,
		ArchiveAfterDays: 90,
	}
}

func ParseFallbackProviders() []string {
	raw := os.Getenv("FALLBACK_PROVIDER")
	if raw == "" {
		raw = os.Getenv("FALLBACK_PROVIDERS")
	}
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
