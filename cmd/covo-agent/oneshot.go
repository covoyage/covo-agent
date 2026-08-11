package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli"
)

func runOneshot(prompt, modeStr, providerStr, modelStr string, yolo, jsonOutput bool, systemPrompt, appendSystemPrompt string) {
	cfg, err := cli.LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	cli.RegisterCustomProviders(cfg)
	homeDir, err := cli.EnsureHomeDir()
	if err != nil {
		log.Fatalf("home dir: %v", err)
	}

	if providerStr == "" {
		providerStr = cli.ResolveProvider(cfg)
	}
	if modelStr == "" {
		modelStr = cli.ResolveModel(cfg)
	}
	if modeStr == "" {
		modeStr = cli.ResolveMode(cfg)
	}

	mode := agent.ModeGeneral
	if m, ok := agent.ParseMode(modeStr); ok {
		mode = m
	}

	llm, err := cli.BuildProvider(providerStr)
	if err != nil {
		log.Fatalf("build provider %q: %v", providerStr, err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	workingDir, _ := os.Getwd()

	covoAgent, err := agent.NewCovoAgent(agent.CovoAgentConfig{
		Mode:                     mode,
		Provider:                 llm,
		ProviderName:             providerStr,
		Model:                    modelStr,
		WorkingDir:               workingDir,
		HomeDir:                  homeDir,
		Logger:                   logger,
		ApprovalCfg:              approvalConfigFromCLI(cfg, yolo),
		ToolProfile:              runtimeState.ActiveProfile(),
		ThinkingCfg:              thinkingConfigFromCLI(cfg),
		FrequencyPenalty:         frequencyPenaltyFromCLI(cfg),
		PresencePenalty:          presencePenaltyFromCLI(cfg),
		SystemPrompt:             systemPrompt,
		AppendSystemPrompt:       appendSystemPrompt,
		Auxiliary:                auxiliaryConfigFromCLI(cfg),
		AuxiliaryProviderBuilder: cli.ResolveAuxiliaryProviderBuilder(),
	})
	if err != nil {
		log.Fatalf("create agent: %v", err)
	}
	defer covoAgent.Close()

	// Oneshot mode has no user present to approve dangerous commands — mark
	// the session as non-interactive so the manual-approval fallback auto-denies.
	if covoAgent.ApprovalSystem() != nil {
		covoAgent.ApprovalSystem().SetNonInteractive(true)
	}

	ctx := context.Background()
	result, err := covoAgent.Core().Run(ctx, prompt)
	if err != nil {
		log.Fatalf("agent run: %v", err)
	}

	if jsonOutput {
		usage := covoAgent.Core().State().TotalUsage()
		out := oneshotJSONResult{
			Result:   result,
			Provider: providerStr,
			Model:    modelStr,
			Tokens: tokenInfo{
				Prompt:     usage.PromptTokens,
				Completion: usage.CompletionTokens,
				Total:      usage.TotalTokens,
			},
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			log.Fatalf("encode output: %v", err)
		}
	} else {
		fmt.Print(result)
	}
}

type oneshotJSONResult struct {
	Result   string    `json:"result"`
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	Tokens   tokenInfo `json:"tokens"`
}

type tokenInfo struct {
	Prompt     int64 `json:"prompt"`
	Completion int64 `json:"completion"`
	Total      int64 `json:"total"`
}

func isTerminalFd(fd uintptr) bool {
	return cli.IsTerminal(fd)
}
