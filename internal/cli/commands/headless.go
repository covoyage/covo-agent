package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"github.com/covoyage/covo-agent/internal/diffrender"
	"github.com/covoyage/covo-agent/internal/headless"
	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/mdstream"
	"github.com/covoyage/covo-agent/internal/telemetry"
	"github.com/covoyage/covonaut/agentcore"
)

func RunHeadless(opts *headless.Options) {
	// Flush buffered OTel spans before the process exits (log.Fatalf on the
	// error paths bypasses defers, so those branches flush explicitly too).
	defer telemetry.ShutdownOtel(context.Background())

	cfg, err := cli.LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	cli.RegisterCustomProviders(cfg)
	shared.RegisterCustomModes(cfg)
	if err := headless.ValidateOptions(opts); err != nil {
		log.Fatalf("invalid headless options: %v", err)
	}
	homeDir, err := cli.EnsureHomeDir()
	if err != nil {
		log.Fatalf("home dir: %v", err)
	}

	providerStr := cli.ResolveProvider(cfg)
	modelStr := cli.ResolveModel(cfg)
	modeStr := opts.Mode
	if modeStr == "" {
		modeStr = cli.ResolveMode(cfg)
	}

	mode, err := agent.RequireMode(modeStr)
	if err != nil {
		log.Fatalf("%v", err)
	}

	llm, err := cli.BuildProvider(providerStr)
	if err != nil {
		log.Fatalf("build provider %q: %v", providerStr, err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelError)}))
	workingDir, _ := os.Getwd()
	yolo := opts.Yolo || os.Getenv("COVO_YOLO") == "1" || os.Getenv("COVO_YOLO") == "true"
	permMode, extraRoots, err := applyCLIPermissionAndDirs(opts.Permission, opts.AddDir, workingDir, false, yolo)
	if err != nil {
		log.Fatalf("%v", err)
	}

	// --tools / --disallowed-tools hide tools from the model and hard-block
	// hallucinated calls; empty lists mean no filtering.
	var toolNameFilter func(string) bool
	if len(opts.Tools) > 0 || len(opts.DisallowedTools) > 0 {
		toolNameFilter = headless.NewToolFilter(opts).IsAllowed
	}
	skillCfgGet, skillCfgSet := shared.SkillConfigFns(cfg)

	outputSchema, err := headless.ParseOutputSchema(opts.OutputSchema)
	if err != nil {
		log.Fatalf("output schema: %v", err)
	}
	var responseFormat *agentcore.ResponseFormat
	if outputSchema != nil {
		responseFormat = agentcore.NewJSONSchemaResponseFormat("output", outputSchema)
	}

	covoAgent, err := agent.NewCovoAgent(agent.CovoAgentConfig{
		Mode:                     mode,
		Provider:                 llm,
		ProviderName:             providerStr,
		Model:                    modelStr,
		WorkingDir:               workingDir,
		HomeDir:                  homeDir,
		Logger:                   logger,
		ApprovalCfg:              shared.ApprovalConfigFromCLI(cfg, permMode == agent.PermissionFull),
		ToolProfile:              shared.RuntimeState.ActiveProfile(),
		ToolNameFilter:           toolNameFilter,
		ThinkingCfg:              shared.ThinkingConfigFromCLI(cfg),
		FrequencyPenalty:         shared.FrequencyPenaltyFromCLI(cfg),
		PresencePenalty:          shared.PresencePenaltyFromCLI(cfg),
		SystemPrompt:             opts.SystemPrompt,
		AppendSystemPrompt:       opts.AppendSystemPrompt,
		Auxiliary:                shared.AuxiliaryConfigFromCLI(cfg),
		AuxiliaryProviderBuilder: cli.ResolveAuxiliaryProviderBuilder(),
		SkillExtraDirs:           shared.SkillExtraDirs(cfg),
		SkillDisabled:            shared.SkillDisabled(cfg),
		SkillConfigGet:           skillCfgGet,
		SkillConfigSet:           skillCfgSet,
		LeanTools:                shared.LeanToolsEnabledFor(cfg, string(mode)),
		ResponseFormat:           responseFormat,
		ExtraWorkspaceRoots:      extraRoots,
		Surface:                  agent.SurfaceExec,
	})
	if err != nil {
		log.Fatalf("create agent: %v", err)
	}
	defer covoAgent.Close()

	if sys := covoAgent.ApprovalSystem(); sys != nil {
		sys.SetNonInteractive(true)
		agent.ApplyPermissionMode(sys, permMode)
	}

	// Apply permission gate patterns for auto-approval/denial
	var gate *headless.PermissionGate
	if len(opts.Allow) > 0 || len(opts.Deny) > 0 {
		gate = headless.NewPermissionGate(opts)
	}
	if toolNameFilter != nil || gate != nil {
		covoAgent.SetPermissionChecker(func(ctx context.Context, toolName string, args []byte) bool {
			// Tools hidden by --tools/--disallowed-tools are always denied,
			// even if an allow pattern would otherwise match.
			if toolNameFilter != nil && !toolNameFilter(toolName) {
				return false
			}
			if gate == nil {
				return true
			}
			// In headless mode, only explicitly allowed tools pass.
			// Unmatched tools are denied (safe default for non-interactive).
			return gate.Check(toolName, string(args)) == "allow"
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle timeout
	if opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// Handle SIGINT
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	format := headless.NormalizeOutputFormat(opts.OutputFormat)

	var writer *headless.StreamingWriter
	if format == "streaming-json" {
		writer = headless.NewStreamingWriter(os.Stdout)
		unsub := headless.BindStreamingWriter(covoAgent.Core(), writer)
		defer unsub()
	}

	var renderer *mdstream.Renderer
	if format == "text" {
		renderer = mdstream.NewRenderer()
		renderer.SetSyntaxHighlighting(diffrender.SyntaxEnabled())
		unsub := covoAgent.Core().On(agentcore.EventMessageDelta, func(ev agentcore.Event) {
			if delta, ok := ev.(*agentcore.MessageDeltaEvent); ok && delta.Delta != "" && delta.Kind != agentcore.BlockKindThinking {
				out := renderer.Feed(delta.Delta)
				if out != "" {
					fmt.Print(out)
				}
			}
		})
		defer unsub()
	}

	result, err := covoAgent.RunDirect(ctx, opts.Prompt)
	if renderer != nil {
		flushed := renderer.Flush()
		if flushed != "" {
			fmt.Print(flushed)
		}
		if err == nil {
			fmt.Println()
		}
	}
	if err != nil {
		if writer != nil {
			_ = writer.WriteError(err.Error())
			_ = writer.WriteDone()
			return
		}
		telemetry.ShutdownOtel(context.Background())
		log.Fatalf("agent run: %v", err)
	}

	payload := result
	if outputSchema != nil {
		payload = headless.ExtractJSONPayload(result)
		if valErr := agentcore.ValidateJSON(outputSchema, payload); valErr != nil {
			msg := fmt.Sprintf("structured output invalid: %v", valErr)
			if writer != nil {
				_ = writer.WriteError(msg)
				_ = writer.WriteDone()
				return
			}
			telemetry.ShutdownOtel(context.Background())
			log.Fatalf("%s", msg)
		}
	}

	switch format {
	case "streaming-json":
		_ = writer.WriteDone()
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if outputSchema != nil {
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
	}
}
