package commands

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/covoyage/covo-agent/internal/diffrender"
	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/theme"

	"github.com/covoyage/covo-agent/internal/agent"
	runtimeapp "github.com/covoyage/covo-agent/internal/app"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/cli/commands/setup"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"github.com/covoyage/covo-agent/internal/diff"
	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covo-agent/internal/slashcmd"
	agentui "github.com/covoyage/covo-agent/internal/tui"
)

// interactiveSession holds the shared state of one interactive TUI run: the
// resolved agent configuration, the live runtime objects, and the UI handles.
// All methods assume run() has initialized the fields they touch.
type interactiveSession struct {
	opts    *RunOptions
	runtime *cli.CommandRuntime
	cfg     *cli.Config
	homeDir string

	logger          *slog.Logger
	workingDir      string
	configTheme     string
	modelContextLen int64

	llm          agentcore.Provider
	providerType string
	model        string
	mode         agent.AgentMode

	showReasoning bool
	thinkingMode  string
	gitTracker    *runtimeapp.GitBranchTracker

	agent        *agent.CovoAgent
	agentFactory *runtimeapp.AgentFactory
	agentRuntime *runtimeapp.AgentRuntime
	bgManager    *runtimeapp.BackgroundManager

	app            *chat.ChatApp
	permissionGate *PermissionGate
	stickyFooter   *agentui.StickyFooter
	statusLineMgr  *agentui.StatusLineManager
	suggestionsMgr *agentui.SuggestionsManager
	slashContext   *slashcmd.ContextBuilder
	changedFiles   *ChangedFilesTracker

	busy          atomic.Bool
	cancelRun     atomic.Pointer[context.CancelFunc]
	pendingImages sync.Map
	pasteStore    *agentui.PasteStore
}

// RunInteractive launches the interactive TUI, or the one-shot path when a
// prompt was supplied via --oneshot/--pipe.
func RunInteractive(opts *RunOptions, runtime *cli.CommandRuntime) {
	homeDir := runtime.HomeDir
	cfg := runtime.Cfg

	// Initialize session YOLO state from flag or environment.
	if opts.Yolo || os.Getenv("COVO_YOLO") == "1" || os.Getenv("COVO_YOLO") == "true" {
		shared.RuntimeState.SetSessionYolo(true)
	}

	// Oneshot/pipe mode: run single prompt without TUI
	oneshotPrompt := opts.Oneshot
	if oneshotPrompt == "" {
		oneshotPrompt = opts.Pipe
	}
	if oneshotPrompt == "-" || (oneshotPrompt == "" && !isTerminalFd(os.Stdin.Fd())) {
		// Read from stdin (pipe mode)
		data, _ := io.ReadAll(os.Stdin)
		oneshotPrompt = strings.TrimSpace(string(data))
	}
	if oneshotPrompt != "" {
		runOneshot(oneshotPrompt, opts.Mode, opts.Provider, opts.Model, opts.Yolo, opts.JSON, opts.SystemPrompt, opts.AppendSystemPrompt)
		return
	}

	s := &interactiveSession{
		opts:    opts,
		runtime: runtime,
		cfg:     cfg,
		homeDir: homeDir,
	}
	s.run()
}

