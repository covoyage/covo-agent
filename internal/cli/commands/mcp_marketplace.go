package commands

import (
	"fmt"
	"github.com/covoyage/covo-agent/internal/cli"
	"strings"

	"github.com/covoyage/covonaut/tui/chat"

	"github.com/covoyage/covo-agent/internal/mcpserver"
	agentpanels "github.com/covoyage/covo-agent/internal/tui/panels"
)

// openMCPMarketplace creates and displays the MCP marketplace panel.
func openMCPMarketplace() {
	// Load current config to check which servers are already configured
	cfg, err := cli.LoadConfig()
	configured := make(map[string]bool)
	if err == nil && cfg != nil {
		for name := range cfg.MCPServers {
			configured[strings.ToLower(name)] = true
		}
	}

	entries := toPanelMCPEntries(mcpserver.AllEntries())
	categories := mcpserver.Categories()
	panel := agentpanels.NewMCPMarketplacePanel(entries, categories, configured)
	var ov chat.OverlayRef
	closeOverlay := func() {
		loadUIBus().ClosePanel(ov)
	}
	panel.SetOnCancel(closeOverlay)
	panel.SetOnInstall(func(entry agentpanels.MCPEntry) {
		closeOverlay()
		installMCPServerFromPanel(entry)
	})
	ov = loadUIBus().ShowPanel(panel, 80, 80)
}

func toPanelMCPEntries(entries []mcpserver.RegistryEntry) []agentpanels.MCPEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]agentpanels.MCPEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, agentpanels.MCPEntry{
			Name:        e.Name,
			DisplayName: e.DisplayName,
			Description: e.Description,
			Category:    e.Category,
			Command:     e.Command,
			Args:        append([]string(nil), e.Args...),
			EnvVars:     append([]string(nil), e.EnvVars...),
		})
	}
	return out
}

func installMCPServerFromPanel(entry agentpanels.MCPEntry) {
	installMCPServer(mcpserver.RegistryEntry{
		Name:        entry.Name,
		DisplayName: entry.DisplayName,
		Description: entry.Description,
		Category:    entry.Category,
		Command:     entry.Command,
		Args:        append([]string(nil), entry.Args...),
		EnvVars:     append([]string(nil), entry.EnvVars...),
	})
}

// installMCPServer adds an MCP server to the user's config.yaml.
func installMCPServer(entry mcpserver.RegistryEntry) {
	cfg, err := cli.LoadConfig()
	if err != nil {
		loadUIBus().PrintError(fmt.Errorf("load config: %w", err))
		return
	}

	if cfg.MCPServers == nil {
		cfg.MCPServers = make(map[string]cli.MCPServerConfig)
	}

	// Check if already installed
	if _, exists := cfg.MCPServers[entry.Name]; exists {
		loadUIBus().PrintSystem(fmt.Sprintf("MCP server %q is already configured.", entry.Name))
		return
	}

	// Add the server, including required env var names so the MCP
	// launcher knows which environment variables to pass through.
	serverCfg := cli.MCPServerConfig{
		Command: entry.Command,
		Args:    entry.Args,
	}
	for _, ev := range entry.EnvVars {
		serverCfg.Env = append(serverCfg.Env, ev)
	}
	cfg.MCPServers[entry.Name] = serverCfg

	if err := cli.SaveConfig(cfg); err != nil {
		loadUIBus().PrintError(fmt.Errorf("save config: %w", err))
		return
	}

	// Build success message
	msg := fmt.Sprintf("✅ Installed MCP server: %s (%s)\n   Command: %s %s\n   Restart the agent to activate.",
		entry.DisplayName, entry.Name, entry.Command, strings.Join(entry.Args, " "))

	if len(entry.EnvVars) > 0 {
		msg += "\n   ⚠️  Required env vars: " + strings.Join(entry.EnvVars, ", ")
	}

	loadUIBus().PrintSystem(msg)
}
