package commands

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/covoyage/covo-agent/internal/diffrender"
	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/component"
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

	showReasoning  bool
	thinkingMode   string
	thinkingStream bool
	historyMode    string
	verboseLevel   string
	gitTracker     *runtimeapp.GitBranchTracker

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
	sideBusy      atomic.Bool
	cancelRun     atomic.Pointer[context.CancelFunc]
	cancelSide    atomic.Pointer[context.CancelFunc]
	pendingImages sync.Map

	// Subagent view state: which sub-agent transcript panel is open ("" = none).
	subagentViewMu     sync.Mutex
	subagentViewOpenID string
	pasteStore         *agentui.PasteStore
	composerDrafts     map[string]component.EditorDraft
	restoredMessages   []agentcore.Message
}

func (s *interactiveSession) currentSessionID() string {
	if s.agentRuntime == nil {
		return ""
	}
	ca := s.agentRuntime.Current()
	if ca == nil {
		return ""
	}
	return ca.SessionManager().CurrentID()
}

func (s *interactiveSession) bindPromptQueue(sessionID string) {
	ca := s.agentRuntime.Current()
	if ca == nil {
		return
	}
	shared.RuntimeState.BindPromptQueueSession(context.Background(), ca.SessionManager(), sessionID)
}

func (s *interactiveSession) switchPromptQueue(sessionID string) {
	ca := s.agentRuntime.Current()
	if ca == nil {
		shared.RuntimeState.ResetPromptQueue()
		return
	}
	shared.RuntimeState.SwitchPromptQueue(context.Background(), ca.SessionManager(), sessionID)
}

func (s *interactiveSession) stashComposerDraft() {
	if s.app == nil {
		return
	}
	ed := s.app.Editor()
	if ed == nil {
		return
	}
	id := s.currentSessionID()
	if id == "" {
		return
	}
	draft := ed.CaptureDraft()
	if s.composerDrafts == nil {
		s.composerDrafts = make(map[string]component.EditorDraft)
	}
	if draft.Empty() {
		delete(s.composerDrafts, id)
		return
	}
	s.composerDrafts[id] = draft
}

func (s *interactiveSession) restoreComposerDraft() {
	if s.app == nil {
		return
	}
	ed := s.app.Editor()
	if ed == nil {
		return
	}
	id := s.currentSessionID()
	if id == "" {
		ed.Clear()
		return
	}
	ed.RestoreDraft(s.composerDrafts[id])
}

