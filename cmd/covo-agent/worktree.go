package main

import (
	"fmt"
	"os"
	"path/filepath"

	toolsworktree "github.com/covoyage/covo-agent/internal/tools/worktree"
	"github.com/spf13/cobra"
)

func newWorktreeCommand() *cobra.Command {
	worktreeCmd := &cobra.Command{
		Use:   "worktree",
		Short: "Manage Git worktrees",
		Args:  cobra.NoArgs,
	}

	worktreeCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List tracked worktrees",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			wm := toolsworktree.NewWorktreeManager(filepath.Join(homeDir, ".covo-agent"))
			w := cmd.OutOrStdout()
			wts := wm.ListWorktrees()
			if len(wts) == 0 {
				fmt.Fprintln(w, "No worktrees tracked.")
				return nil
			}
			for _, wt := range wts {
				active, _ := wt["active"].(bool)
				mark := " "
				if active {
					mark = "*"
				}
				fmt.Fprintf(w, "[%s] branch=%s path=%s created=%s\n",
					mark, wt["branch"], wt["path"], wt["created_at"])
			}
			return nil
		},
	})

	worktreeCmd.AddCommand(&cobra.Command{
		Use:   "prune",
		Short: "Prune stale worktrees",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			wm := toolsworktree.NewWorktreeManager(filepath.Join(homeDir, ".covo-agent"))
			w := cmd.OutOrStdout()
			pruned := wm.PruneStale()
			if len(pruned) == 0 {
				fmt.Fprintln(w, "No stale worktrees to prune.")
				return nil
			}
			for _, branch := range pruned {
				fmt.Fprintf(w, "Pruned worktree for branch %q\n", branch)
			}
			return nil
		},
	})

	worktreeCmd.AddCommand(&cobra.Command{
		Use:   "gc",
		Short: "Prune and clean all tracked worktrees",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			wm := toolsworktree.NewWorktreeManager(filepath.Join(homeDir, ".covo-agent"))
			w := cmd.OutOrStdout()
			pruned := wm.PruneStale()
			if len(pruned) == 0 {
				fmt.Fprintln(w, "No stale worktrees to clean up.")
			} else {
				for _, branch := range pruned {
					fmt.Fprintf(w, "Pruned worktree for branch %q\n", branch)
				}
			}
			removed := wm.CleanupAll()
			for _, branch := range removed {
				fmt.Fprintf(w, "Removed worktree for branch %q\n", branch)
			}
			if len(pruned) == 0 && len(removed) == 0 {
				fmt.Fprintln(w, "No worktrees to clean up.")
			}
			return nil
		},
	})

	return worktreeCmd
}
