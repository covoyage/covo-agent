package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/headless"
	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/spf13/cobra"
)

type rootOptions struct {
	mode               string
	provider           string
	model              string
	yolo               bool
	oneshot            string
	pipe               string
	json               bool
	systemPrompt       string
	appendSystemPrompt string
	sessionID          string
	sandbox            string

	// Logging
	logLevel string

	// Headless mode flags
	headless        bool
	tools           []string
	disallowedTools []string
	maxTurns        int
	allowPatterns   []string
	denyPatterns    []string
	outputFormat    string
	headlessTimeout time.Duration
}

type commandRuntime struct {
	cfg     *cli.Config
	homeDir string
}

func newRootCommand() *cobra.Command {
	opts := &rootOptions{}
	runtime := &commandRuntime{}

	root := &cobra.Command{
		Use:          "covo-agent",
		Short:        "General-purpose AI agent for the terminal",
		SilenceUsage: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			suggestions := cmd.SuggestionsFor(args[0])
			var sugg string
			if len(suggestions) > 0 {
				sugg += "\n\nDid you mean this?\n"
				for _, s := range suggestions {
					sugg += fmt.Sprintf("\t%s\n", s)
				}
			}
			return fmt.Errorf("unknown command %q for %q%s", args[0], cmd.CommandPath(), sugg)
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := applyLogLevelFlag(opts); err != nil {
				return err
			}
			if err := initializeCommandRuntime(runtime); err != nil {
				return err
			}
			// Apply OS-level sandbox early, before any file operations.
			// Only apply for the root command (interactive/oneshot), not subcommands.
			if cmd.Name() == "covo-agent" {
				applySandboxIfRequested(opts, runtime)
			}
			return nil
		},
		Run: func(_ *cobra.Command, _ []string) {
			if opts.headless {
				runHeadless(&headless.Options{
					Prompt:             opts.oneshot,
					Tools:              opts.tools,
					DisallowedTools:    opts.disallowedTools,
					MaxTurns:           opts.maxTurns,
					Allow:              opts.allowPatterns,
					Deny:               opts.denyPatterns,
					OutputFormat:       opts.outputFormat,
					Timeout:            opts.headlessTimeout,
					SystemPrompt:       opts.systemPrompt,
					AppendSystemPrompt: opts.appendSystemPrompt,
				})
				return
			}
			runInteractive(opts, runtime)
		},
	}

	flags := root.PersistentFlags()
	flags.StringVar(&opts.mode, "mode", "", "agent mode: general (all-purpose) or code (coding-focused)")
	flags.StringVar(&opts.provider, "provider", "", "AI provider: openai, anthropic, gemini, xiaomi, openrouter, or custom")
	flags.StringVar(&opts.model, "model", "", "model name to use")
	flags.BoolVar(&opts.yolo, "yolo", false, "bypass all dangerous command approval prompts (use at your own risk)")
	flags.StringVarP(&opts.oneshot, "oneshot", "z", "", "run a single prompt and output result to stdout (alias: --pipe)")
	flags.StringVar(&opts.pipe, "pipe", "", "alias for --oneshot")
	flags.BoolVar(&opts.json, "json", false, "output structured JSON (only with --oneshot/--pipe)")
	flags.StringVar(&opts.systemPrompt, "system-prompt", "", "replace the default system prompt content")
	flags.StringVar(&opts.appendSystemPrompt, "append-system-prompt", "", "append content to the system prompt")
	flags.StringVar(&opts.sessionID, "session-id", "", "resume or create a session with the given ID")
	flags.StringVar(&opts.sandbox, "sandbox", "", "OS-level sandbox profile: workspace, read-only, strict, devbox, off, or custom profile name")
	flags.StringVar(&opts.logLevel, "log-level", "", "log level: DEBUG, INFO, WARN, or ERROR (default: per-command)")

	// Headless mode flags
	flags.BoolVar(&opts.headless, "headless", false, "run in non-interactive headless mode (no TUI)")
	flags.StringSliceVar(&opts.tools, "tools", nil, "whitelist of tool names to allow in headless mode")
	flags.StringSliceVar(&opts.disallowedTools, "disallowed-tools", nil, "blacklist of tool names to block in headless mode")
	flags.IntVar(&opts.maxTurns, "max-turns", 0, "max agent turns in headless mode (0 = unlimited)")
	flags.StringSliceVar(&opts.allowPatterns, "allow", nil, "permission patterns to auto-approve (e.g. 'edit:*', 'bash:ls *')")
	flags.StringSliceVar(&opts.denyPatterns, "deny", nil, "permission patterns to auto-deny")
	flags.StringVar(&opts.outputFormat, "output-format", "text", "output format in headless mode: text or streaming-json")
	flags.DurationVar(&opts.headlessTimeout, "timeout", 0, "timeout for headless mode execution")

	root.SuggestionsMinimumDistance = 2

	_ = root.RegisterFlagCompletionFunc("log-level", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"DEBUG", "INFO", "WARN", "ERROR"}, cobra.ShellCompDirectiveNoFileComp
	})

	root.AddCommand(newModelCommand(runtime))
	root.AddCommand(newVersionCommand())
	root.AddCommand(newStatusCommand())
	root.AddCommand(newConfigCommand(runtime))
	root.AddCommand(newSessionCommand(runtime))
	root.AddCommand(newDoctorCommand())
	root.AddCommand(newMCPCommand())
	root.AddCommand(newCronCommand())
	root.AddCommand(newSkillCommand())
	root.AddCommand(newMemoryCommand())
	root.AddCommand(newUpdateCommand())
	root.AddCommand(newSetupCommand())
	root.AddCommand(newAnalyzeCommand())
	root.AddCommand(newPRCommand())
	root.AddCommand(newAuthCommand())
	root.AddCommand(newGatewayCommand(runtime))
	root.AddCommand(newPairingCommand())
	root.AddCommand(newPluginCommand(runtime))
	root.AddCommand(newLSPCommand())
	root.AddCommand(newLanguageCommand())
	root.AddCommand(newProfileCommand())
	root.AddCommand(newThemeCommand())
	root.AddCommand(newACPCommand())
	root.AddCommand(newExtCommand())
	root.AddCommand(newTemplateCommand())
	root.AddCommand(newPackageCommand())
	root.AddCommand(newFeaturesCommand())
	root.AddCommand(newReviewCommand())
	root.AddCommand(newCommitmentsCommand())
	root.AddCommand(newWorktreeCommand())
	root.AddCommand(newTestgenCommand())
	root.AddCommand(newDreamingCommand())
	root.AddCommand(newRolloutCommand())
	root.AddCommand(newBackupCommand())
	root.AddCommand(newRestoreCommand())
	root.AddCommand(newMigrateCommand())
	root.AddCommand(newHeartbeatCommand())
	root.AddCommand(newCompletionCommand(root))
	root.AddCommand(newCrashReportCommand())

	for _, registered := range plugin.GlobalCLICommands() {
		registered := registered
		root.AddCommand(&cobra.Command{
			Use:                registered.Name + " [args...]",
			Short:              registered.Description,
			DisableFlagParsing: true,
			RunE: func(_ *cobra.Command, args []string) error {
				return registered.Run(context.Background(), args)
			},
		})
	}

	return root
}

