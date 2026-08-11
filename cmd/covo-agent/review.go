package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/spf13/cobra"
)

func newReviewCommand() *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:   "review [base]",
		Short: "Review changes",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := exec.LookPath("git"); err != nil {
				return fmt.Errorf("git is required for review")
			}
			repoCheck := exec.Command("git", "rev-parse", "--is-inside-work-tree")
			if err := repoCheck.Run(); err != nil {
				return fmt.Errorf("not in a git repository")
			}

			base := ""
			if len(args) > 0 {
				base = args[0]
			}
			if base == "" {
				base = detectBaseBranch()
			}

			diffCmd := exec.Command("git", "diff", base+"...HEAD")
			diffOut, err := diffCmd.Output()
			if err != nil {
				return fmt.Errorf("error getting diff: %w", err)
			}
			diff := string(diffOut)
			if diff == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "No changes detected.")
				return nil
			}

			logCmd := exec.Command("git", "log", base+"..HEAD", "--oneline", "--no-decorate")
			logOut, _ := logCmd.Output()
			commits := strings.TrimSpace(string(logOut))

			branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
			branchOut, _ := branchCmd.Output()
			branch := strings.TrimSpace(string(branchOut))

			diffStatCmd := exec.Command("git", "diff", base+"...HEAD", "--stat")
			diffStatOut, _ := diffStatCmd.Output()
			diffStat := strings.TrimSpace(string(diffStatOut))

			var promptBuilder strings.Builder
			promptBuilder.WriteString("Review the following code changes.\n\n")
			promptBuilder.WriteString(fmt.Sprintf("Branch: %s\n", branch))
			if commits != "" {
				promptBuilder.WriteString(fmt.Sprintf("\nCommits:\n%s\n", commits))
			}
			if diffStat != "" {
				promptBuilder.WriteString(fmt.Sprintf("\nFiles changed:\n%s\n", diffStat))
			}
			promptBuilder.WriteString(fmt.Sprintf("\n```diff\n%s\n```\n\n", diff))
			promptBuilder.WriteString(`Provide a concise code review covering:

1. **Summary** — what does this change do?
2. **Issues** — bugs, edge cases, security, performance
3. **Suggestions** — specific, actionable improvements

Be direct and focus on meaningful problems.`)

			cfg, err := cli.LoadConfig()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			cli.RegisterCustomProviders(cfg)
			homeDir, err := cli.EnsureHomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}

			providerType := cli.ResolveProvider(cfg)
			modelStr := cli.ResolveModel(cfg)
			modeStr := cli.ResolveMode(cfg)

			mode := agent.ModeGeneral
			if m, ok := agent.ParseMode(modeStr); ok {
				mode = m
			}

			llm, err := cli.BuildProvider(providerType)
			if err != nil {
				return fmt.Errorf("build provider %q: %w", providerType, err)
			}

			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
			workingDir, _ := os.Getwd()

			covoAgent, err := agent.NewCovoAgent(agent.CovoAgentConfig{
				Mode:                     mode,
				Provider:                 llm,
				ProviderName:             providerType,
				Model:                    modelStr,
				WorkingDir:               workingDir,
				HomeDir:                  homeDir,
				Logger:                   logger,
				ApprovalCfg:              approvalConfigFromCLI(cfg, false),
				ToolProfile:              runtimeState.ActiveProfile(),
				ThinkingCfg:              thinkingConfigFromCLI(cfg),
				FrequencyPenalty:         frequencyPenaltyFromCLI(cfg),
				PresencePenalty:          presencePenaltyFromCLI(cfg),
				SystemPrompt:             "",
				AppendSystemPrompt:       "You are an expert code reviewer. Analyze the provided diff carefully and produce a structured, actionable review. Focus on correctness, security, and performance. Be concise and direct.",
				Auxiliary:                auxiliaryConfigFromCLI(cfg),
				AuxiliaryProviderBuilder: cli.ResolveAuxiliaryProviderBuilder(),
			})
			if err != nil {
				return fmt.Errorf("create agent: %w", err)
			}
			defer covoAgent.Close()

			ctx := context.Background()
			result, err := covoAgent.Core().Run(ctx, promptBuilder.String())
			if err != nil {
				return fmt.Errorf("agent run: %w", err)
			}

			w := cmd.OutOrStdout()
			fmt.Fprintln(w)
			fmt.Fprintln(w, "── Code Review ──")
			fmt.Fprintln(w, result)

			if apply && result != "" {
				fmt.Fprintln(w)
				fmt.Fprintln(w, "── Generating patch from review suggestions... ──")
				patchPrompt := fmt.Sprintf(
					"Based on this code review, produce a unified diff patch that implements the suggested improvements.\n\n%s\n\n```diff\n%s\n```\n\nOutput ONLY the patch (unified diff format) with no explanation.",
					result, diff,
				)
				patchResult, err := covoAgent.Core().Run(ctx, patchPrompt)
				if err != nil {
					return fmt.Errorf("generate patch: %w", err)
				}

				patchPath := "review.patch"
				if err := os.WriteFile(patchPath, []byte(patchResult), 0644); err != nil {
					return fmt.Errorf("write patch: %w", err)
				}
				fmt.Fprintf(w, "Patch saved to %s. Review and apply with: git apply %s\n", patchPath, patchPath)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "write review.patch from suggestions")
	return cmd
}
