package automation

import (
	"encoding/json"
	"fmt"
	"github.com/covoyage/covo-agent/internal/cli"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/spf13/cobra"

	"github.com/covoyage/covo-agent/internal/rollout"
)

func NewRolloutCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rollout",
		Short: "Rollout tracing and replay commands",
	}

	cmd.AddCommand(newRolloutListCommand())
	cmd.AddCommand(newRolloutShowCommand())
	cmd.AddCommand(newRolloutExportCommand())
	cmd.AddCommand(newRolloutImportCommand())
	cmd.AddCommand(newRolloutDeleteCommand())
	cmd.AddCommand(newRolloutCountCommand())
	cmd.AddCommand(newRolloutReplayCommand())
	cmd.AddCommand(newRolloutDiffCommand())
	cmd.AddCommand(newRolloutTestCommand())

	return cmd
}

func getRolloutStore() (*rollout.Store, error) {
	homeDir, err := cli.HomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home dir: %w", err)
	}
	return rollout.NewStore(homeDir)
}

func getReplayProvider() (agentcore.Provider, string, error) {
	providerStr := cli.ResolveProvider(&cli.Config{})
	modelStr := cli.ResolveModel(&cli.Config{})
	provider, err := cli.BuildProvider(providerStr)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create provider: %w", err)
	}
	return provider, modelStr, nil
}

// ── list ────────────────────────────────────────────────────────────────────

func newRolloutListCommand() *cobra.Command {
	var (
		sessionID string
		model     string
		limit     int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recorded rollouts",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := getRolloutStore()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			filter := rollout.ListFilter{
				SessionID: sessionID,
				Model:     model,
				Limit:     limit,
			}
			rollouts, err := store.List(ctx, filter)
			if err != nil {
				return fmt.Errorf("failed to list rollouts: %w", err)
			}
			if len(rollouts) == 0 {
				fmt.Println("No rollouts found.")
				return nil
			}
			for _, r := range rollouts {
				fmt.Printf("%-12s  model=%-20s  turns=%-3d  session=%s  %s\n",
					r.ID, r.Model, r.TurnCount, r.SessionID,
					r.StartedAt.Format(time.RFC3339))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "Filter by session ID")
	cmd.Flags().StringVar(&model, "model", "", "Filter by model name")
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "Maximum number of results")
	return cmd
}

// ── show ────────────────────────────────────────────────────────────────────

func newRolloutShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show details of a recorded rollout",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := getRolloutStore()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			r, err := store.Get(ctx, args[0])
			if err != nil {
				return fmt.Errorf("failed to get rollout: %w", err)
			}
			fmt.Print(rollout.FormatRolloutSummary(r))
			return nil
		},
	}
}

// ── export ──────────────────────────────────────────────────────────────────

func newRolloutExportCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "export <id>",
		Short: "Export a rollout as JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := getRolloutStore()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			r, err := store.Get(ctx, args[0])
			if err != nil {
				return fmt.Errorf("failed to get rollout: %w", err)
			}
			bundle := &rollout.Bundle{
				Version:    1,
				ExportedAt: time.Now(),
				Rollouts:   []rollout.Rollout{*r},
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(bundle)
		},
	}
}

// ── import ──────────────────────────────────────────────────────────────────

func newRolloutImportCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "import <file>",
		Short: "Import rollouts from a JSON file or stdin",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := getRolloutStore()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			var data []byte
			if len(args) == 0 || args[0] == "-" {
				data, err = io.ReadAll(os.Stdin)
			} else {
				data, err = os.ReadFile(args[0])
			}
			if err != nil {
				return fmt.Errorf("failed to read input: %w", err)
			}
			bundle, single, err := rollout.ParseBundleOrRollout(data)
			if err != nil {
				return err
			}
			count := 0
			if bundle != nil {
				for i := range bundle.Rollouts {
					if err := store.Save(ctx, &bundle.Rollouts[i]); err != nil {
						return fmt.Errorf("failed to save rollout %s: %w", bundle.Rollouts[i].ID, err)
					}
					count++
				}
			} else if single != nil {
				if err := store.Save(ctx, single); err != nil {
					return fmt.Errorf("failed to save rollout: %w", err)
				}
				count = 1
			}
			fmt.Printf("Imported %d rollout(s).\n", count)
			return nil
		},
	}
}

// ── delete ──────────────────────────────────────────────────────────────────

func newRolloutDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a recorded rollout",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := getRolloutStore()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if err := store.Delete(ctx, args[0]); err != nil {
				return fmt.Errorf("failed to delete rollout: %w", err)
			}
			fmt.Printf("Deleted rollout %s.\n", args[0])
			return nil
		},
	}
}

// ── count ───────────────────────────────────────────────────────────────────

func newRolloutCountCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "count",
		Short: "Count recorded rollouts",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := getRolloutStore()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			n, err := store.Count(ctx)
			if err != nil {
				return fmt.Errorf("failed to count rollouts: %w", err)
			}
			fmt.Printf("%d rollout(s) recorded.\n", n)
			return nil
		},
	}
}

// ── replay ──────────────────────────────────────────────────────────────────

