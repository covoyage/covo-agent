package prefs

import (
	"fmt"
	"github.com/covoyage/covo-agent/internal/cli"

	"github.com/spf13/cobra"
)

func NewProfileCommand() *cobra.Command {
	profileCmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage profiles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Default: show current profile + list
			current := cli.ResolveActiveProfile()
			profiles, err := cli.ListProfiles()
			if err != nil {
				return fmt.Errorf("list profiles: %w", err)
			}
			w := cmd.OutOrStdout()
			if current != "" {
				fmt.Fprintf(w, "  Active profile: %s\n", current)
			} else {
				fmt.Fprintln(w, "  No active profile (using default)")
			}
			if len(profiles) == 0 {
				fmt.Fprintln(w, "  No profiles.")
				return nil
			}
			fmt.Fprintln(w, "  Profiles:")
			for _, p := range profiles {
				mark := " "
				if p == current {
					mark = "*"
				}
				fmt.Fprintf(w, "    %s %s\n", mark, p)
			}
			return nil
		},
	}

	profileCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List profiles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			profiles, err := cli.ListProfiles()
			if err != nil {
				return fmt.Errorf("list profiles: %w", err)
			}
			current := cli.ResolveActiveProfile()
			w := cmd.OutOrStdout()
			if len(profiles) == 0 {
				fmt.Fprintln(w, "  No profiles.")
				return nil
			}
			for _, p := range profiles {
				mark := " "
				if p == current {
					mark = "*"
				}
				fmt.Fprintf(w, "  %s %s\n", mark, p)
			}
			return nil
		},
	})

	profileCmd.AddCommand(&cobra.Command{
		Use:   "use <name>",
		Short: "Switch active profile",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			profiles, _ := cli.ListProfiles()
			return profiles, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cli.UseProfile(args[0]); err != nil {
				return fmt.Errorf("use profile: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ Switched to profile %q\n", args[0])
			return nil
		},
	})

	profileCmd.AddCommand(&cobra.Command{
		Use:   "create <name>",
		Short: "Create a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cli.CreateProfile(args[0]); err != nil {
				return fmt.Errorf("create profile: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ Profile %q created\n", args[0])
			return nil
		},
	})

	profileCmd.AddCommand(&cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cli.DeleteProfile(args[0]); err != nil {
				return fmt.Errorf("delete profile: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ Profile %q deleted\n", args[0])
			return nil
		},
	})

	profileCmd.AddCommand(&cobra.Command{
		Use:   "info [name]",
		Short: "Show profile details",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := cli.ResolveActiveProfile()
			if len(args) > 0 {
				name = args[0]
			}
			if name == "" {
				fmt.Println("  No active profile.")
				return nil
			}
			cli.PrintProfileStatus(name)
			return nil
		},
	})

	return profileCmd
}
