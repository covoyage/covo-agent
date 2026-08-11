package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/covoyage/covo-agent/internal/cli"
	agenttheme "github.com/covoyage/covo-agent/internal/theme"
	"github.com/spf13/cobra"
)

func newThemeCommand() *cobra.Command {
	themeCmd := &cobra.Command{
		Use:   "theme",
		Short: "Manage themes",
		Args:  cobra.NoArgs,
	}

	themeCmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show the current theme",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			current, err := readSkinTheme(homeDir)
			if err != nil {
				return fmt.Errorf("read skin: %w", err)
			}
			w := cmd.OutOrStdout()
			if current == "" {
				fmt.Fprintln(w, "  No theme set (using terminal default)")
			} else {
				fmt.Fprintf(w, "  Theme: %s\n", current)
			}
			return nil
		},
	})

	themeCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List available themes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			themes := agenttheme.All()
			w := cmd.OutOrStdout()
			fmt.Fprintln(w, "  Available themes:")
			var darkNames, lightNames []string
			for _, p := range themes {
				if p.Dark {
					darkNames = append(darkNames, p.Name)
				} else {
					lightNames = append(lightNames, p.Name)
				}
			}
			if len(darkNames) > 0 {
				fmt.Fprintln(w, "    Dark:")
				for _, n := range darkNames {
					fmt.Fprintf(w, "      %s\n", n)
				}
			}
			if len(lightNames) > 0 {
				fmt.Fprintln(w, "    Light:")
				for _, n := range lightNames {
					fmt.Fprintf(w, "      %s\n", n)
				}
			}
			return nil
		},
	})

	themeCmd.AddCommand(&cobra.Command{
		Use:   "set <name>",
		Short: "Set the active theme",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			var names []string
			for _, p := range agenttheme.All() {
				names = append(names, p.Name)
			}
			return names, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			p := agenttheme.Get(name)
			if p == nil {
				return fmt.Errorf("unknown theme: %s (run 'covo-agent theme list' to see available themes)", name)
			}
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			if err := writeSkinTheme(homeDir, name); err != nil {
				return fmt.Errorf("write skin: %w", err)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "  Theme set to: %s\n", name)
			if p.Dark {
				fmt.Fprintln(w, "  (dark theme)")
			} else {
				fmt.Fprintln(w, "  (light theme)")
			}
			return nil
		},
	})

	return themeCmd
}

// readSkinTheme reads the theme name from skin.yaml.
func readSkinTheme(homeDir string) (string, error) {
	path := filepath.Join(homeDir, "skin.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var cfg skinConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", err
	}
	return cfg.Theme, nil
}

// writeSkinTheme writes the theme name to skin.yaml, preserving existing colors.
func writeSkinTheme(homeDir, themeName string) error {
	path := filepath.Join(homeDir, "skin.yaml")
	var cfg skinConfig

	data, err := os.ReadFile(path)
	if err == nil {
		yaml.Unmarshal(data, &cfg) // ignore parse error, will overwrite
	}

	cfg.Theme = themeName
	out, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshal skin: %w", err)
	}
	return os.WriteFile(path, out, 0644)
}
