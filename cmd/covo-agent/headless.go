package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/headless"
	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/mdstream"
	"github.com/covoyage/covonaut/agentcore"
)

func runHeadless(opts *headless.Options) {
	if err := headless.ValidateOptions(opts); err != nil {
		log.Fatalf("invalid headless options: %v", err)
	}

	cfg, err := cli.LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	cli.RegisterCustomProviders(cfg)
	homeDir, err := cli.EnsureHomeDir()
	if err != nil {
		log.Fatalf("home dir: %v", err)
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
		log.Fatalf("build provider %q: %v", providerStr, err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelError)}))
	workingDir, _ := os.Getwd()

	covoAgent, err := agent.NewCovoAgent(agent.CovoAgentConfig{
		Mode:               mode,
		Provider:           llm,
		ProviderName:       providerStr,
		Model:              modelStr,
		WorkingDir:         workingDir,
		HomeDir:            homeDir,
		Logger:             logger,
		ApprovalCfg:        approvalConfigFromCLI(cfg, true), // auto-approve in headless
		ToolProfile:        runtimeState.ActiveProfile(),
		ThinkingCfg:        thinkingConfigFromCLI(cfg),
		FrequencyPenalty:   frequencyPenaltyFromCLI(cfg),
		PresencePenalty:    presencePenaltyFromCLI(cfg),
		SystemPrompt:       opts.SystemPrompt,
		AppendSystemPrompt: opts.AppendSystemPrompt,
		Auxiliary:          auxiliaryConfigFromCLI(cfg),
		AuxiliaryProviderBuilder: cli.ResolveAuxiliaryProviderBuilder(),
	})
	if err != nil {
		log.Fatalf("create agent: %v", err)
	}
	defer covoAgent.Close()

	if covoAgent.ApprovalSystem() != nil {
		covoAgent.ApprovalSystem().SetNonInteractive(true)
	}

	// Apply permission gate patterns for auto-approval/denial
	if len(opts.Allow) > 0 || len(opts.Deny) > 0 {
		gate := headless.NewPermissionGate(opts)
		covoAgent.SetPermissionChecker(func(ctx context.Context, toolName string, args []byte) bool {
			result := gate.Check(toolName, string(args))
			// In headless mode, only explicitly allowed tools pass.
			// Unmatched tools are denied (safe default for non-interactive).
			return result == "allow"
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

	// Streaming JSON output
	var writer *headless.StreamingWriter
	if opts.OutputFormat == "streaming-json" {
		writer = headless.NewStreamingWriter(os.Stdout)
	}

	// For text output, use mdstream for incremental ANSI rendering
	var renderer *mdstream.Renderer
	if opts.OutputFormat != "streaming-json" {
		renderer = mdstream.NewRenderer()
		renderer.SetSyntaxHighlighting(true)
		unsub := covoAgent.Core().On(agentcore.EventMessageDelta, func(ev agentcore.Event) {
			if delta, ok := ev.(*agentcore.MessageDeltaEvent); ok && delta.Delta != "" {
				out := renderer.Feed(delta.Delta)
				if out != "" {
					fmt.Print(out)
				}
			}
		})
		defer unsub()
	}

	result, err := covoAgent.Core().Run(ctx, opts.Prompt)
	if err != nil {
		if renderer != nil {
			flushed := renderer.Flush()
			if flushed != "" {
				fmt.Print(flushed)
			}
		}
		if writer != nil {
			_ = writer.WriteError(err.Error())
			_ = writer.WriteDone()
		} else {
			log.Fatalf("agent run: %v", err)
		}
		return
	}

	if renderer != nil {
		flushed := renderer.Flush()
		if flushed != "" {
			fmt.Print(flushed)
		}
		fmt.Println()
	}

	if writer != nil {
		_ = writer.WriteText(result, 0)
		_ = writer.WriteDone()
	} else {
		if renderer == nil {
			fmt.Print(result)
		}
	}
}
