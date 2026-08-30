package automation

import (
	"context"
	"fmt"
	"time"

	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/telemetry"

	"github.com/covoyage/covo-agent/internal/tools"
	"github.com/spf13/cobra"
)

func NewCronCommand() *cobra.Command {
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

	cronCmd.AddCommand(&cobra.Command{
		Use:   "run-due",
		Short: "Run all due cron jobs once, then exit",
		Long: "Run every enabled cron job whose next run time has passed, then exit.\n" +
			"For scheduled execution outside the interactive TUI, invoke this\n" +
			"command from an OS scheduler (crontab, launchd, systemd timer).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			store := tools.NewCronStore(homeDir)
			_ = store.Load()
			w := cmd.OutOrStdout()
			due := store.DueJobs()
			if len(due) == 0 {
				fmt.Fprintln(w, "No due cron jobs.")
				return nil
			}

			// Flush buffered OTel spans before the process exits.
			defer telemetry.ShutdownOtel(context.Background())

			failed := 0
			for _, job := range due {
				fmt.Fprintf(w, "Running %s (%s)...\n", shortID(job.ID), job.Name)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				result, err := runCronPrompt(ctx, job.ID, job.Prompt)
				cancel()
				status := "ok"
				if err != nil {
					status = fmt.Sprintf("error: %v", err)
					failed++
					fmt.Fprintf(w, "  ✗ %s: %v\n", job.Name, err)
				} else {
					if len(result) > 500 {
						status = result[:500] + "..."
					}
					fmt.Fprintf(w, "  ✓ %s\n\n%s\n", job.Name, result)
				}
				_ = store.RecordRun(job.ID, status)
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d cron jobs failed", failed, len(due))
			}
			return nil
		},
	})

	return cronCmd
}

// shortID truncates a cron job ID for display, matching the other subcommands.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
