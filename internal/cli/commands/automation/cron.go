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
				if j.ConsecutiveErrors > 0 {
					fmt.Fprintf(w, "       Consecutive errors: %d (last: %s)\n", j.ConsecutiveErrors, j.LastResult)
				}
				if j.Model != "" {
					fmt.Fprintf(w, "       Model: %s\n", j.Model)
				}
				if j.ContextFrom != "" {
					fmt.Fprintf(w, "       Context from: %s\n", j.ContextFrom)
				}
			}
			return nil
		},
	})

	cronCmd.AddCommand(&cobra.Command{
		Use:   "incidents",
		Short: "Show recent failed cron runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			store := tools.NewCronStore(homeDir)
			_ = store.Load()
			w := cmd.OutOrStdout()
			incidents, err := store.ListIncidents(20)
			if err != nil {
				return fmt.Errorf("list incidents: %w", err)
			}
			if len(incidents) == 0 {
				fmt.Fprintln(w, "  No cron incidents.")
				return nil
			}
			for _, inc := range incidents {
				fmt.Fprintf(w, "  %s  [%s] %s  %s\n", inc.JobID[:8], inc.Status, inc.EndedAt.Format("2006-01-02 15:04"), inc.Name)
				summary := inc.Summary
				if len(summary) > 200 {
					summary = summary[:200] + "..."
				}
				fmt.Fprintf(w, "       %s\n", summary)
			}
			return nil
		},
	})

	cronAdd := &cobra.Command{
		Use:   "add <name> <schedule> <prompt>",
		Short: "Add a new cron job",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			model, _ := cmd.Flags().GetString("model")
			skills, _ := cmd.Flags().GetStringSlice("skill")
			preRun, _ := cmd.Flags().GetString("pre-run")
			contextFrom, _ := cmd.Flags().GetString("context-from")
			toolsAllow, _ := cmd.Flags().GetStringSlice("tool")
			store := tools.NewCronStore(homeDir)
			_ = store.Load()
			job, err := store.CreateWithOptions(tools.CronJobOptions{
				Name:        args[0],
				Prompt:      args[2],
				Schedule:    args[1],
				ToolsAllow:  toolsAllow,
				Model:       model,
				Skills:      skills,
				PreRun:      preRun,
				ContextFrom: contextFrom,
			})
			if err != nil {
				return fmt.Errorf("create cron: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ Cron job %q created (id: %s)\n", job.Name, job.ID[:8])
			return nil
		},
	}
	cronAdd.Flags().String("model", "", "Model override for this job (provider unchanged)")
	cronAdd.Flags().StringSlice("skill", nil, "Skill names the agent should load for each run (repeatable)")
	cronAdd.Flags().String("pre-run", "", "Shell command executed before each run; non-zero exit aborts the run")
	cronAdd.Flags().String("context-from", "", "Job ID whose last result is injected as context above this job's prompt")
	cronAdd.Flags().StringSlice("tool", nil, "Tool names this job may use (repeatable)")
	cronCmd.AddCommand(cronAdd)

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
				if job.PreRun != "" {
					if err := tools.RunPreRun(context.Background(), job.PreRun); err != nil {
						failed++
						_ = store.FinishRun(job.ID, tools.CronRunRecord{
							JobID:     job.ID,
							StartedAt: time.Now(),
							EndedAt:   time.Now(),
							Status:    "prerun_error",
							Summary:   fmt.Sprintf("pre-run failed: %v", err),
							SessionID: "cron-" + job.ID,
						})
						fmt.Fprintf(w, "  ✗ %s: pre-run failed: %v\n", job.Name, err)
						continue
					}
				}
				runJob := store.ComposeRun(job)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				started := time.Now()
				result, err := runCronPrompt(ctx, runJob)
				cancel()
				ended := time.Now()
				status := "ok"
				summary := result
				if err != nil {
					status = "error"
					summary = err.Error()
					failed++
					fmt.Fprintf(w, "  ✗ %s: %v\n", job.Name, err)
				} else {
					if len(summary) > 500 {
						summary = summary[:500] + "..."
					}
					fmt.Fprintf(w, "  ✓ %s\n\n%s\n", job.Name, result)
				}
				_ = store.FinishRun(job.ID, tools.CronRunRecord{
					JobID:     job.ID,
					StartedAt: started,
					EndedAt:   ended,
					Status:    status,
					Summary:   summary,
					SessionID: "cron-" + job.ID,
				})
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
