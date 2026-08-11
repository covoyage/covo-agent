package main

import (
	"fmt"
	"strings"

	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/spf13/cobra"
)

func newFeaturesCommand() *cobra.Command {
	featuresCmd := &cobra.Command{
		Use:   "features",
		Short: "Manage feature flags",
		Args:  cobra.NoArgs,
	}

	featuresCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all feature flags",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			listFeatures()
			return nil
		},
	})

	featuresCmd.AddCommand(&cobra.Command{
		Use:   "enable <name>...",
		Short: "Enable one or more feature flags",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			for _, name := range args {
				if err := cli.Enable(name); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "enable %q: %v\n", name, err)
				} else {
					fmt.Fprintf(w, "  ✓ enabled feature: %s\n", name)
				}
			}
			return nil
		},
	})

	featuresCmd.AddCommand(&cobra.Command{
		Use:   "disable <name>...",
		Short: "Disable one or more feature flags",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			for _, name := range args {
				if err := cli.Disable(name); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "disable %q: %v\n", name, err)
				} else {
					fmt.Fprintf(w, "  ✗ disabled feature: %s\n", name)
				}
			}
			return nil
		},
	})

	featuresCmd.AddCommand(&cobra.Command{
		Use:   "promote <name> <stage>",
		Short: "Advance a flag lifecycle stage",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			stage, err := parseStage(args[1])
			if err != nil {
				return fmt.Errorf("invalid stage %q: %w", args[1], err)
			}
			if err := cli.Promote(args[0], stage); err != nil {
				return fmt.Errorf("promote %q: %w", args[0], err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  ↑ promoted feature: %s → %s\n", args[0], stage)
			return nil
		},
	})

	return featuresCmd
}

func parseStage(s string) (cli.Stage, error) {
	switch strings.ToLower(s) {
	case "under-development", "underdev":
		return cli.UnderDevelopment, nil
	case "experimental", "exp":
		return cli.Experimental, nil
	case "stable":
		return cli.Stable, nil
	case "deprecated", "depr":
		return cli.Deprecated, nil
	case "removed":
		return cli.Removed, nil
	default:
		return -1, fmt.Errorf("unknown stage: %q (valid: under-development, experimental, stable, deprecated, removed)", s)
	}
}

func listFeatures() {
	flags := cli.ListFlags()
	if len(flags) == 0 {
		fmt.Println("  No feature flags registered.")
		return
	}
	fmt.Println("── Feature Flags ──")
	var currentStage cli.Stage = -1
	for _, f := range flags {
		if f.Stage != currentStage {
			if currentStage >= 0 {
				fmt.Println()
			}
			currentStage = f.Stage
			fmt.Printf("  [%s]\n", f.Stage)
		}
		status := "○"
		if f.Enabled {
			status = "●"
		}
		override := ""
		if f.Overridden {
			override = " (overridden)"
		}
		envKey := "COVO_FEATURE_" + strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))
		fmt.Printf("    %s %s%s\n", status, f.Name, override)
		fmt.Printf("      %s\n", f.Description)
		fmt.Printf("      default: %v (from stage: %s)  |  env: %s\n", f.Default, f.Stage, envKey)
	}
}