// RunInteractive launches the interactive TUI, or the one-shot path when a
// prompt was supplied via --print/--oneshot/--pipe.
func RunInteractive(opts *RunOptions, runtime *cli.CommandRuntime) {
	homeDir := runtime.HomeDir
	cfg := runtime.Cfg

	// Initialize session YOLO state from flag or environment.
	if opts.Yolo || os.Getenv("COVO_YOLO") == "1" || os.Getenv("COVO_YOLO") == "true" {
		shared.RuntimeState.SetSessionYolo(true)
	}

	printPrompt := opts.Print
	if printPrompt == "-" || (printPrompt == "" && !isTerminalFd(os.Stdin.Fd())) {
		data, _ := io.ReadAll(os.Stdin)
		printPrompt = strings.TrimSpace(string(data))
	}
	if printPrompt != "" {
		runOneshot(printPrompt, opts.Mode, opts.Provider, opts.Model, opts.Yolo, opts.JSON, opts.OutputFormat, opts.OutputSchema, opts.SystemPrompt, opts.AppendSystemPrompt, opts.Permission, opts.AddDir)
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

func loggingRetainDays(cfg *cli.Config) int {
	if cfg != nil && cfg.Logging != nil {
		return cfg.Logging.RetainDays
	}
	return 0
}

// run boots the agent and the TUI, then blocks until the app finishes.
func (s *interactiveSession) run() {
	// Redirect logs to a rotating file to prevent stderr from corrupting the TUI.
	logFile, err := logutil.OpenAppLog(s.homeDir, logutil.ResolveRetainDays(loggingRetainDays(s.cfg)))
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
	if _, _, err := applyCLIPermissionAndDirs(s.opts.Permission, s.opts.AddDir, s.workingDir, true, s.opts.Yolo); err != nil {
		log.Fatalf("%v", err)
	}

	s.showReasoning, s.thinkingMode, s.thinkingStream = shared.DisplayConfigFromCLI(s.cfg)
	s.historyMode = shared.HistoryModeFromCLI(s.cfg)
	s.verboseLevel = shared.VerboseLevelFromCLI(s.cfg)
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
	if s.verboseLevel != "" {
		s.app.History().SetVerboseLevel(s.verboseLevel)
	}
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
	agentui.CaptureTerminalProbe(s.app)
	if typeahead != "" {
		if ed := s.app.Editor(); ed != nil {
			ed.SetValue(ed.GetValue() + typeahead)
		}
	}

	skillCfgGet, skillCfgSet := shared.SkillConfigFns(s.cfg)
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
		SkillExtraDirs:           shared.SkillExtraDirs(s.cfg),
		SkillDisabled:            shared.SkillDisabled(s.cfg),
		SkillConfigGet:           skillCfgGet,
		SkillConfigSet:           skillCfgSet,
		SpawnNotifyFunc:          s.onSubagentComplete,
		LeanTools:                shared.LeanToolsEnabled(s.cfg),
		SystemPrompt:             s.opts.SystemPrompt,
		AppendSystemPrompt:       s.opts.AppendSystemPrompt,
		ExtraWorkspaceRoots:      shared.RuntimeState.ExtraWorkspaceRoots(),
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

	s.openOrResumeSession(covoAgent)
	s.bindPromptQueue(covoAgent.SessionManager().CurrentID())
	defer func() {
		shared.RuntimeState.PersistPromptQueue(context.Background())
		s.agentRuntime.Close()
	}()

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
	s.prepareAgent(covoAgent)
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
	s.installSubagentViewClickHandler()

	s.app.SetReady()
	s.startInteractiveFollowups()

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

func (s *interactiveSession) openOrResumeSession(covoAgent *agent.CovoAgent) {
	sessionMgr := covoAgent.SessionManager()
	ctx := context.Background()
	switch {
	case s.opts.SessionID != "":
		if err := sessionMgr.ResumeSession(ctx, s.opts.SessionID); err != nil {
			sessionMgr.SetCurrentSessionID(s.opts.SessionID)
		}
	case s.opts.Continue:
		id, err := sessionMgr.LatestSessionID(ctx, s.workingDir)
		if err != nil {
			log.Printf("lookup latest session: %v", err)
		}
		if id != "" {
			if err := sessionMgr.ResumeSession(ctx, id); err != nil {
				log.Printf("resume latest session: %v", err)
			}
		} else {
			sessionMgr.EnsureCurrentSession(ctx, s.workingDir)
		}
	default:
		sessionMgr.EnsureCurrentSession(ctx, s.workingDir)
	}
	_ = sessionMgr.EnsureSessionRow(ctx, sessionMgr.CurrentID(), s.workingDir)
	if snap, err := sessionMgr.LoadSession(ctx, sessionMgr.CurrentID()); err == nil && len(snap.Messages) > 0 {
		covoAgent.Core().State().Restore(snap)
		s.restoredMessages = snap.Messages
	}
}

func (s *interactiveSession) startInteractiveFollowups() {
	if s.opts.ShowSessionPicker {
		s.openSessions()
	}
	prompt := strings.TrimSpace(s.opts.InitialPrompt)
	if prompt == "" || s.opts.Print != "" {
		return
	}
	safego.SafeGo(func() {
		s.handleSubmit(context.Background(), prompt)
	}, s.logger)
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
