package slashcmd

import (
	"fmt"
	"strings"

	"github.com/covoyage/covo-agent/internal/marketplace"
)

// handleMarketplace provides a slash command interface to the plugin marketplace.
//
// Usage:
//
//	/marketplace                    — list all available plugins
//	/marketplace <type>             — list plugins of a specific type (skill, mcp, command, agent, hook)
//	/marketplace search <query>     — search for plugins
//	/marketplace install <name>     — install a plugin
//	/marketplace uninstall <name>   — uninstall a plugin
//	/marketplace categories         — list available plugin types
func handleMarketplace(sctx *SlashContext, parts []string) bool {
	if sctx.Services.HomeDir == "" {
		sctx.UI.App.PrintSystem("(home directory not available)")
		return true
	}

	mkt := marketplace.New(sctx.Services.HomeDir, "")

	if len(parts) < 2 {
		// List all
		return listMarketplace(sctx, mkt, "")
	}

	sub := parts[1]

	switch sub {
	case "search":
		if len(parts) < 3 {
			sctx.UI.App.PrintSystem("Usage: /marketplace search <query>")
			return true
		}
		query := strings.TrimSpace(strings.TrimPrefix(sctx.Input, parts[0]))
		query = strings.TrimSpace(strings.TrimPrefix(query, "search"))
		return searchMarketplace(sctx, mkt, query)

	case "install":
		if len(parts) < 3 {
			sctx.UI.App.PrintSystem("Usage: /marketplace install <name>")
			return true
		}
		name := parts[2]
		sctx.UI.App.PrintSystem(fmt.Sprintf("Installing %s...", name))
		path, err := mkt.Install(name)
		if err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("install: %w", err))
			return true
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("✅ Installed: %s", path))
		return true

	case "uninstall", "remove":
		if len(parts) < 3 {
			sctx.UI.App.PrintSystem("Usage: /marketplace uninstall <name>")
			return true
		}
		name := parts[2]
		if err := mkt.Uninstall(name); err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("uninstall: %w", err))
			return true
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("✅ Uninstalled: %s", name))
		return true

	case "categories":
		cats := mkt.Categories()
		if len(cats) == 0 {
			sctx.UI.App.PrintSystem("(no categories available — check your marketplace URL)")
			return true
		}
		sctx.UI.App.PrintSystem("── Marketplace Categories ──")
		for _, c := range cats {
			sctx.UI.App.PrintSystem(fmt.Sprintf("  • %s", c))
		}
		return true

	default:
		// Try as type filter
		pt := marketplace.PluginType(sub)
		switch pt {
		case marketplace.PluginTypeSkill, marketplace.PluginTypeMCP,
			marketplace.PluginTypeCommand, marketplace.PluginTypeAgent,
			marketplace.PluginTypeHook, marketplace.PluginTypePlatform:
			return listMarketplace(sctx, mkt, pt)
		}
		sctx.UI.App.PrintSystem(strings.Join([]string{
			"Usage:",
			"  /marketplace                    — list all plugins",
			"  /marketplace <type>             — list by type (skill, mcp, command, agent, hook)",
			"  /marketplace search <query>     — search plugins",
			"  /marketplace install <name>     — install a plugin",
			"  /marketplace uninstall <name>   — uninstall a plugin",
			"  /marketplace categories         — list plugin types",
		}, "\n"))
		return true
	}
}

func listMarketplace(sctx *SlashContext, mkt *marketplace.Marketplace, filterType marketplace.PluginType) bool {
	entries, err := mkt.List(filterType)
	if err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("marketplace: %w", err))
		return true
	}

	if len(entries) == 0 {
		sctx.UI.App.PrintSystem("(no plugins found in marketplace)")
		return true
	}

	sctx.UI.App.PrintSystem(fmt.Sprintf("── Marketplace (%d plugins) ──", len(entries)))
	for _, e := range entries {
		status := "○"
		if e.Installed {
			status = "✓"
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("  %s [%s] %s — %s", status, e.Type, e.Name, truncate(e.Description, 60)))
	}
	sctx.UI.App.PrintSystem("Use /marketplace install <name> to install.")
	return true
}

func searchMarketplace(sctx *SlashContext, mkt *marketplace.Marketplace, query string) bool {
	results, err := mkt.Search(query)
	if err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("marketplace search: %w", err))
		return true
	}

	if len(results) == 0 {
		sctx.UI.App.PrintSystem(fmt.Sprintf("(no plugins matching %q)", query))
		return true
	}

	sctx.UI.App.PrintSystem(fmt.Sprintf("── Search: %q (%d results) ──", query, len(results)))
	for _, e := range results {
		status := "○"
		if e.Installed {
			status = "✓"
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("  %s [%s] %s — %s", status, e.Type, e.Name, truncate(e.Description, 60)))
	}
	return true
}
