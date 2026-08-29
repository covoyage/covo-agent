package automation

import (
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/covoyage/covo-agent/internal/rollout"
)

func NewDebugCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Debug and diagnostics commands for rollout traces",
	}
	cmd.AddCommand(newDebugTraceReduceCommand())
	cmd.AddCommand(newDebugTraceFromStoreCommand())
	return cmd
}

// newDebugTraceReduceCommand reduces an exported bundle file into a semantic
// trace graph (state.json), mirroring the unified `debug trace-reduce` entry
// point.
func newDebugTraceReduceCommand() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "trace-reduce <file>",
		Short: "Reduce an exported rollout bundle into a semantic trace graph",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var data []byte
			var err error
			if len(args) == 0 || args[0] == "-" {
				data, err = io.ReadAll(os.Stdin)
			} else {
				data, err = os.ReadFile(args[0])
			}
			if err != nil {
				return err
			}
			bundle, single, err := rollout.ParseBundleOrRollout(data)
			if err != nil {
				return err
			}
			var rollouts []*rollout.Rollout
			if bundle != nil {
				for i := range bundle.Rollouts {
					rollouts = append(rollouts, &bundle.Rollouts[i])
				}
			} else if single != nil {
				rollouts = append(rollouts, single)
			}

			graph := rollout.ReduceTrace(rollouts)

			if output == "" {
				output = "state.json"
			}
			out, err := graph.Marshal()
			if err != nil {
				return err
			}
			if err := os.WriteFile(output, out, 0o644); err != nil {
				return err
			}
			_, _ = os.Stdout.WriteString(rollout.FormatTrace(graph))
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "state.json", "Output file for the JSON graph")
	return cmd
}

// newDebugTraceFromStoreCommand reduces rollouts currently in the store into a
// single combined trace graph, optionally rooted at a specific rollout.
func newDebugTraceFromStoreCommand() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "trace <rollout-id>",
		Short: "Reduce recorded rollouts (optionally under a root) into a trace graph",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := getRolloutStore()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			var rollouts []*rollout.Rollout
			if len(args) == 1 {
				// Collect the named rollout and everything that descends from it.
				root, err := store.Get(ctx, args[0])
				if err != nil {
					return err
				}
				rollouts = append(rollouts, root)
				desc, err := store.Descendants(ctx, args[0])
				if err != nil {
					return err
				}
				rollouts = append(rollouts, desc...)
			} else {
				sums, err := store.List(ctx, rollout.ListFilter{Limit: 1000})
				if err != nil {
					return err
				}
				for _, s := range sums {
					r, err := store.Get(ctx, s.ID)
					if err != nil {
						continue
					}
					rollouts = append(rollouts, r)
				}
			}

			graph := rollout.ReduceTrace(rollouts)
			out, err := graph.Marshal()
			if err != nil {
				return err
			}
			if output == "" {
				output = "state.json"
			}
			if err := os.WriteFile(output, out, 0o644); err != nil {
				return err
			}
			_, _ = os.Stdout.WriteString(rollout.FormatTrace(graph))
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "state.json", "Output file for the JSON graph")
	return cmd
}
