package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/cli/commands"
	"github.com/covoyage/covo-agent/internal/cli/commands/automation"
	"github.com/covoyage/covo-agent/internal/cli/commands/code"
	"github.com/covoyage/covo-agent/internal/cli/commands/config"
	"github.com/covoyage/covo-agent/internal/cli/commands/health"
	"github.com/covoyage/covo-agent/internal/cli/commands/integrations"
	"github.com/covoyage/covo-agent/internal/cli/commands/model"
	"github.com/covoyage/covo-agent/internal/cli/commands/prefs"
	"github.com/covoyage/covo-agent/internal/cli/commands/session"
	"github.com/covoyage/covo-agent/internal/cli/commands/setup"
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
	allowPatterns   []string
	denyPatterns    []string
	outputFormat    string
	headlessTimeout time.Duration
}

func newRootCommand() *cobra.Command {
	opts := &rootOptions{}
	runtime := &cli.CommandRuntime{}

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
				commands.ApplySandboxIfRequested(opts.sandbox, runtime)
			}
			return nil
		},
		Run: func(_ *cobra.Command, _ []string) {
			if opts.headless {
				commands.RunHeadless(&headless.Options{
					Prompt:             opts.oneshot,
					Tools:              opts.tools,
					DisallowedTools:    opts.disallowedTools,
					Allow:              opts.allowPatterns,
					Deny:               opts.denyPatterns,
					OutputFormat:       opts.outputFormat,
					Timeout:            opts.headlessTimeout,
					SystemPrompt:       opts.systemPrompt,
					AppendSystemPrompt: opts.appendSystemPrompt,
				})
				return
			}
			commands.RunInteractive(&commands.RunOptions{
				Mode:               opts.mode,
				Provider:           opts.provider,
				Model:              opts.model,
				Yolo:               opts.yolo,
				Oneshot:            opts.oneshot,
				Pipe:               opts.pipe,
				JSON:               opts.json,
				SystemPrompt:       opts.systemPrompt,
				AppendSystemPrompt: opts.appendSystemPrompt,
				SessionID:          opts.sessionID,
			}, runtime)
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
	flags.StringSliceVar(&opts.allowPatterns, "allow", nil, "permission patterns to auto-approve (e.g. 'edit:*', 'bash:ls *')")
	flags.StringSliceVar(&opts.denyPatterns, "deny", nil, "permission patterns to auto-deny")
	flags.StringVar(&opts.outputFormat, "output-format", "text", "output format in headless mode: text or streaming-json")
	flags.DurationVar(&opts.headlessTimeout, "timeout", 0, "timeout for headless mode execution")

	root.SuggestionsMinimumDistance = 2

	_ = root.RegisterFlagCompletionFunc("log-level", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"DEBUG", "INFO", "WARN", "ERROR"}, cobra.ShellCompDirectiveNoFileComp
	})

	root.AddCommand(model.NewModelCommand(runtime))
	root.AddCommand(health.NewVersionCommand())
	root.AddCommand(health.NewStatusCommand())
	root.AddCommand(config.NewConfigCommand(runtime))
	root.AddCommand(session.NewSessionCommand(runtime))
	root.AddCommand(health.NewDoctorCommand())
	root.AddCommand(integrations.NewMCPCommand())
	root.AddCommand(automation.NewCronCommand())
	root.AddCommand(automation.NewSkillCommand())
	root.AddCommand(session.NewMemoryCommand())
	root.AddCommand(health.NewUpdateCommand())
	root.AddCommand(setup.NewSetupCommand())
	root.AddCommand(code.NewAnalyzeCommand())
	root.AddCommand(code.NewPRCommand())
	root.AddCommand(config.NewAuthCommand())
	root.AddCommand(integrations.NewGatewayCommand(runtime))
	root.AddCommand(integrations.NewPairingCommand())
	root.AddCommand(integrations.NewPluginCommand(runtime))
	root.AddCommand(integrations.NewLSPCommand())
	root.AddCommand(prefs.NewLanguageCommand())
	root.AddCommand(prefs.NewProfileCommand())
	root.AddCommand(prefs.NewThemeCommand())
	root.AddCommand(integrations.NewACPCommand())
	root.AddCommand(integrations.NewExtCommand())
	root.AddCommand(commands.NewTemplateCommand())
	root.AddCommand(integrations.NewPackageCommand())
	root.AddCommand(config.NewFeaturesCommand())
	root.AddCommand(code.NewReviewCommand())
	root.AddCommand(session.NewCommitmentsCommand())
	root.AddCommand(code.NewWorktreeCommand())
	root.AddCommand(code.NewTestgenCommand())
	root.AddCommand(session.NewDreamingCommand())
	root.AddCommand(automation.NewRolloutCommand())
	root.AddCommand(automation.NewDebugCommand())
	root.AddCommand(session.NewBackupCommand())
	root.AddCommand(session.NewRestoreCommand())
	root.AddCommand(session.NewMigrateCommand())
	root.AddCommand(automation.NewHeartbeatCommand())
	root.AddCommand(newCompletionCommand(root))
	root.AddCommand(health.NewCrashReportCommand())

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

func initializeCommandRuntime(runtime *cli.CommandRuntime) error {
	if runtime.Cfg != nil {
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

	runtime.Cfg = cfg
	runtime.HomeDir = homeDir
	return nil
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
