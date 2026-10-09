package shared

import (
	"os"
	"strings"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/covoyage/covo-agent/internal/plugin"
	agenttools "github.com/covoyage/covo-agent/internal/tools"
	"github.com/covoyage/covo-agent/internal/workspace"
)

// RegisterCustomModes publishes config-defined modes so RequireMode and
// custom-mode prompts work for TUI, oneshot, exec, cron, ACP, gateway, and MCP.
func RegisterCustomModes(cfg *cli.Config) {
	if cfg == nil {
		return
	}
	for _, cm := range cfg.CustomModes {
		def := &agent.CustomModeDefinition{
			Name:         cm.Name,
			Description:  cm.Description,
			SystemPrompt: cm.SystemPrompt,
		}
		if cm.Tools != nil {
			def.AllowTools = cm.Tools.Allow
			def.DenyTools = cm.Tools.Deny
		}
		agent.RegisterCustomMode(def)
	}
}

func MCPAgentConfig(cfg *cli.Config) map[string]agenttools.MCPConfig {
	m := map[string]agenttools.MCPConfig{}
	if cfg != nil && cfg.MCPServers != nil {
		for name, srv := range cfg.MCPServers {
			m[name] = agenttools.MCPConfig{
				Command: srv.Command,
				Args:    srv.Args,
				Env:     srv.Env,
				Timeout: srv.Timeout,
			}
		}
	}
	home, err := cli.EnsureHomeDir()
	if err == nil {
		for name, def := range plugin.DiscoverHome(home).MCPServers() {
			if _, exists := m[name]; exists {
				continue
			}
			m[name] = agenttools.MCPConfig{
				Command: def.Command,
				Args:    def.Args,
				Env:     def.Env,
				Timeout: def.Timeout,
			}
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func RemoteFromFlag(raw string) *workspace.RemoteSpec {
	spec, err := workspace.ParseRemote(raw)
	if err != nil {
		return nil
	}
	return spec
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
// Default (auto / unset): on for code/cyber mode, or when more than one MCP server is configured.
func LeanToolsEnabled(cfg *cli.Config) bool {
	modeName := ""
	if cfg != nil {
		modeName = cfg.Mode
	}
	return LeanToolsEnabledFor(cfg, modeName)
}

// LeanToolsEnabledFor is LeanToolsEnabled using an explicit runtime mode
// (oneshot/headless --mode, or a TUI /mode switch) instead of only cfg.Mode.
func LeanToolsEnabledFor(cfg *cli.Config, modeName string) bool {
	lean := ""
	if cfg != nil && cfg.Tools != nil {
		lean = strings.ToLower(strings.TrimSpace(cfg.Tools.Lean))
	}
	if env := strings.ToLower(strings.TrimSpace(os.Getenv("COVO_TOOLS_LEAN"))); env != "" {
		lean = env
	}
	switch lean {
	case "on", "true", "1", "yes":
		return true
	case "off", "false", "0", "no":
		return false
	default:
		switch strings.ToLower(strings.TrimSpace(modeName)) {
		case "code", "cyber":
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
