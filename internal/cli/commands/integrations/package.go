package integrations

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pkg "github.com/covoyage/covo-agent/internal/pkg"
	"github.com/spf13/cobra"
)

func NewPackageCommand() *cobra.Command {
	packageCmd := &cobra.Command{
		Use:   "package",
		Short: "Manage packages",
		Args:  cobra.NoArgs,
	}

	packageCmd.AddCommand(&cobra.Command{
		Use:     "install <path|url>",
		Aliases: []string{"i"},
		Short:   "Install a package",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := expandPath(args[0])
			var installed *pkg.Package
			var err error
			if pkg.IsGitURL(source) {
				installed, err = pkg.InstallGit(source)
			} else {
				installed, err = pkg.InstallLocal(source)
			}
			if err != nil {
				return fmt.Errorf("install: %w", err)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Installed package %q v%s\n", installed.Name, installed.Version)
			if installed.Description != "" {
				fmt.Fprintln(w, " ", installed.Description)
			}
			return nil
		},
	})

	packageCmd.AddCommand(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List installed packages",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			packages, err := pkg.ListInstalled()
			if err != nil {
				return fmt.Errorf("list: %w", err)
			}
			w := cmd.OutOrStdout()
			if len(packages) == 0 {
				fmt.Fprintln(w, "No packages installed.")
				return nil
			}
			fmt.Fprintln(w, "Installed packages:")
			for name, entry := range packages {
				desc := ""
				if entry.Source != nil {
					desc = " [" + entry.Source.Type + ": " + entry.Source.URL + "]"
				}
				fmt.Fprintf(w, "  %s v%s%s\n", name, entry.Version, desc)
			}
			return nil
		},
	})

	packageCmd.AddCommand(&cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a package",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := pkg.Remove(args[0]); err != nil {
				return fmt.Errorf("remove: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed package %q\n", args[0])
			return nil
		},
	})

	packageCmd.AddCommand(&cobra.Command{
		Use:   "info <name>",
		Short: "Show package details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			packages, err := pkg.ListInstalled()
			if err != nil {
				return fmt.Errorf("info: %w", err)
			}
			name := args[0]
			entry, ok := packages[name]
			if !ok {
				return fmt.Errorf("package %q not found", name)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Name:    %s\n", name)
			fmt.Fprintf(w, "Version: %s\n", entry.Version)
			if entry.Source != nil {
				fmt.Fprintf(w, "Source:  %s (%s)\n", entry.Source.Type, entry.Source.URL)
			}
			c := entry.Contents
			if len(c.Extensions) > 0 {
				fmt.Fprintf(w, "Extensions: %s\n", strings.Join(c.Extensions, ", "))
			}
			if len(c.Skills) > 0 {
				fmt.Fprintf(w, "Skills: %s\n", strings.Join(c.Skills, ", "))
			}
			if len(c.Templates) > 0 {
				fmt.Fprintf(w, "Templates: %s\n", strings.Join(c.Templates, ", "))
			}
			return nil
		},
	})

	return packageCmd
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		hd, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(hd, path[2:])
		}
	}
	return path
}
