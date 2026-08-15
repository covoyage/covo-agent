package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/covoyage/covo-agent/internal/plugin/builtin"
	"github.com/spf13/cobra"
)

func newPluginCommand(runtime *commandRuntime) *cobra.Command {
	pluginCmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	pluginCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List enabled platform plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPluginSystem(runtime.homeDir, cmd.ErrOrStderr(), func(system *plugin.System) error {
				writeEnabledPlugins(cmd.OutOrStdout(), system.Registry)
				return nil
			})
		},
	})
	pluginCmd.AddCommand(&cobra.Command{
		Use:   "info <name>",
		Short: "Show plugin details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPluginSystem(runtime.homeDir, cmd.ErrOrStderr(), func(system *plugin.System) error {
				return writePluginInfo(cmd.OutOrStdout(), system.Registry, args[0])
			})
		},
	})
	pluginCmd.AddCommand(newPluginToggleCommand(runtime, true))
	pluginCmd.AddCommand(newPluginToggleCommand(runtime, false))
	pluginCmd.AddCommand(&cobra.Command{
		Use:   "marketplace",
		Short: "List available platform plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPluginSystem(runtime.homeDir, cmd.ErrOrStderr(), func(system *plugin.System) error {
				writePluginMarketplace(cmd.OutOrStdout(), system.Registry)
				return nil
			})
		},
	})

	return pluginCmd
}

func newPluginToggleCommand(runtime *commandRuntime, enable bool) *cobra.Command {
	action := "disable"
	if enable {
		action = "enable"
	}
	return &cobra.Command{
		Use:       action + " <name>",
		Short:     strings.ToUpper(action[:1]) + action[1:] + " a platform plugin",
		Args:      cobra.ExactArgs(1),
		ValidArgs: builtin.Names(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPluginSystem(runtime.homeDir, cmd.ErrOrStderr(), func(system *plugin.System) error {
				return setPluginEnabled(cmd.OutOrStdout(), system, args[0], enable)
			})
		},
	}
}

func withPluginSystem(homeDir string, logOutput io.Writer, run func(*plugin.System) error) error {
	logger := slog.New(slog.NewTextHandler(logOutput, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelWarn)}))
	system, err := plugin.NewSystem(context.Background(), plugin.SystemConfig{HomeDir: homeDir, Logger: logger})
	if err != nil {
		return fmt.Errorf("plugin system: %w", err)
	}
	defer system.Shutdown()
	if err := system.RegisterBuiltin(builtin.Providers()); err != nil {
		return fmt.Errorf("register builtin plugins: %w", err)
	}
	return run(system)
}

func writeEnabledPlugins(w io.Writer, registry *plugin.Registry) {
	entries := registry.ListEnabledByCategory(plugin.CategoryPlatform)
	if len(entries) == 0 {
		fmt.Fprintln(w, "  No enabled platform plugins.")
		fmt.Fprintln(w, "  Enable one with: covo-agent plugin enable <name>")
		fmt.Fprintln(w, "  Available:", strings.Join(builtin.Names(), ", "))
		return
	}
	fmt.Fprintf(w, "  Enabled plugins (%d):\n", len(entries))
	for _, entry := range entries {
		fmt.Fprintf(w, "    ✓ %s  [%s]\n", entry.Name, entry.ID)
	}
}

func writePluginInfo(w io.Writer, registry *plugin.Registry, name string) error {
	entry := registry.Get(name)
	if entry == nil {
		return fmt.Errorf("plugin %q not found", name)
	}
	status := "disabled"
	if entry.Enabled {
		status = "enabled"
	}
	fmt.Fprintf(w, "  Name:     %s\n", entry.Name)
	fmt.Fprintf(w, "  ID:       %s\n", entry.ID)
	fmt.Fprintf(w, "  Category: %s\n", entry.Category)
	fmt.Fprintf(w, "  Status:   %s\n", status)
	if provider, ok := entry.Provider.(plugin.PlatformProvider); ok {
		if err := provider.Validate(); err != nil {
			fmt.Fprintf(w, "  Validate: ✗ %s\n", err)
		} else {
			fmt.Fprintln(w, "  Validate: ✓")
		}
	}
	return nil
}

func setPluginEnabled(w io.Writer, system *plugin.System, name string, enabled bool) error {
	if system.Registry.Get(name) == nil {
		return fmt.Errorf("plugin %q not found; available: %s", name, strings.Join(builtin.Names(), ", "))
	}
	var err error
	if enabled {
		err = system.Registry.Enable(name)
	} else {
		err = system.Registry.Disable(name)
	}
	if err != nil {
		return err
	}
	if err := system.SavePluginConfig(); err != nil {
		return fmt.Errorf("save plugin config: %w", err)
	}
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	fmt.Fprintf(w, "  ✓ Plugin %q %s\n", name, state)
	return nil
}

func writePluginMarketplace(w io.Writer, registry *plugin.Registry) {
	fmt.Fprintln(w, "  Available platform plugins:")
	fmt.Fprintln(w)
	for _, name := range builtin.Names() {
		entry := registry.Get(name)
		status := "available (not configured)"
		if entry != nil && entry.Enabled {
			status = "enabled"
		} else if entry != nil {
			status = "disabled"
		}
		fmt.Fprintf(w, "    %-12s  %s\n", name, status)
		if description := builtin.Description(name); description != "" {
			fmt.Fprintf(w, "                      %s\n", description)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Use 'covo-agent plugin enable <name>' to enable a plugin")
	fmt.Fprintln(w, "  Each plugin needs its own API keys set in .env")
}

func registerPluginMemoryProviders(homeDir string, ps *plugin.System) {
	for _, mp := range ps.MemoryProviders() {
		if _, exists := evolution.GetMemoryProvider(mp.Name); exists {
			continue
		}
		name, factory := mp.Name, mp.Factory
		evolution.RegisterMemoryProvider(name, func(cfg evolution.MemoryProviderConfig) (evolution.MemoryProvider, error) {
			raw, err := factory(cfg.HomeDir)
			if err != nil {
				return nil, err
			}
			p, ok := raw.(evolution.MemoryProvider)
			if !ok {
				return nil, fmt.Errorf("plugin memory provider %q does not implement evolution.MemoryProvider", name)
			}
			return p, nil
		})
	}
}
