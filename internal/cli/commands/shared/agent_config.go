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

func SkillExtraDirs(cfg *cli.Config) []string {
	if cfg == nil || cfg.Skills == nil {
		return nil
	}
	return append([]string(nil), cfg.Skills.AdditionalDirs...)
}

func SkillDisabled(cfg *cli.Config) []string {
	if cfg == nil || cfg.Skills == nil {
		return nil
	}
	return append([]string(nil), cfg.Skills.Disabled...)
}

func SkillConfigFns(cfg *cli.Config) (func() map[string]string, func(key, value string) error) {
	get := func() map[string]string {
		loaded, err := cli.LoadConfig()
		if err != nil || loaded == nil || loaded.Skills == nil {
			if cfg != nil && cfg.Skills != nil {
				return cfg.Skills.Config
			}
			return nil
		}
		return loaded.Skills.Config
	}
	set := func(key, value string) error {
		loaded, err := cli.LoadConfig()
		if err != nil {
			return err
		}
		if loaded.Skills == nil {
			loaded.Skills = &cli.SkillsConfig{}
		}
		if loaded.Skills.Config == nil {
			loaded.Skills.Config = make(map[string]string)
		}
		loaded.Skills.Config[key] = value
		if cfg != nil {
			if cfg.Skills == nil {
				cfg.Skills = &cli.SkillsConfig{}
			}
			if cfg.Skills.Config == nil {
				cfg.Skills.Config = make(map[string]string)
			}
			cfg.Skills.Config[key] = value
		}
		return cli.SaveConfig(loaded)
	}
	return get, set
}

// LeanToolsEnabled is true when long-tail tool schemas should be hidden.
// Default (auto / unset): on for code mode, or when more than one MCP server is configured.
func LeanToolsEnabled(cfg *cli.Config) bool {
	mode := ""
	if cfg != nil && cfg.Tools != nil {
		mode = strings.ToLower(strings.TrimSpace(cfg.Tools.Lean))
	}
	if env := strings.ToLower(strings.TrimSpace(os.Getenv("COVO_TOOLS_LEAN"))); env != "" {
		mode = env
	}
	switch mode {
	case "on", "true", "1", "yes":
		return true
	case "off", "false", "0", "no":
		return false
	default:
		if cfg != nil && strings.EqualFold(strings.TrimSpace(cfg.Mode), "code") {
			return true
		}
		return cfg != nil && len(cfg.MCPServers) > 1
	}
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
