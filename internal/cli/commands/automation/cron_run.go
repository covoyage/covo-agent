package automation

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/telemetry"
	"github.com/covoyage/covo-agent/internal/tools"
)

// runCronPrompt executes a cron job with a throwaway agent in the current
// working directory, mirroring the interactive session's cron runner. The job
// must be pre-composed via CronStore.ComposeRun (context chaining) and its
// pre-run gate must have already passed. Used by `cron run-due` so scheduled
// jobs can fire outside the TUI (e.g. from an OS crontab or systemd timer).
func runCronPrompt(ctx context.Context, job *tools.CronJob) (string, error) {
	cfg, err := cli.LoadConfig()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	cli.RegisterCustomProviders(cfg)
	shared.RegisterCustomModes(cfg)
	homeDir, err := cli.EnsureHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}

	providerStr := cli.ResolveProvider(cfg)
	modelStr := cli.ResolveModel(cfg)
	if job.Model != "" {
		modelStr = job.Model
	}
	modeStr := cli.ResolveMode(cfg)

	mode, err := agent.RequireMode(modeStr)
	if err != nil {
		return "", err
	}

	llm, err := cli.BuildProvider(providerStr)
	if err != nil {
		return "", fmt.Errorf("build provider %q: %w", providerStr, err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelError)}))
	workingDir, _ := os.Getwd()
	skillCfgGet, skillCfgSet := shared.SkillConfigFns(cfg)

	var toolNameFilter func(string) bool
	if len(job.ToolsAllow) > 0 {
		allowed := make(map[string]bool, len(job.ToolsAllow))
		for _, name := range job.ToolsAllow {
			allowed[name] = true
		}
		toolNameFilter = func(name string) bool { return allowed[name] }
	}

	appendSystemPrompt := ""
	if len(job.Skills) > 0 {
		appendSystemPrompt = fmt.Sprintf(
			"Before handling the user's request, load and follow these skills via the skill tool: %s.",
			strings.Join(job.Skills, ", "))
	}

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
		SkillConfigGet:           skillCfgGet,
		SkillConfigSet:           skillCfgSet,
		ToolNameFilter:           toolNameFilter,
		AppendSystemPrompt:       appendSystemPrompt,
		LeanTools:                shared.LeanToolsEnabledFor(cfg, string(mode)),
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

	output, err := covoAgent.RunDirectWithSession(ctx, job.Prompt, "cron-"+job.ID)
	if err != nil {
		return "", fmt.Errorf("cron job %s: %w", job.ID, err)
	}
	return output, nil
}