func newRolloutReplayCommand() *cobra.Command {
	var (
		model       string
		maxTurns    int
		live        bool
		strategyStr string
	)
	cmd := &cobra.Command{
		Use:   "replay <id>",
		Short: "Replay a recorded rollout",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := getRolloutStore()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			r, err := store.Get(ctx, args[0])
			if err != nil {
				return fmt.Errorf("failed to get rollout: %w", err)
			}
			provider, defaultModel, err := getReplayProvider()
			if err != nil {
				return err
			}
			if model == "" {
				model = defaultModel
			}

			mode := rollout.ReplayModeDeterministic
			if live {
				mode = rollout.ReplayModeLive
			}

			strategy := rollout.StrategyMock
			switch strategyStr {
			case "real":
				strategy = rollout.StrategyReal
			case "abort":
				strategy = rollout.StrategyAbort
			case "mock", "":
				strategy = rollout.StrategyMock
			default:
				return fmt.Errorf("unknown strategy %q (use mock, real, or abort)", strategyStr)
			}

			engine := rollout.NewReplayEngine(rollout.ReplayConfig{
				Model:    model,
				Provider: provider,
				Mode:     mode,
				Strategy: strategy,
				Logger:   slog.Default(),
			})

			result, err := engine.Replay(ctx, r)
			if err != nil {
				return fmt.Errorf("replay failed: %w", err)
			}

			fmt.Printf("Replay: %s (mode=%s strategy=%s)\n", result.Rollout.ID, result.Mode, result.Strategy)
			fmt.Printf("Turns:  %d replayed\n", result.TurnsReplayed)
			fmt.Printf("Tokens: %d prompt + %d completion\n",
				result.TotalTokens.PromptTokens, result.TotalTokens.CompletionTokens)
			fmt.Printf("Time:   %s\n", result.Duration.Round(time.Millisecond))
			if len(result.Errors) > 0 {
				fmt.Printf("Errors: %d\n", len(result.Errors))
				for _, e := range result.Errors {
					fmt.Printf("  - %s\n", e)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "Model override for replay")
	cmd.Flags().IntVar(&maxTurns, "max-turns", 0, "Maximum number of turns to replay (0=all)")
	cmd.Flags().BoolVar(&live, "live", false, "Execute tools for real instead of using recorded results")
	cmd.Flags().StringVar(&strategyStr, "strategy", "mock", "Tool execution strategy: mock, real, abort")
	return cmd
}

// ── diff ────────────────────────────────────────────────────────────────────

func newRolloutDiffCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "diff <id1> <id2>",
		Short: "Compare two rollouts with a structured diff",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := getRolloutStore()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			r1, err := store.Get(ctx, args[0])
			if err != nil {
				return fmt.Errorf("failed to get rollout %s: %w", args[0], err)
			}
			r2, err := store.Get(ctx, args[1])
			if err != nil {
				return fmt.Errorf("failed to get rollout %s: %w", args[1], err)
			}
			diff := rollout.DiffRollouts(r1, r2)
			fmt.Print(rollout.FormatDiff(diff))
			return nil
		},
	}
}

// ── test ────────────────────────────────────────────────────────────────────

func newRolloutTestCommand() *cobra.Command {
	var (
		model       string
		strategyStr string
		reportPath  string
	)
	cmd := &cobra.Command{
		Use:   "test <dir-or-id...>",
		Short: "Run batch regression tests against recorded rollouts",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider, defaultModel, err := getReplayProvider()
			if err != nil {
				return err
			}
			if model == "" {
				model = defaultModel
			}

			strategy := rollout.StrategyMock
			switch strategyStr {
			case "real":
				strategy = rollout.StrategyReal
			case "abort":
				strategy = rollout.StrategyAbort
			case "mock", "":
				strategy = rollout.StrategyMock
			default:
				return fmt.Errorf("unknown strategy %q", strategyStr)
			}

			// Collect test cases from args (directories or individual rollout IDs).
			var cases []rollout.TestCase
			for _, arg := range args {
				fi, statErr := os.Stat(arg)
				if statErr == nil && fi.IsDir() {
					dirCases, loadErr := rollout.LoadTestCasesFromDir(arg)
					if loadErr != nil {
						return loadErr
					}
					cases = append(cases, dirCases...)
				} else {
					cases = append(cases, rollout.TestCase{
						Name:      arg,
						FilePath:  arg,
						Criterion: rollout.CriterionNoErrors,
					})
				}
			}

			if len(cases) == 0 {
				fmt.Println("No test cases found.")
				return nil
			}

			suite, err := rollout.RunTestSuite(cmd.Context(), rollout.TestConfig{
				Provider: provider,
				Logger:   slog.Default(),
				Model:    model,
				Strategy: strategy,
			}, cases)
			if err != nil {
				return fmt.Errorf("test suite failed: %w", err)
			}

			fmt.Print(rollout.FormatTestSuite(suite))

			if reportPath != "" {
				if writeErr := rollout.WriteTestReport(suite, reportPath); writeErr != nil {
					return writeErr
				}
				fmt.Printf("Report written to %s\n", reportPath)
			}

			if suite.Failed > 0 {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "Model override for replay")
	cmd.Flags().StringVar(&strategyStr, "strategy", "mock", "Tool execution strategy: mock, real, abort")
	cmd.Flags().StringVar(&reportPath, "report", "", "Write JSON test report to this path")
	return cmd
}
