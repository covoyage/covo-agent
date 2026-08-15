package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/extension"
	"github.com/spf13/cobra"
)

func newExtCommand() *cobra.Command {
	extCmd := &cobra.Command{
		Use:   "ext",
		Short: "Manage external extensions",
		Args:  cobra.NoArgs,
	}

	extCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List discovered extensions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			extDir := filepath.Join(homeDir, "extensions")
			mgr := extension.NewManager(extDir)
			if err := mgr.Discover(context.Background()); err != nil {
				return fmt.Errorf("discover extensions: %w", err)
			}
			w := cmd.OutOrStdout()
			exts := mgr.List()
			if len(exts) == 0 {
				fmt.Fprintln(w, "  No extensions found.")
				fmt.Fprintf(w, "  Create subdirectories in: %s\n", extDir)
				fmt.Fprintln(w, "  Each subdirectory needs a manifest.json and executable binary.")
				return nil
			}
			fmt.Fprintf(w, "  Extensions (%d):\n", len(exts))
			for _, ext := range exts {
				status := "✓"
				if !ext.Enabled {
					status = "✗"
				}
				fmt.Fprintf(w, "    %s %s  (%d tools)  v%s\n", status, ext.Name, len(ext.Tools), ext.Version)
			}
			return nil
		},
	})

	extCmd.AddCommand(&cobra.Command{
		Use:   "info <name>",
		Short: "Show extension details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			extDir := filepath.Join(homeDir, "extensions")
			mgr := extension.NewManager(extDir)
			if err := mgr.Discover(context.Background()); err != nil {
				return fmt.Errorf("discover extensions: %w", err)
			}
			name := args[0]
			ext := mgr.Get(name)
			if ext == nil {
				return fmt.Errorf("extension %q not found", name)
			}
			w := cmd.OutOrStdout()
			status := "enabled"
			if !ext.Enabled {
				status = "disabled"
			}
			fmt.Fprintf(w, "  Name:        %s\n", ext.Name)
			fmt.Fprintf(w, "  Description: %s\n", ext.Description)
			fmt.Fprintf(w, "  Version:     %s\n", ext.Version)
			fmt.Fprintf(w, "  Status:      %s\n", status)
			if ext.BinaryPath != "" {
				fmt.Fprintf(w, "  Binary:      %s\n", ext.BinaryPath)
			} else {
				fmt.Fprintln(w, "  Binary:      (not found)")
			}
			if len(ext.Tools) > 0 {
				fmt.Fprintf(w, "  Tools (%d):\n", len(ext.Tools))
				for _, t := range ext.Tools {
					fmt.Fprintf(w, "    - %s: %s\n", t.Name, t.Description)
				}
			} else {
				fmt.Fprintln(w, "  Tools:       (none)")
			}
			return nil
		},
	})

	extCmd.AddCommand(&cobra.Command{
		Use:   "run <name> <tool> [json_args]",
		Short: "Run an extension tool",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			extDir := filepath.Join(homeDir, "extensions")
			mgr := extension.NewManager(extDir)
			if err := mgr.Discover(context.Background()); err != nil {
				return fmt.Errorf("discover extensions: %w", err)
			}
			var callArgs json.RawMessage
			if len(args) > 2 {
				callArgs = json.RawMessage(args[2])
			} else {
				callArgs = json.RawMessage("{}")
			}
			result, err := mgr.ExecuteTool(context.Background(), args[0], args[1], callArgs)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  Result: %s\n", string(result))
			return nil
		},
	})

	extCmd.AddCommand(&cobra.Command{
		Use:   "reload",
		Short: "Re-discover extensions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			extDir := filepath.Join(homeDir, "extensions")
			mgr := extension.NewManager(extDir)
			if err := mgr.Reload(context.Background()); err != nil {
				return fmt.Errorf("reload: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ Reloaded %d extensions\n", len(mgr.List()))
			return nil
		},
	})

	return extCmd
}
