package automation

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/telemetry"
)

// runCronPrompt executes a cron job prompt with a throwaway agent in the
// current working directory, mirroring the interactive session's cron runner.
// Used by `cron run-due` so scheduled jobs can fire outside the TUI (e.g.
// from an OS crontab or systemd timer).
func runCronPrompt(ctx context.Context, jobID, prompt string) (string, error) {
	cfg, err := cli.LoadConfig()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	cli.RegisterCustomProviders(cfg)
	homeDir, err := cli.EnsureHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}

	providerStr := cli.ResolveProvider(cfg)
	modelStr := cli.ResolveModel(cfg)
	modeStr := cli.ResolveMode(cfg)

	mode := agent.ModeGeneral
	if m, ok := agent.ParseMode(modeStr); ok {
		mode = m
	}

	llm, err := cli.BuildProvider(providerStr)
	if err != nil {
		return "", fmt.Errorf("build provider %q: %w", providerStr, err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelError)}))
	workingDir, _ := os.Getwd()

	covoAgent, err := agent.NewCovoAgent(agent.CovoAgentConfig{
		Mode:                     mode,
		Provider:                 llm,
		ProviderName:             providerStr,
		Model:                    modelStr,
		WorkingDir:               workingDir,
		HomeDir:                  homeDir,
		Logger:                   logger,
		ApprovalCfg:              shared.ApprovalConfigFromCLI(cfg, true), // auto-approve scheduled runs
		ToolProfile:              shared.RuntimeState.ActiveProfile(),
		ThinkingCfg:              shared.ThinkingConfigFromCLI(cfg),
		FrequencyPenalty:         shared.FrequencyPenaltyFromCLI(cfg),
		PresencePenalty:          shared.PresencePenaltyFromCLI(cfg),
		Auxiliary:                shared.AuxiliaryConfigFromCLI(cfg),
		AuxiliaryProviderBuilder: cli.ResolveAuxiliaryProviderBuilder(),
	})
	if err != nil {
		return "", fmt.Errorf("create agent: %w", err)
	}
	defer covoAgent.Close()

	if covoAgent.ApprovalSystem() != nil {
		covoAgent.ApprovalSystem().SetNonInteractive(true)
	}

	// Flush this job's spans promptly; do not shut down the pipeline since
	// more jobs may run in the same process.
	defer telemetry.FlushOtel(context.Background())

	output, err := covoAgent.RunDirectWithSession(ctx, prompt, "cron-"+jobID)
	if err != nil {
		return "", fmt.Errorf("cron job %s: %w", jobID, err)
	}
	return output, nil
}
