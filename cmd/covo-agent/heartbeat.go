package main

import (
	"fmt"

	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/tools"
	"github.com/spf13/cobra"
)

func newHeartbeatCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "heartbeat <interval>",
		Short: "Run heartbeat processing",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}

			interval := args[0]
			if len(interval) < 2 || (interval[len(interval)-1] != 'm' && interval[len(interval)-1] != 'h' && interval[:1] != "@") {
				return fmt.Errorf("invalid interval %q. Use e.g. 30m, 1h, 6h, or @every 30m", interval)
			}

			schedule := "@every " + interval
			store := tools.NewCronStore(homeDir)
			_ = store.Load()

			name := fmt.Sprintf("heartbeat-%s", interval)
			for _, j := range store.List() {
				if j.Name == name {
					store.Remove(j.ID)
					break
				}
			}

			prompt := "HEARTBEAT CHECK — Review the current state: check pending standing orders, " +
				"pending commitments, running sub-agents, and any active goals. Summarize what's " +
				"happening and whether any action is needed."

			job, err := store.Create(name, prompt, schedule)
			if err != nil {
				return fmt.Errorf("create heartbeat: %w", err)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "  ✓ Heartbeat created: %s (every %s, id: %s)\n", name, interval, job.ID[:8])
			fmt.Fprintln(w)
			fmt.Fprintln(w, "  The agent will now periodically check in on pending tasks.")
			fmt.Fprintln(w, "  Run `covo-agent cron list` to see all jobs.")
			fmt.Fprintln(w, "  Run `covo-agent cron remove <id>` to stop.")
			return nil
		},
	}
}
