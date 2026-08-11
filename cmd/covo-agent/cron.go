package main

import (
	"fmt"

	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/tools"
	"github.com/spf13/cobra"
)

func newCronCommand() *cobra.Command {
	cronCmd := &cobra.Command{
		Use:   "cron",
		Short: "Manage scheduled jobs",
		Args:  cobra.NoArgs,
	}

	cronCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all cron jobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			store := tools.NewCronStore(homeDir)
			_ = store.Load()
			w := cmd.OutOrStdout()
			jobs := store.List()
			if len(jobs) == 0 {
				fmt.Fprintln(w, "  No cron jobs.")
				return nil
			}
			for _, j := range jobs {
				status := "active"
				if !j.Enabled {
					status = "paused"
				}
				fmt.Fprintf(w, "  %s  [%s] %s\n", j.ID[:8], status, j.Name)
				fmt.Fprintf(w, "       Schedule: %s\n", j.Schedule)
				fmt.Fprintf(w, "       Runs: %d\n", j.RunCount)
			}
			return nil
		},
	})

	cronCmd.AddCommand(&cobra.Command{
		Use:   "add <name> <schedule> <prompt>",
		Short: "Add a new cron job",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			store := tools.NewCronStore(homeDir)
			_ = store.Load()
			job, err := store.Create(args[0], args[2], args[1])
			if err != nil {
				return fmt.Errorf("create cron: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ Cron job %q created (id: %s)\n", job.Name, job.ID[:8])
			return nil
		},
	})

	cronCmd.AddCommand(&cobra.Command{
		Use:   "remove <id>",
		Short: "Remove a cron job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			store := tools.NewCronStore(homeDir)
			_ = store.Load()
			store.Remove(args[0])
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ Cron job %s removed\n", args[0][:min(len(args[0]), 8)])
			return nil
		},
	})

	cronCmd.AddCommand(&cobra.Command{
		Use:   "pause <id>",
		Short: "Pause a cron job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			store := tools.NewCronStore(homeDir)
			_ = store.Load()
			if err := store.Disable(args[0]); err != nil {
				return fmt.Errorf("pause: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ Cron job %s paused\n", args[0][:min(len(args[0]), 8)])
			return nil
		},
	})

	cronCmd.AddCommand(&cobra.Command{
		Use:   "resume <id>",
		Short: "Resume a cron job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			store := tools.NewCronStore(homeDir)
			_ = store.Load()
			if err := store.Enable(args[0]); err != nil {
				return fmt.Errorf("resume: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ Cron job %s resumed\n", args[0][:min(len(args[0]), 8)])
			return nil
		},
	})

	return cronCmd
}