func applyLogLevelFlag(opts *rootOptions) error {
	if opts.logLevel == "" {
		return nil
	}
	lvl, err := logutil.ParseLevel(opts.logLevel)
	if err != nil {
		return err
	}
	logutil.SetLevel(lvl)
	return nil
}

func initializeCommandRuntime(runtime *commandRuntime) error {
	if runtime.cfg != nil {
		return nil
	}

	homeDir, err := cli.EnsureHomeDir()
	if err != nil {
		return fmt.Errorf("ensure home dir: %w", err)
	}
	if err := cli.LoadDotEnv(); err != nil {
		log.Printf("load .env: %v", err)
	}
	cfg, err := cli.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cli.RegisterCustomProviders(cfg)
	if cfg.Display != nil && cfg.Display.Language != "" {
		i18n.InitFromConfig(cfg.Display.Language)
	}

	runtime.cfg = cfg
	runtime.homeDir = homeDir
	return nil
}

func newModelCommand(runtime *commandRuntime) *cobra.Command {
	return &cobra.Command{
		Use:   "model",
		Short: "Select the inference provider and default model",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runModelCommand(runtime.cfg, runtime.homeDir)
		},
	}
}

func newCompletionCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:       "completion [bash|zsh|fish|powershell]",
		Short:     "Generate a shell completion script",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return root.GenPowerShellCompletion(cmd.OutOrStdout())
			default:
				return fmt.Errorf("unsupported shell %q", args[0])
			}
		},
	}
}
