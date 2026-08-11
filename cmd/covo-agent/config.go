package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/spf13/cobra"
)

func newConfigCommand(runtime *commandRuntime) *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			writeConfigSummary(cmd.OutOrStdout(), runtime.cfg, runtime.homeDir)
			return nil
		},
	}

	configCmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show the active configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			writeConfigSummary(cmd.OutOrStdout(), runtime.cfg, runtime.homeDir)
			return nil
		},
	})
	configCmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print the configuration file path",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), configPath(runtime.homeDir))
			return nil
		},
	})
	configCmd.AddCommand(&cobra.Command{
		Use:   "edit",
		Short: "Open the configuration file in an editor",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return editConfigFile(configPath(runtime.homeDir))
		},
	})
	configCmd.AddCommand(&cobra.Command{
		Use:   "check",
		Short: "Validate the active configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return checkConfigFile(cmd.OutOrStdout(), runtime.cfg, configPath(runtime.homeDir))
		},
	})
	configCmd.AddCommand(&cobra.Command{
		Use:       "set <key> <value>",
		Short:     "Set a configuration value",
		Args:      cobra.ExactArgs(2),
		ValidArgs: []string{"provider", "model", "mode"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return setConfigValue(cmd.OutOrStdout(), runtime.cfg, args[0], args[1])
		},
	})
	configCmd.AddCommand(&cobra.Command{
		Use:   "schema",
		Short: "Print the configuration JSON schema",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			schema, err := cli.GenerateConfigSchema()
			if err != nil {
				return fmt.Errorf("generate schema: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), schema)
			return nil
		},
	})

	return configCmd
}

func configPath(homeDir string) string {
	return filepath.Join(homeDir, "config.yaml")
}

func writeConfigSummary(w io.Writer, cfg *cli.Config, homeDir string) {
	fmt.Fprintf(w, "  Home:      %s\n", homeDir)
	fmt.Fprintf(w, "  Provider:  %s\n", cfg.Provider)
	fmt.Fprintf(w, "  Model:     %s\n", cfg.Model)
	fmt.Fprintf(w, "  Mode:      %s\n", cfg.Mode)
}

func editConfigFile(path string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor: %w", err)
	}
	return nil
}

func checkConfigFile(w io.Writer, cfg *cli.Config, path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("config: %s not found", path)
		}
		return fmt.Errorf("stat config: %w", err)
	}
	fmt.Fprintf(w, "  ✓ config: %s\n", path)
	fmt.Fprintf(w, "  ✓ provider: %s\n", cfg.Provider)
	fmt.Fprintf(w, "  ✓ model: %s\n", cfg.Model)
	fmt.Fprintf(w, "  ✓ mode: %s\n", cfg.Mode)
	return nil
}

func setConfigValue(w io.Writer, cfg *cli.Config, key, value string) error {
	switch key {
	case "provider":
		cfg.Provider = value
	case "model":
		cfg.Model = value
	case "mode":
		cfg.Mode = value
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	if err := cli.SaveConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Fprintf(w, "  ✓ %s set to %q\n", key, value)
	return nil
}