// run boots the agent and the TUI, then blocks until the app finishes.
func (s *interactiveSession) run() {
	// Redirect logs to a file to prevent stderr output from corrupting the TUI
	logFile, err := os.OpenFile(filepath.Join(s.homeDir, "covo-agent.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	var logWriter io.Writer = logFile
	if err != nil {
		log.Printf("open log file: %v", err)
		logWriter = io.Discard
	} else {
		defer logFile.Close()
	}
	s.logger = configureInteractiveLogging(logWriter)

	// Flag descriptions already updated during early init; skip duplicate i18n/config loading.
	if !cli.HasProviderConfigured() {
		setup.RunFirstTimeSetup(s.cfg, s.homeDir)
	}

	// East-Asian ambiguous characters must be measured the way the terminal
	// renders them, or every row containing one loses/gains a cell.
	switch s.cfg.AmbiguousWidthMode() {
	case "wide":
		core.SetAmbiguousWide(true)
	case "narrow":
		core.SetAmbiguousWide(false)
	default:
		core.SetAmbiguousWide(core.DetectAmbiguousWideFromEnv())
	}

	if err := theme.InitThemeFromEnv(); err != nil {
		log.Fatalf("theme: %v", err)
	}

	// Apply skin.yaml overrides if present
	if s.cfg.Display != nil {
		s.configTheme = s.cfg.Display.Theme
	}
	shared.ApplySkinOverrides(s.homeDir, s.configTheme)

	s.providerType = cli.ResolveProvider(s.cfg)
	if s.opts.Provider != "" {
		s.providerType = s.opts.Provider
	}
	s.model = cli.ResolveModel(s.cfg)
	if s.opts.Model != "" {
		s.model = s.opts.Model
	}
	modeStr := cli.ResolveMode(s.cfg)
	if s.opts.Mode != "" {
		modeStr = s.opts.Mode
	}

	mode, ok := agent.ParseMode(modeStr)
	if !ok {
		log.Fatalf("invalid mode %q: must be 'general' or 'code'", modeStr)
	}
	s.mode = mode

	llm, err := cli.BuildProvider(s.providerType)
	if err != nil {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintf(os.Stderr, "  Failed to initialize %s provider: %v\n", cli.ProviderDisplayName(s.providerType), err)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  Make sure your API key is set. You can:")
		fmt.Fprintln(os.Stderr, "    - Set it in ~/.covo-agent/.env")
		fmt.Fprintln(os.Stderr, "    - Export it as an environment variable")
		fmt.Fprintln(os.Stderr, "    - Run: covo-agent --setup")
		fmt.Fprintln(os.Stderr, "")
		os.Exit(1)
	}
	s.llm = llm

	fallbackTypes := shared.ParseFallbackProviders()
	if len(fallbackTypes) > 0 {
		llm, err = cli.BuildFallbackProvider(s.providerType, fallbackTypes, s.logger)
		if err != nil {
			log.Fatalf("build fallback provider: %v", err)
		}
		s.providerType = s.providerType + "+" + strings.Join(fallbackTypes, "+")
	}
	s.llm = llm

	workingDir, err := os.Getwd()
	if err != nil {
		log.Printf("get working dir: %v", err)
		workingDir = s.homeDir
	}
	if wd := os.Getenv("WORKING_DIR"); wd != "" {
		workingDir = wd
	}
	s.workingDir = workingDir
	workingDirOverride = workingDir

	s.showReasoning, s.thinkingMode = shared.DisplayConfigFromCLI(s.cfg)
	s.modelContextLen = shared.ResolveModelContextLength(s.cfg, s.providerType, s.model)

	var skillURLs []string
	if s.cfg.Skills != nil {
		skillURLs = s.cfg.Skills.URLs
	}

	// Register custom modes from config so they're available for
	// mode validation and system prompt selection.
	for _, cm := range s.cfg.CustomModes {
		def := &agent.CustomModeDefinition{
			Name:         cm.Name,
			Description:  cm.Description,
			SystemPrompt: cm.SystemPrompt,
		}
		if cm.Tools != nil {
			def.AllowTools = cm.Tools.Allow
			def.DenyTools = cm.Tools.Deny
		}
		agent.RegisterCustomMode(def)
	}

	slashSuggestions := slashcmd.BuildSlashSuggestions()
	atSuggestions := slashcmd.BuildAtSuggestions()
	s.app = s.buildChatApp(slashSuggestions, atSuggestions)
	s.app.SuppressAutoRetry = true
	s.app.History().SetTheme(chatHistoryTheme())
	shared.RuntimeState.SetUI(agentui.NewUIBus(s.app))
	s.app.HoldSubmit()
	if ed := s.app.Editor(); ed != nil {
		ed.SetPlaceholder(i18n.T("system.starting"))
	}
	typeahead := agentui.CaptureTypeahead(os.Stdin)
	if err := s.app.Start(); err != nil {
		log.Fatalf("start tui: %v", err)
	}
	defer s.app.Stop()
	if typeahead != "" {
		if ed := s.app.Editor(); ed != nil {
			ed.SetValue(ed.GetValue() + typeahead)
		}
	}

	s.agentFactory = runtimeapp.NewAgentFactory(agent.CovoAgentConfig{
		WorkingDir:               s.workingDir,
		HomeDir:                  s.homeDir,
		Logger:                   s.logger,
		CuratorCfg:               shared.CuratorConfig(s.cfg),
		ExecutionMode:            shared.ExecModeFromConfig(s.cfg),
		Concurrency:              int64(shared.ConcurrencyFromConfig(s.cfg)),
		ComputerUse:              shared.ComputerUseFromConfig(s.cfg),
		ContextEngine:            shared.ContextEngineFromConfig(s.cfg),
		MCPServers:               shared.MCPAgentConfig(s.cfg),
		ApprovalCfg:              shared.ApprovalConfigFromCLI(s.cfg, s.opts.Yolo),
		ThinkingCfg:              shared.ThinkingConfigFromCLI(s.cfg),
		FrequencyPenalty:         shared.FrequencyPenaltyFromCLI(s.cfg),
		PresencePenalty:          shared.PresencePenaltyFromCLI(s.cfg),
		ShowReasoning:            s.showReasoning,
		ThinkingMode:             s.thinkingMode,
		SkillURLs:                skillURLs,
		SystemPrompt:             s.opts.SystemPrompt,
		AppendSystemPrompt:       s.opts.AppendSystemPrompt,
		WorkspaceOnly:            shared.WorkspaceOnlyFromConfig(s.cfg),
		Auxiliary:                shared.AuxiliaryConfigFromCLI(s.cfg),
		AuxiliaryProviderBuilder: cli.ResolveAuxiliaryProviderBuilder(),
	}, shared.RuntimeState)

	covoAgent, err := s.agentFactory.New(runtimeapp.AgentRequest{
		Mode:          s.mode,
		Provider:      s.llm,
		ProviderName:  s.providerType,
		Model:         s.model,
		ContextLength: s.modelContextLen,
	})
	if err != nil {
		_ = s.app.Stop()
		log.Fatalf("create agent: %v", err)
	}
	s.agent = covoAgent

	s.bgManager = runtimeapp.NewBackgroundManager()
	s.agentRuntime = runtimeapp.NewAgentRuntime(s.agentFactory, covoAgent)
	runtimeServices := runtimeapp.NewRuntimeServices(s.homeDir, s.logger, s.agentRuntime)
	runtimeServices.Start(context.Background())
	defer runtimeServices.Stop()

	// ChangedFilesTracker tracks all files modified during the session.
	// Initialized early so it's available to the SlashContext and hotkeys.
	s.changedFiles = NewChangedFilesTracker(covoAgent.Core())

	// Create or ensure a session exists for persistence
	if s.opts.SessionID != "" {
		sessionMgr := covoAgent.SessionManager()
		if err := sessionMgr.ResumeSession(context.Background(), s.opts.SessionID); err != nil {
			// Session doesn't exist — start with this ID anyway
			sessionMgr.SetCurrentSessionID(s.opts.SessionID)
		}
	} else {
		covoAgent.SessionManager().EnsureCurrentSession(context.Background(), s.workingDir)
	}
	defer s.agentRuntime.Close()

	agentUIBinder := &runtimeapp.AgentUIBinder{
		App: func() *chat.ChatApp { return s.app },
		Footer: func() runtimeapp.UsageFooter {
			if s.stickyFooter == nil {
				return nil
			}
			return s.stickyFooter
		},
		PrintSystem: func(message string) {
			loadUIBus().PrintSystem(message)
		},
		Mode:  func() string { return string(s.mode) },
		Model: func() string { return s.model },
	}

	s.agentRuntime.OnReplace(func(replacement runtimeapp.AgentReplacement) {
		if s.changedFiles != nil {
			s.changedFiles.Rebind(replacement.Core)
		}
		if s.app != nil {
			agentUIBinder.Bind(replacement.Core)
			if replacement.Snapshot != nil {
				shared.RestoreChatHistory(s.app, replacement.Snapshot.Messages)
			}
		}
	})
	s.agentRuntime.SetPrepare(s.prepareAgent)

	s.wirePermissionGate()
	s.wireApprovalOverlay()
	s.wireHandoff()
	s.wireAskUser()

	s.suggestionsMgr = agentui.NewSuggestionsManager(func(text string) {
		if s.app == nil {
			return
		}
		s.handleSubmit(context.Background(), text)
	})

	s.wireFooter()

	s.gitTracker = runtimeapp.NewGitBranchTracker(s.workingDir)
	safego.SafeGo(s.pumpFooterStatus, nil)

	memoryMonitor := runtimeapp.NewMemoryMonitor(func(gib float64) {
		s.app.PrintSystem(i18n.T("system.memory_high", "gb", fmt.Sprintf("%.2f", gib)))
	})
	go memoryMonitor.Run(s.app.Done())

	theme.SetOnSemanticThemeChange(func() {
		s.app.History().SetTheme(chatHistoryTheme())
	})

	s.printWelcome()

	agentUIBinder.Bind(covoAgent.Core())
	agent.BindDiffPreviewer(covoAgent.Core(), s.workingDir, func(previews []diff.FileDiff) {
		if formatted := agentui.FormatDiffPreviews(previews); formatted != "" {
			s.app.PrintSystem(formatted)
		}
	})
	agentui.BindThinkingIndicator(s.app, covoAgent.Core())

	s.registerKeybindings()

	selectionAutoScroll := agentui.BindSelectionAutoScroll(s.app)
	if selectionDragMonitor := agentui.NewSelectionDragMonitor(selectionAutoScroll, s.app); selectionDragMonitor != nil {
		s.app.Host().AddChild(selectionDragMonitor)
	}

	s.installHotkeyRouter()

	s.app.SetReady()

	cronScheduler := s.buildCronScheduler()
	cronScheduler.Start(context.Background())
	defer cronScheduler.Stop()

	<-s.app.Done()
}

// chatHistoryTheme builds the chat history theme with fenced-code syntax
// highlighting configured from COVO_SYNTAX_HIGHLIGHT (see
// diffrender.SyntaxEnabled). Applied at startup and re-applied on theme
// switches so assistant markdown, diff fences, and content previews all
// follow the same switch.
func chatHistoryTheme() chat.ChatHistoryTheme {
	th := chat.DefaultChatHistoryTheme()
	syntax := diffrender.SyntaxEnabled()
	th.MarkdownTheme.DisableSyntax = !syntax
	th.MarkdownTheme.HighlightFence = func(source, lang string) string {
		return diffrender.HighlightCode(source, lang, syntax)
	}
	th.MarkdownTheme.FenceRenderer = func(lang, source string, width int64) []string {
		if lang != "mermaid" {
			return nil
		}
		rendered := agentui.RenderMermaid(source)
		return strings.Split(rendered, "\n")
	}
	return th
}

func configureInteractiveLogging(writer io.Writer) *slog.Logger {
	if writer == nil {
		writer = io.Discard
	}
	logger := slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelWarn)}))
	slog.SetDefault(logger)
	log.SetOutput(writer)
	return logger
}
