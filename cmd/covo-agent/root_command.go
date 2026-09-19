package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
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
	"github.com/covoyage/covo-agent/internal/workspace"
	"github.com/spf13/cobra"
)

type rootOptions struct {
	mode               string
	provider           string
	model              string
	yolo               bool
	print              string
	permission         string
	addDir             []string
	json               bool
	systemPrompt       string
	appendSystemPrompt string
	sessionID          string
	continueSession    bool
	showSessionPicker  bool
	sandbox            string

	// Logging
	logLevel string

	// Exec policy flags
	tools           []string
	disallowedTools []string
	allowPatterns   []string
	denyPatterns    []string
	outputFormat    string
	outputSchema    string
	headlessTimeout time.Duration
}

func newRootCommand() *cobra.Command {
	opts := &rootOptions{}
	runtime := &cli.CommandRuntime{}

	root := &cobra.Command{
		Use:          "covo-agent [prompt]",
		Short:        "General-purpose AI agent for the terminal",
		SilenceUsage: true,
		Args:         cobra.MaximumNArgs(1),
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := applyLogLevelFlag(opts); err != nil {
				return err
			}
			if err := initializeCommandRuntime(runtime); err != nil {
				return err
			}
			// Apply OS-level sandbox early, before any file operations.
			// Only apply for interactive/print and exec, not other subcommands.
			if cmd.Name() == "covo-agent" || cmd.Name() == "exec" {
				commands.ApplySandboxIfRequested(opts.sandbox, runtime)
			}
			return nil
		},
		Run: func(_ *cobra.Command, args []string) {
			initial := ""
			if len(args) > 0 {
				initial = strings.TrimSpace(args[0])
			}
			if opts.continueSession && opts.sessionID != "" {
				log.Fatal("--continue cannot be combined with --session-id")
			}
			commands.RunInteractive(&commands.RunOptions{
				Mode:               opts.mode,
				Provider:           opts.provider,
				Model:              opts.model,
				Yolo:               opts.yolo,
				Permission:         opts.permission,
				AddDir:             opts.addDir,
				Print:              opts.print,
				JSON:               opts.json,
				OutputFormat:       opts.outputFormat,
				OutputSchema:       opts.outputSchema,
				SystemPrompt:       opts.systemPrompt,
				AppendSystemPrompt: opts.appendSystemPrompt,
				SessionID:          opts.sessionID,
				Continue:           opts.continueSession,
				ShowSessionPicker:  opts.showSessionPicker,
				InitialPrompt:      initial,
			}, runtime)
		},
	}

	flags := root.PersistentFlags()
	flags.StringVar(&opts.mode, "mode", "", "agent mode: general (all-purpose) or code (coding-focused)")
	flags.StringVar(&opts.provider, "provider", "", "AI provider: openai, anthropic, gemini, xiaomi, openrouter, or custom")
	flags.StringVar(&opts.permission, "permission", "", "permission mode: ask|auto|full (TUI) or smart|full|off (exec)")
	flags.StringSliceVar(&opts.addDir, "add-dir", nil, "extra workspace directory (repeatable, max 16)")
	flags.StringVar(&opts.model, "model", "", "model name to use")
	flags.BoolVar(&opts.yolo, "yolo", false, "bypass all dangerous command approval prompts (use at your own risk)")
	flags.StringVarP(&opts.print, "print", "p", "", "run a single prompt and print the result (no TUI)")
	flags.StringVarP(&opts.print, "oneshot", "z", "", "alias for --print")
	flags.StringVar(&opts.print, "pipe", "", "alias for --print")
	flags.BoolVar(&opts.json, "json", false, "output structured JSON (only with --print/--oneshot/--pipe)")
	flags.StringVar(&opts.systemPrompt, "system-prompt", "", "replace the default system prompt content")
	flags.StringVar(&opts.appendSystemPrompt, "append-system-prompt", "", "append content to the system prompt")
	flags.StringVar(&opts.sessionID, "session-id", "", "resume or create a session with the given ID")
	flags.StringVar(&opts.sandbox, "sandbox", "", "OS-level sandbox profile: workspace, read-only, strict, devbox, off, or custom profile name")
	flags.StringVar(&opts.logLevel, "log-level", "", "log level: DEBUG, INFO, WARN, or ERROR (default: per-command)")

	// Exec policy flags
	flags.StringSliceVar(&opts.tools, "tools", nil, "whitelist of tool names to allow in exec mode")
	flags.StringSliceVar(&opts.disallowedTools, "disallowed-tools", nil, "blacklist of tool names to block in exec mode")
	flags.StringSliceVar(&opts.allowPatterns, "allow", nil, "permission patterns to auto-approve (e.g. 'edit:*', 'bash:ls *')")
	flags.StringSliceVar(&opts.denyPatterns, "deny", nil, "permission patterns to auto-deny")
	flags.StringVar(&opts.outputFormat, "output-format", "text", "output format: text, json, or streaming-json (alias: stream-json)")
	flags.StringVar(&opts.outputSchema, "output-schema", "", "JSON Schema for the final answer (inline object or file path)")
	flags.DurationVar(&opts.headlessTimeout, "timeout", 0, "timeout for exec mode")

	root.Flags().BoolVarP(&opts.continueSession, "continue", "c", false, "resume the latest session in the current directory")
	root.Flags().BoolVar(&opts.showSessionPicker, "session", false, "open the session picker on startup")

	root.SuggestionsMinimumDistance = 2

	_ = root.RegisterFlagCompletionFunc("log-level", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"DEBUG", "INFO", "WARN", "ERROR"}, cobra.ShellCompDirectiveNoFileComp
	})

	root.AddCommand(newExecCommand(opts))
	root.AddCommand(newInitCommand())
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

