package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"github.com/covoyage/covo-agent/internal/headless"
	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/telemetry"
	"github.com/covoyage/covonaut/agentcore"
)

func runOneshot(prompt, modeStr, providerStr, modelStr string, yolo, jsonOutput bool, outputFormat, outputSchema, systemPrompt, appendSystemPrompt, permission string, addDir []string) {
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

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelError)}))
	workingDir, _ := os.Getwd()
	permMode, extraRoots, err := applyCLIPermissionAndDirs(permission, addDir, workingDir, false, yolo)
	if err != nil {
		log.Fatalf("%v", err)
	}
	skillCfgGet, skillCfgSet := shared.SkillConfigFns(cfg)

	schema, err := headless.ParseOutputSchema(outputSchema)
	if err != nil {
		log.Fatalf("output schema: %v", err)
	}
	var responseFormat *agentcore.ResponseFormat
	if schema != nil {
		responseFormat = agentcore.NewJSONSchemaResponseFormat("output", schema)
	}

	covoAgent, err := agent.NewCovoAgent(agent.CovoAgentConfig{
		Mode:                     mode,
		Provider:                 llm,
		ProviderName:             providerStr,
		Model:                    modelStr,
		WorkingDir:               workingDir,
		HomeDir:                  homeDir,
		Logger:                   logger,
		ApprovalCfg:              shared.ApprovalConfigFromCLI(cfg, yolo),
		ToolProfile:              shared.RuntimeState.ActiveProfile(),
		ThinkingCfg:              shared.ThinkingConfigFromCLI(cfg),
		FrequencyPenalty:         shared.FrequencyPenaltyFromCLI(cfg),
		PresencePenalty:          shared.PresencePenaltyFromCLI(cfg),
		SystemPrompt:             systemPrompt,
		AppendSystemPrompt:       appendSystemPrompt,
		Auxiliary:                shared.AuxiliaryConfigFromCLI(cfg),
		AuxiliaryProviderBuilder: cli.ResolveAuxiliaryProviderBuilder(),
		SkillExtraDirs:           shared.SkillExtraDirs(cfg),
		SkillDisabled:            shared.SkillDisabled(cfg),
		SkillConfigGet:           skillCfgGet,
		SkillConfigSet:           skillCfgSet,
		LeanTools:                shared.LeanToolsEnabled(cfg),
		ResponseFormat:           responseFormat,
		ExtraWorkspaceRoots:      extraRoots,
	})
	if err != nil {
		log.Fatalf("create agent: %v", err)
	}
	defer covoAgent.Close()

	// Oneshot mode has no user present to approve dangerous commands — mark
	// the session as non-interactive so the manual-approval fallback auto-denies.
	if sys := covoAgent.ApprovalSystem(); sys != nil {
		sys.SetNonInteractive(true)
		agent.ApplyPermissionMode(sys, permMode)
	}

	ctx := context.Background()
	// Flush buffered OTel spans before the process exits; the fatal error
	// path above already flushes explicitly.
	defer telemetry.ShutdownOtel(context.Background())
	result, err := covoAgent.RunDirect(ctx, prompt)
	if err != nil {
		telemetry.ShutdownOtel(context.Background())
		log.Fatalf("agent run: %v", err)
	}

	payload := result
	if schema != nil {
		payload = headless.ExtractJSONPayload(result)
		if valErr := agentcore.ValidateJSON(schema, payload); valErr != nil {
			telemetry.ShutdownOtel(context.Background())
			log.Fatalf("structured output invalid: %v", valErr)
		}
	}

	format := headless.NormalizeOutputFormat(outputFormat)
	if jsonOutput && format == "text" {
		format = "json"
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	switch format {
	case "json":
		if schema != nil {
			var parsed any
			if unmarshalErr := json.Unmarshal([]byte(payload), &parsed); unmarshalErr != nil {
				log.Fatalf("decode structured output: %v", unmarshalErr)
			}
			if encodeErr := enc.Encode(parsed); encodeErr != nil {
				log.Fatalf("encode output: %v", encodeErr)
			}
			return
		}
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
		if encodeErr := enc.Encode(out); encodeErr != nil {
			log.Fatalf("encode output: %v", encodeErr)
		}
	default:
		fmt.Print(payload)
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