func (opts *rootOptions) nonInteractivePrompt() string {
	return opts.print
}

func (opts *rootOptions) headlessOptions(prompt string) *headless.Options {
	return &headless.Options{
		Prompt:             prompt,
		Tools:              opts.tools,
		DisallowedTools:    opts.disallowedTools,
		Permission:         opts.permission,
		Yolo:               opts.yolo,
		AddDir:             opts.addDir,
		Allow:              opts.allowPatterns,
		Deny:               opts.denyPatterns,
		OutputFormat:       opts.outputFormat,
		OutputSchema:       opts.outputSchema,
		Timeout:            opts.headlessTimeout,
		SystemPrompt:       opts.systemPrompt,
		AppendSystemPrompt: opts.appendSystemPrompt,
	}
}

func newInitCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init [directory]",
		Short: "Create AGENTS.md project guidance",
		Long:  "Write AGENTS.md in the given directory (default: current directory) if it does not already exist.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}
			path, created, err := workspace.WriteAgentsFile(dir, force)
			if err != nil {
				return err
			}
			if created {
				fmt.Printf("Wrote %s\n", path)
				return nil
			}
			fmt.Printf("%s already exists. Use --force to overwrite.\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing AGENTS.md")
	return cmd
}

func newExecCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "exec [prompt]",
		Short: "Run a non-interactive agent task",
		Long:  "Run one prompt without the TUI. Policy flags such as --tools, --allow, --deny, --output-format, and --output-schema apply here.",
		Args:  cobra.MaximumNArgs(1),
		Run: func(_ *cobra.Command, args []string) {
			prompt := ""
			if len(args) > 0 {
				prompt = strings.TrimSpace(args[0])
			}
			if prompt == "" {
				prompt = opts.nonInteractivePrompt()
			}
			if prompt == "-" || (prompt == "" && !cli.IsTerminal(os.Stdin.Fd())) {
				data, _ := io.ReadAll(os.Stdin)
				prompt = strings.TrimSpace(string(data))
			}
			commands.RunHeadless(opts.headlessOptions(prompt))
		},
	}
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
