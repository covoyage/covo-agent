package main

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
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui"
	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/component"
	core "github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"

	"github.com/covoyage/covo-agent/internal/agent"
	runtimeapp "github.com/covoyage/covo-agent/internal/app"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/crash"
	"github.com/covoyage/covo-agent/internal/diff"
	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covo-agent/internal/slashcmd"
	toolsplanning "github.com/covoyage/covo-agent/internal/tools/planning"
	agentui "github.com/covoyage/covo-agent/internal/tui"
	agentpanels "github.com/covoyage/covo-agent/internal/tui/panels"
)

func main() {
	if h := newCrashHandler(); h != nil {
		defer h.RecoverAndReport()
	}
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// newCrashHandler builds the system-level crash handler so unrecovered panics
// in the main goroutine are written to ~/.covo-agent/crash-reports/ before the
// process exits. Returns nil if the home directory cannot be resolved.
func newCrashHandler() *crash.Handler {
	home, err := cli.HomeDir()
	if err != nil {
		return nil
	}
	h := crash.New(home)
	h.SetLogFile(filepath.Join(home, "covo-agent.log"))
	return h
}

func configureInteractiveLogging(writer io.Writer) *slog.Logger {
	if writer == nil {
		writer = io.Discard
	}
	logger := slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: slog.LevelWarn}))
	slog.SetDefault(logger)
	log.SetOutput(writer)
	return logger
}

func runInteractive(opts *rootOptions, runtime *commandRuntime) {
	modeFlag := &opts.mode
	providerFlag := &opts.provider
	modelFlag := &opts.model
	yoloFlag := &opts.yolo
	oneshotFlag := &opts.oneshot
	pipeFlag := &opts.pipe
	jsonFlag := &opts.json
	systemPromptFlag := &opts.systemPrompt
	appendSystemPromptFlag := &opts.appendSystemPrompt
	sessionIDFlag := &opts.sessionID
	homeDir := runtime.homeDir
	cfg := runtime.cfg

	// Initialize session YOLO state from flag or environment.
	if *yoloFlag || os.Getenv("COVO_YOLO") == "1" || os.Getenv("COVO_YOLO") == "true" {
		runtimeState.SetSessionYolo(true)
	}

	// Oneshot/pipe mode: run single prompt without TUI
	oneshotPrompt := *oneshotFlag
	if oneshotPrompt == "" {
		oneshotPrompt = *pipeFlag
	}
	if oneshotPrompt == "" {
		// Check for -z shorthand
		oneshotPrompt = opts.oneshot
	}
	if oneshotPrompt == "-" || (oneshotPrompt == "" && !isTerminalFd(os.Stdin.Fd())) {
		// Read from stdin (pipe mode)
		data, _ := io.ReadAll(os.Stdin)
		oneshotPrompt = strings.TrimSpace(string(data))
	}
	if oneshotPrompt != "" {
		runOneshot(oneshotPrompt, *modeFlag, *providerFlag, *modelFlag, *yoloFlag, *jsonFlag, *systemPromptFlag, *appendSystemPromptFlag)
		return
	}

	// Redirect logs to a file to prevent stderr output from corrupting the TUI
	logFile, err := os.OpenFile(filepath.Join(homeDir, "covo-agent.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	var logWriter io.Writer = logFile
	if err != nil {
		log.Printf("open log file: %v", err)
		logWriter = io.Discard
	} else {
		defer logFile.Close()
	}
	logger := configureInteractiveLogging(logWriter)

	// Flag descriptions already updated during early init; skip duplicate i18n/config loading.
	if !cli.HasProviderConfigured() {
		runFirstTimeSetup(cfg, homeDir)
	}

	if err := theme.InitThemeFromEnv(); err != nil {
		log.Fatalf("theme: %v", err)
	}

	// Apply skin.yaml overrides if present
	configTheme := ""
	if cfg.Display != nil {
		configTheme = cfg.Display.Theme
	}
	applySkinOverrides(homeDir, configTheme)

	providerType := cli.ResolveProvider(cfg)
	if *providerFlag != "" {
		providerType = *providerFlag
	}
	model := cli.ResolveModel(cfg)
	if *modelFlag != "" {
		model = *modelFlag
	}
	modeStr := cli.ResolveMode(cfg)
	if *modeFlag != "" {
		modeStr = *modeFlag
	}

	mode, ok := agent.ParseMode(modeStr)
	if !ok {
		log.Fatalf("invalid mode %q: must be 'general' or 'code'", modeStr)
	}

	llm, err := cli.BuildProvider(providerType)
	if err != nil {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintf(os.Stderr, "  Failed to initialize %s provider: %v\n", cli.ProviderDisplayName(providerType), err)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  Make sure your API key is set. You can:")
		fmt.Fprintln(os.Stderr, "    - Set it in ~/.covo-agent/.env")
		fmt.Fprintln(os.Stderr, "    - Export it as an environment variable")
		fmt.Fprintln(os.Stderr, "    - Run: covo-agent --setup")
		fmt.Fprintln(os.Stderr, "")
		os.Exit(1)
	}

	fallbackTypes := parseFallbackProviders()
	if len(fallbackTypes) > 0 {
		llm, err = cli.BuildFallbackProvider(providerType, fallbackTypes, logger)
		if err != nil {
			log.Fatalf("build fallback provider: %v", err)
		}
		providerType = providerType + "+" + strings.Join(fallbackTypes, "+")
	}

	workingDir, err := os.Getwd()
	if err != nil {
		log.Printf("get working dir: %v", err)
		workingDir = homeDir
	}
	if wd := os.Getenv("WORKING_DIR"); wd != "" {
		workingDir = wd
	}

	workingDirOverride = workingDir

	showReasoning, thinkingMode := displayConfigFromCLI(cfg)
	modelContextLen := resolveModelContextLength(cfg, providerType, model)

	var skillURLs []string
	if cfg.Skills != nil {
		skillURLs = cfg.Skills.URLs
	}

	// Register custom modes from config so they're available for
	// mode validation and system prompt selection.
	for _, cm := range cfg.CustomModes {
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

	agentFactory := runtimeapp.NewAgentFactory(agent.CovoAgentConfig{
		WorkingDir:               workingDir,
		HomeDir:                  homeDir,
		Logger:                   logger,
		CuratorCfg:               curatorConfig(cfg),
		ExecutionMode:            execModeFromConfig(cfg),
		Concurrency:              int64(concurrencyFromConfig(cfg)),
		ComputerUse:              computerUseFromConfig(cfg),
		ContextEngine:            contextEngineFromConfig(cfg),
		MCPServers:               mcpAgentConfig(cfg),
		ApprovalCfg:              approvalConfigFromCLI(cfg, *yoloFlag),
		ThinkingCfg:              thinkingConfigFromCLI(cfg),
		FrequencyPenalty:         frequencyPenaltyFromCLI(cfg),
		PresencePenalty:          presencePenaltyFromCLI(cfg),
		ShowReasoning:            showReasoning,
		ThinkingMode:             thinkingMode,
		SkillURLs:                skillURLs,
		SystemPrompt:             *systemPromptFlag,
		AppendSystemPrompt:       *appendSystemPromptFlag,
		WorkspaceOnly:            workspaceOnlyFromConfig(cfg),
		Auxiliary:                auxiliaryConfigFromCLI(cfg),
		AuxiliaryProviderBuilder: cli.ResolveAuxiliaryProviderBuilder(),
	}, runtimeState)

	covoAgent, err := agentFactory.New(runtimeapp.AgentRequest{
		Mode:          mode,
		Provider:      llm,
		ProviderName:  providerType,
		Model:         model,
		ContextLength: modelContextLen,
	})
	if err != nil {
		log.Fatalf("create agent: %v", err)
	}

	var busy atomic.Bool
	var cancelRun atomic.Pointer[context.CancelFunc]
	var bgManager = runtimeapp.NewBackgroundManager()
	var statusLineMgr *agentui.StatusLineManager
	var suggestionsMgr *agentui.SuggestionsManager
	var permissionGate *PermissionGate
	var pendingImages sync.Map
	var appPtr *chat.ChatApp
	agentRuntime := runtimeapp.NewAgentRuntime(agentFactory, covoAgent)
	runtimeServices := runtimeapp.NewRuntimeServices(homeDir, logger, agentRuntime)
	runtimeServices.Start(context.Background())
	defer runtimeServices.Stop()

	// ChangedFilesTracker tracks all files modified during the session.
	// Declared early so it's in scope for the SlashContext closure below.
	changedFilesTracker := NewChangedFilesTracker(covoAgent.Core())

	// Create or ensure a session exists for persistence
	if *sessionIDFlag != "" {
		sessionMgr := covoAgent.SessionManager()
		if err := sessionMgr.ResumeSession(context.Background(), *sessionIDFlag); err != nil {
			// Session doesn't exist — start with this ID anyway
			sessionMgr.SetCurrentSessionID(*sessionIDFlag)
		}
	} else {
		covoAgent.SessionManager().EnsureCurrentSession(context.Background(), workingDir)
	}
	defer agentRuntime.Close()
	currentMode := mode

	prepareAgent := func(newCA *agent.CovoAgent) {
		if newCA == nil {
			return
		}
		if permissionGate != nil {
			newCA.SetPermissionChecker(permissionGate.Checker())
		}
		if runtimeState.SessionYolo() {
			if approvalSys := newCA.ApprovalSystem(); approvalSys != nil {
				approvalSys.EnableSessionYolo("cli")
			}
		}
	}
	agentRuntime.SetPrepare(prepareAgent)

	requestFor := func(m agent.AgentMode, provider agentcore.Provider, providerName, modelName string) runtimeapp.AgentRequest {
		return runtimeapp.AgentRequest{
			Mode:          m,
			Provider:      provider,
			ProviderName:  providerName,
			Model:         modelName,
			ContextLength: resolveModelContextLength(cfg, providerName, modelName),
		}
	}

	createAgent := func(m agent.AgentMode) *agent.CovoAgent {
		newCA, err := agentFactory.New(requestFor(m, llm, providerType, model))
		if err != nil {
			log.Printf("create agent: %v", err)
			return nil
		}
		prepareAgent(newCA)
		return newCA
	}

	replaceAgent := func(request runtimeapp.AgentRequest, preserveState bool) (*agent.CovoAgent, error) {
		replacement, err := agentRuntime.Replace(request, preserveState)
		if err != nil {
			return nil, err
		}
		return replacement.Agent, nil
	}

	// StickyFooter placeholder declared before switchToMode for closure capture
	var stickyFooter *agentui.StickyFooter

	agentUIBinder := &runtimeapp.AgentUIBinder{
		App: func() *chat.ChatApp { return appPtr },
		Footer: func() runtimeapp.UsageFooter {
			if stickyFooter == nil {
				return nil
			}
			return stickyFooter
		},
		PrintSystem: func(message string) {
			loadUIBus().PrintSystem(message)
		},
	}

	agentRuntime.OnReplace(func(replacement runtimeapp.AgentReplacement) {
		if changedFilesTracker != nil {
			changedFilesTracker.Rebind(replacement.Core)
		}
		if appPtr != nil {
			agentUIBinder.Bind(replacement.Core)
			if replacement.Snapshot != nil {
				restoreChatHistory(appPtr, replacement.Snapshot.Messages)
			}
		}
	})

	switchToMode := func(newMode agent.AgentMode) {
		_, err := replaceAgent(requestFor(newMode, llm, providerType, model), true)
		if err != nil {
			log.Printf("replace agent: %v", err)
			return
		}
		currentMode = newMode
		if stickyFooter != nil {
			stickyFooter.SetMode(string(newMode))
		}
	}

	switchModel := func(newModel string) {
		_, err := replaceAgent(requestFor(currentMode, llm, providerType, newModel), true)
		if err != nil {
			log.Printf("replace agent: %v", err)
			return
		}
		model = newModel
		cfg.Model = newModel
		if err := cli.SaveConfig(cfg); err != nil {
			log.Printf("save config: %v", err)
		}
		if appPtr != nil {
			loadUIBus().UpdateStatusBar(providerType, newModel, string(currentMode))
		}
	}

	switchProviderModel := func(newProvider, newModel string) error {
		if err := cli.ValidateProvider(newProvider); err != nil {
			return err
		}
		if !cli.HasProviderConfiguredFor(newProvider) {
			env := cli.ProviderAPIKeyEnv(newProvider)
			return fmt.Errorf("%s is not set", env)
		}

		newLLM, err := cli.BuildProvider(newProvider)
		if err != nil {
			return fmt.Errorf("build provider %s: %w", newProvider, err)
		}

		normalizedProvider := cli.ProviderName(newProvider)
		_, err = replaceAgent(requestFor(currentMode, newLLM, normalizedProvider, newModel), true)
		if err != nil {
			return fmt.Errorf("create agent for provider %s: %w", normalizedProvider, err)
		}
		providerType = normalizedProvider
		model = newModel
		cfg.Provider = providerType
		cfg.Model = model
		if err := cli.SaveConfig(cfg); err != nil {
			log.Printf("save config: %v", err)
		}
		llm = newLLM
		if appPtr != nil {
			loadUIBus().UpdateStatusBar(newProvider, newModel, string(currentMode))
		}
		return nil
	}

	switchProvider := func(newProvider string) error {
		return switchProviderModel(newProvider, cli.DefaultModel(newProvider))
	}

	slashSuggestions := slashcmd.BuildSlashSuggestions()
	atSuggestions := slashcmd.BuildAtSuggestions()

	openModelPicker := func() {
		if appPtr == nil {
			return
		}
		showTUIModelPicker(appPtr, providerType, model, cfg, switchProviderModel)
	}

	var slashContextBuilder *slashcmd.ContextBuilder
	var handleSubmit func(ctx context.Context, input string)
	handleSubmit = func(ctx context.Context, input string) {
		trimmed := strings.TrimSpace(input)
		if trimmed == "" {
			return
		}

		// Handle pending approval via text input (y/s/a/n keys).
		if permissionGate != nil && permissionGate.HasPending() {
			lower := strings.ToLower(trimmed)
			switch lower {
			case "y", "yes", "o", "once":
				permissionGate.Respond(ChoiceOnce)
			case "s", "session":
				permissionGate.Respond(ChoiceSession)
			case "a", "always":
				permissionGate.Respond(ChoiceAlways)
			case "n", "no", "d", "deny":
				permissionGate.Respond(ChoiceDeny)
			}
			return
		}

		if strings.HasPrefix(trimmed, "!!") && trimmed != "!!" {
			cmdStr := strings.TrimSpace(trimmed[2:])
			if cmdStr != "" {
				executeShellCommand(ctx, cmdStr, workingDir, appPtr, &busy)
			}
			return
		}

		if strings.HasPrefix(trimmed, bashModePrefix) && !strings.HasPrefix(trimmed, "!/") && trimmed != "!" {
			cmdStr := strings.TrimSpace(trimmed[1:])
			if cmdStr != "" {
				executeShellCommand(ctx, cmdStr, workingDir, appPtr, &busy)
			}
			return
		}

		if strings.HasPrefix(trimmed, "/") {
			// Handle /yolo inline since it needs per-session state
			if trimmed == "/yolo" || strings.HasPrefix(trimmed, "/yolo ") {
				nowYolo := runtimeState.ToggleSessionYolo()
				if ca := agentRuntime.Current(); ca != nil {
					if approvalSys := ca.ApprovalSystem(); approvalSys != nil {
						if nowYolo {
							approvalSys.EnableSessionYolo("cli")
						} else {
							approvalSys.DisableSessionYolo("cli")
						}
					}
				}
				if permissionGate != nil {
					permissionGate.YoloMode = nowYolo
				}
				if nowYolo {
					loadUIBus().PrintSystem(i18n.T("system.yolo_on"))
				} else {
					loadUIBus().PrintSystem(i18n.T("system.yolo_off"))
				}
				return
			}

			// Handle /approve inline since it needs per-session permission gate state
			if trimmed == "/approve" || strings.HasPrefix(trimmed, "/approve ") {
				if permissionGate == nil {
					loadUIBus().PrintSystem("(approval gate not available)")
					return
				}
				parts := strings.Fields(trimmed)
				if len(parts) < 2 {
					loadUIBus().PrintSystem(permissionGate.FormatApprovalStatus())
					return
				}
				arg := strings.ToLower(parts[1])
				if arg == "all" {
					// Toggle all categories
					allOn := len(permissionGate.AutoApprovedCategories()) == len(categoryOrder)
					for _, cat := range categoryOrder {
						if allOn {
							// Turn all off
							if permissionGate.IsCategoryAutoApproved(cat) {
								permissionGate.ToggleCategoryAutoApprove(cat)
							}
						} else {
							// Turn all on
							if !permissionGate.IsCategoryAutoApproved(cat) {
								permissionGate.ToggleCategoryAutoApprove(cat)
							}
						}
					}
					if allOn {
						loadUIBus().PrintSystem("🔒 All categories set to manual approval.")
					} else {
						loadUIBus().PrintSystem("✅ All categories set to auto-approve.")
					}
					return
				}
				var cat ApprovalCategory
				switch arg {
				case "edit", "edits", "file", "files":
					cat = CatEdit
				case "bash", "shell", "cmd":
					cat = CatBash
				case "git":
					cat = CatGit
				case "delete", "del", "rm":
					cat = CatDelete
				case "exec", "code", "execute":
					cat = CatExec
				case "desktop", "computer":
					cat = CatDesktop
				default:
					loadUIBus().PrintSystem(fmt.Sprintf("Unknown category: %s\nUsage: /approve [edit|bash|git|delete|exec|desktop|all]", arg))
					return
				}
				now := permissionGate.ToggleCategoryAutoApprove(cat)
				if now {
					loadUIBus().PrintSystem(fmt.Sprintf("✅ Auto-approve ON: %s", categoryLabel(cat)))
				} else {
					loadUIBus().PrintSystem(fmt.Sprintf("🔒 Manual approval: %s", categoryLabel(cat)))
				}
				return
			}
			if slashContextBuilder == nil {
				return
			}
			handled := slashcmd.HandleSlashCommand(slashContextBuilder.Build(trimmed, ctx, providerType, model))
			if handled {
				return
			}
		}

		if busy.Load() {
			switch runtimeState.BusyInputMode() {
			case "interrupt":
				if cf := cancelRun.Load(); cf != nil && *cf != nil {
					(*cf)()
					loadUIBus().PrintSystem(i18n.T("system.interrupted"))
				}
			case "queue":
				runtimeState.SetPendingInput(trimmed)
				loadUIBus().PrintSystem(i18n.T("system.queued"))
			case "steer":
				// Steering: inject as follow-up message
				loadUIBus().PrintSystem(i18n.T("system.steer_unsupported"))
			default: // "block"
				loadUIBus().PrintSystem(i18n.T("system.busy"))
			}
			return
		}

		ag := agentRuntime.Core()
		if ag == nil {
			newCA, err := replaceAgent(requestFor(currentMode, llm, providerType, model), false)
			if err != nil {
				log.Printf("replace agent: %v", err)
				return
			}
			ag = newCA.Core()
		}

		ctxLen := int64(128000)
		if modelContextLen > 0 {
			ctxLen = modelContextLen
		}
		if ca := agentRuntime.Current(); ca != nil {
			if ce := ca.Core().ContextEngine(); ce != nil {
				if cl := ce.ContextLength(); cl > 0 {
					ctxLen = cl
				}
			}
		}

		result := agent.PreprocessContextReferences(trimmed, workingDir, ctxLen)
		if len(result.Warnings) > 0 && appPtr != nil {
			for _, w := range result.Warnings {
				loadUIBus().PrintSystem("⚠ " + w)
			}
		}
		if result.Blocked {
			return
		}
		trimmed = result.Message

		// Expand [image:name] placeholders to full file paths from pasted images.
		pendingImages.Range(func(key, value any) bool {
			name, _ := key.(string)
			path, _ := value.(string)
			if name != "" && path != "" {
				trimmed = strings.ReplaceAll(trimmed, "[image:"+name+"]", "[image: "+path+"]")
			}
			pendingImages.Delete(key)
			return true
		})

		ctx, cancel := context.WithCancel(ctx)
		cancelRun.Store(&cancel)
		busy.Store(true)
		safego.SafeGo(func() {
			defer busy.Store(false)
			defer func() { cancelRun.Store(nil) }()
			ag.Run(ctx, trimmed)

			// Process queued input if any
			if queued := runtimeState.TakePendingInput(); queued != "" {
				if appPtr != nil {
					loadUIBus().PrintSystem(i18n.T("system.processing_queued", "text", truncate(queued, 60)))
				}
				handleSubmit(ctx, queued)
			}
		}, nil)
	}

	app := tui.NewChatApp(chat.ChatAppConfig{
		Title: fmt.Sprintf(
			"covo-agent · provider=%s model=%s mode=%s",
			providerType, model, mode,
		),
		ReasoningRenderer: &chat.DefaultReasoningRenderer{
			Show: showReasoning,
			Mode: thinkingMode,
		},
		ShowTimings:        true,
		ShowTurns:          true,
		AltScreen:          true,
		MouseMode:          defaultMouseMode(),
		KittyKeyboardMode:  defaultKeyboardMode(),
		KittyKeyboardFlags: defaultKeyboardFlags(),
		Providers: []core.AutocompleteProvider{
			&component.StaticProvider{
				TriggerStr:  "/",
				Suggestions: slashSuggestions,
			},
			// File/folder providers — trigger on "@file" and "@folder" for
			// interactive filesystem navigation.
			agentui.NewFilePathBrowser("@file", workingDir, func() bool {
				return cli.IsEnabled("fuzzy-file-search")
			}),
			agentui.NewFilePathBrowser("@folder", workingDir, func() bool {
				return cli.IsEnabled("fuzzy-file-search")
			}),
			&component.StaticProvider{
				TriggerStr:  "@",
				Suggestions: atSuggestions,
			},
		},
		OnSubmit: handleSubmit,
		OnInterrupt: func() {
			if cf := cancelRun.Load(); cf != nil && *cf != nil {
				(*cf)()
				loadUIBus().PrintSystem(i18n.T("system.interrupted"))
			}
		},
		OnImagePaste: func() {
			path, err := cli.ClipboardImagePaste()
			if err != nil {
				if appPtr != nil {
					loadUIBus().PrintSystem(i18n.T("system.no_image"))
				}
				return
			}
			if appPtr != nil {
				// Inject image reference into editor
				ed := loadUIBus().Editor()
				ed.SetValue(ed.GetValue() + " [image:" + filepath.Base(path) + "]")
				loadUIBus().PrintSystem(i18n.T("system.image_pasted", "name", filepath.Base(path)))
				// Store the path for submit-time injection
				pendingImages.Store(filepath.Base(path), path)
			}
		},
	})
	appPtr = app
	appPtr.SuppressAutoRetry = true
	runtimeState.SetUI(agentui.NewUIBus(appPtr))
	permissionGate = NewPermissionGate(app)
	permissionGate.YoloMode = runtimeState.SessionYolo()
	covoAgent.SetPermissionChecker(permissionGate.Checker())

	// Wire pre-edit diff approval: when the agent attempts to modify files,
	// show a diff and ask the user to approve before the tool runs.
	// Uses the existing approval overlay for the UI.
	covoAgent.SetPreEditDiffChecker(func(ctx context.Context, toolName, filePath, diffText string) bool {
		// Print the diff to the chat history for context.
		loadUIBus().PrintSystem(fmt.Sprintf("── Proposed Edit: %s → %s ──", toolName, filepath.Base(filePath)))
		loadUIBus().PrintSystem(diffText)

		// Show the approval overlay.
		approved := make(chan bool, 1)
		permissionGate.showApprovalOverlay(
			fmt.Sprintf("Approve %s to %s?", toolName, filepath.Base(filePath)),
			func(choice ApprovalChoice) {
				approved <- (choice != ChoiceDeny)
			},
			func() {
				approved <- false
			},
		)
		select {
		case ok := <-approved:
			return ok
		case <-ctx.Done():
			return false
		}
	})

	// Wire approval system to permission gate for session/permanent approvals
	if approvalSys := covoAgent.ApprovalSystem(); approvalSys != nil {
		permissionGate.ApprovalSystem = &approvalBridge{system: approvalSys}
	}
	permissionGate.PendingPatternProvider = covoAgent.PendingApprovalPattern

	// Wire approval picker: overlay anchored near bottom, feels inline.
	permissionGate.showApprovalOverlay = func(prompt string, onChoose func(ApprovalChoice), onCancel func()) {
		host := loadUIBus().Host()
		if host == nil {
			onCancel()
			return
		}
		var ov chat.OverlayRef
		picker := agentpanels.NewApprovalPicker(prompt,
			func(choice agentpanels.ApprovalChoice) {
				onChoose(ApprovalChoice(choice))
				if ov != nil {
					host.RemoveOverlay(ov)
				}
				host.Focus(loadUIBus().Editor())
			},
			func() {
				onCancel()
				if ov != nil {
					host.RemoveOverlay(ov)
				}
				host.Focus(loadUIBus().Editor())
			},
		)
		ov = agentui.NewAnchoredOverlay(picker, 4, 50, 50, 50, 25)
		host.PushOverlay(ov)
	}

	// Wire TUI-friendly handoff: show message inline rather than via os.Stderr.
	covoAgent.SetHandoffCallback(func(ctx context.Context, message string) (string, error) {
		loadUIBus().PrintSystem("── HANDOFF ──")
		loadUIBus().PrintSystem(message)
		return agent.ReadStdinLine(ctx)
	})

	// Display a random feature discovery tip
	app.PrintSystem(i18n.T("system.tip_prefix", "tip", agentui.RandomTip()))

	suggestionsMgr = agentui.NewSuggestionsManager(func(text string) {
		if appPtr == nil {
			return
		}
		handleSubmit(context.Background(), text)
	})

	stickyFooter = agentui.NewStickyFooter()
	stickyFooter.SetShortcuts(i18n.T("statusline.shortcuts"))
	stickyFooter.SetMode(string(mode))

	statusLineMgr = agentui.NewStatusLineManager()
	stickyFooter.SetStatusLineManager(statusLineMgr)
	slashContextBuilder = newSlashContextBuilder(slashCompositionConfig{
		App:         appPtr,
		Busy:        &busy,
		Agents:      agentRuntime,
		State:       runtimeState,
		ActiveMode:  func() agent.AgentMode { return currentMode },
		CreateAgent: createAgent,
		ReplaceAgent: func(mode agent.AgentMode, preserveState bool) *agent.CovoAgent {
			replacement, err := replaceAgent(requestFor(mode, llm, providerType, model), preserveState)
			if err != nil {
				log.Printf("replace agent: %v", err)
			}
			return replacement
		},
		SwitchToMode:      switchToMode,
		SwitchModel:       switchModel,
		SwitchProvider:    switchProvider,
		OpenModelPicker:   openModelPicker,
		BackgroundManager: bgManager,
		StatusLineManager: statusLineMgr,
		WorkingDir:        workingDir,
		HomeDir:           homeDir,
		ChangedFiles:      changedFilesTracker,
	})
	statusLineMgr.SetRenderFn("mode", func(pal *theme.Palette) string {
		md := stickyFooter.Snapshot().Mode
		if md == "" {
			return ""
		}
		modeIcon := "◇"
		modeStyle := pal.Dim
		switch md {
		case "code":
			modeIcon = "⚙"
			modeStyle = pal.Accent
			md = i18n.T("statusline.mode_code")
		case "general":
			modeIcon = "◆"
			modeStyle = pal.Success
			md = i18n.T("statusline.mode_general")
		}
		// Append Plan/Act phase indicator when in Plan mode.
		if ca := agentRuntime.Current(); ca != nil && ca.IsPlanMode() {
			md = md + " │ " + pal.Accent.Render("📋 Plan")
		}
		return modeStyle.Render(fmt.Sprintf("%s %s", modeIcon, md))
	})
	statusLineMgr.SetRenderFn("bg-tasks", func(pal *theme.Palette) string {
		n := stickyFooter.Snapshot().BgTaskCount
		if n <= 0 {
			return ""
		}
		return pal.Accent.Render(i18n.T("statusline.bg_tasks", "count", fmt.Sprintf("%d", n)))
	})
	statusLineMgr.SetRenderFn("git-branch", func(pal *theme.Palette) string {
		b := stickyFooter.Snapshot().GitBranch
		if b == "" {
			return ""
		}
		return pal.Dim.Render(b)
	})
	statusLineMgr.SetRenderFn("context-used", func(pal *theme.Palette) string {
		c := stickyFooter.Snapshot().ContextUsed
		if c == "" {
			return ""
		}
		return pal.Dim.Render(c)
	})
	statusLineMgr.SetRenderFn("shortcuts", func(pal *theme.Palette) string {
		s := stickyFooter.Snapshot().Shortcuts
		if s == "" {
			return ""
		}
		return pal.Dim.Render(s)
	})

	app.SetFooter(stickyFooter)

	gitTracker := runtimeapp.NewGitBranchTracker(workingDir)
	safego.SafeGo(func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-app.Done():
				gitTracker.Stop()
				return
			case <-ticker.C:
				branch := gitTracker.Branch()
				stickyFooter.SetGitBranch(branch)
				stickyFooter.SetTodoStore(func() []agentui.TodoItem {
					ca := agentRuntime.Current()
					if ca == nil {
						return nil
					}
					todos := ca.TodoStore().Read()
					items := make([]agentui.TodoItem, 0, len(todos))
					for _, t := range todos {
						items = append(items, agentui.TodoItem{
							ID:       t.ID,
							Content:  t.Content,
							Status:   string(t.Status),
							Priority: t.Priority,
						})
					}
					return items
				})

				ca := agentRuntime.Current()
				if ca != nil {
					promptTokens := ca.CostTracker().LastPromptTokens()
					if promptTokens > 0 {
						// Compute context window usage percentage.
						ctxLen := int64(0)
						if ce := ca.Core().ContextEngine(); ce != nil {
							ctxLen = ce.ContextLength()
						}
						if ctxLen > 0 {
							pct := promptTokens * 100 / ctxLen
							if pct > 999 {
								pct = 999
							}
							ctxK := promptTokens / 1024
							totalK := ctxLen / 1024
							stickyFooter.SetContextUsage(
								fmt.Sprintf("ctx: %dk/%dk (%d%%)", ctxK, totalK, pct))
							stickyFooter.SetContextWarn(pct >= 80)
						} else {
							stickyFooter.SetContextUsage(fmt.Sprintf("ctx: %d tokens", promptTokens))
							stickyFooter.SetContextWarn(false)
						}
					}
				}

				runningCount := 0
				if bgManager != nil {
					for _, t := range bgManager.List() {
						if t.Status == runtimeapp.TaskRunning {
							runningCount++
						}
					}
				}
				stickyFooter.SetBgTaskCount(runningCount)

				app.Host().RequestRender()
			}
		}
	}, nil)

	memoryMonitor := runtimeapp.NewMemoryMonitor(func(gib float64) {
		app.PrintSystem(i18n.T("system.memory_high", "gb", fmt.Sprintf("%.2f", gib)))
	})
	go memoryMonitor.Run(app.Done())

	theme.SetOnSemanticThemeChange(func() {
		app.History().SetTheme(chat.DefaultChatHistoryTheme())
	})

	welcomeInfo := agentui.WelcomeInfo{
		Provider:   providerType,
		Model:      model,
		Mode:       string(mode),
		WorkingDir: workingDir,
	}
	if agentCore := covoAgent.Core(); agentCore != nil {
		welcomeInfo.ToolCount = len(agentCore.ToolNames())
	}
	welcomeInfo.SkillCount = len(covoAgent.Config().SkillConfig.AvailableSkills)
	app.History().Append(chat.ChatMessage{
		Role: chat.RoleAssistant,
		Text: agentui.BuildWelcomeMessage(welcomeInfo),
	})

	agentUIBinder.Bind(covoAgent.Core())
	agent.BindDiffPreviewer(covoAgent.Core(), workingDir, func(previews []diff.FileDiff) {
		if formatted := agentui.FormatDiffPreviews(previews); formatted != "" {
			app.PrintSystem(formatted)
		}
	})
	agentui.BindThinkingIndicator(app, covoAgent.Core())

	// changedFilesTracker was declared earlier in this function.

	app.Keybindings().Register("app.help", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+/"},
		Description: i18n.T("keybinding.help"),
	})
	app.Keybindings().Register("app.quit", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+q", "ctrl+d"},
		Description: i18n.T("keybinding.quit"),
	})
	app.Keybindings().Register("app.session", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+o"},
		Description: i18n.T("keybinding.session"),
	})
	app.Keybindings().Register("app.todo", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+t"},
		Description: i18n.T("keybinding.todo"),
	})
	app.Keybindings().Register("app.skills", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+k"},
		Description: i18n.T("keybinding.skills"),
	})
	app.Keybindings().Register("app.interrupt", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+c"},
		Description: i18n.T("keybinding.interrupt"),
	})
	app.Keybindings().Register("app.model-picker", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+p"},
		Description: i18n.T("keybinding.model_picker"),
	})
	app.Keybindings().Register("app.editor", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+e"},
		Description: i18n.T("keybinding.editor"),
	})
	app.Keybindings().Register("app.session-tree", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+y"},
		Description: i18n.T("keybinding.session_tree"),
	})
	app.Keybindings().Register("app.changed-files", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+g"},
		Description: "Show changed files tree",
	})

	openSessions := func() {
		mgr := agentRuntime.Current()
		if mgr == nil {
			return
		}
		infos, _ := mgr.SessionManager().ListSessions(context.Background())
		currentID := mgr.SessionManager().CurrentID()
		var items []component.SessionItem
		for _, info := range infos {
			items = append(items, component.SessionItem{
				ID:            info.ID,
				Name:          info.Name,
				Label:         info.Label,
				ParentSession: info.ParentSession,
				Preview:       info.Summary,
				CreatedAt:     info.CreatedAt.Format("01/02 15:04"),
				UpdatedAt:     info.UpdatedAt.Format("01/02 15:04"),
				MsgCount:      info.MessageCount,
				IsCurrent:     info.ID == currentID,
			})
		}
		selector := component.NewSessionSelector()
		selector.SetItems(items)
		var ov chat.OverlayRef
		closeOverlay := func() {
			loadUIBus().ClosePanel(ov)
		}
		refreshItems := func() {
			mgr := agentRuntime.Current()
			if mgr == nil {
				return
			}
			infos, _ := mgr.SessionManager().ListSessions(context.Background())
			currentID := mgr.SessionManager().CurrentID()
			var newItems []component.SessionItem
			for _, info := range infos {
				newItems = append(newItems, component.SessionItem{
					ID:            info.ID,
					Name:          info.Name,
					Label:         info.Label,
					ParentSession: info.ParentSession,
					Preview:       info.Summary,
					CreatedAt:     info.CreatedAt.Format("01/02 15:04"),
					UpdatedAt:     info.UpdatedAt.Format("01/02 15:04"),
					MsgCount:      info.MessageCount,
					IsCurrent:     info.ID == currentID,
				})
			}
			selector.SetItems(newItems)
			loadUIBus().Host().RequestRender()
		}
		selector.SetOnCancel(closeOverlay)
		selector.SetOnSelect(func(item component.SessionItem) {
			closeOverlay()
			mgr := agentRuntime.Current()
			if mgr == nil {
				return
			}
			if err := mgr.SessionManager().ResumeSession(context.Background(), item.ID); err != nil {
				loadUIBus().PrintError(fmt.Errorf("resume session: %w", err))
				return
			}
			snap, _ := mgr.SessionManager().LoadSession(context.Background(), item.ID)
			mgr.Core().State().Restore(snap)
			restoreChatHistory(appPtr, snap.Messages)
			loadUIBus().PrintSystem(i18n.T("system.session_resumed", "id", item.ID[:8]))
			loadUIBus().StatusBar().SetMode(i18n.T("app.session_title", "id", item.ID[:8]))
		})
		selector.SetOnDelete(func(item component.SessionItem) {
			mgr := agentRuntime.Current()
			if mgr == nil {
				return
			}
			if err := mgr.SessionManager().DeleteSession(context.Background(), item.ID); err != nil {
				loadUIBus().PrintError(fmt.Errorf("delete session: %w", err))
				return
			}
			loadUIBus().PrintSystem(i18n.T("system.session_deleted", "id", item.ID[:8]))
			refreshItems()
		})
		selector.SetOnRename(func(item component.SessionItem, newName string) {
			mgr := agentRuntime.Current()
			if mgr == nil {
				return
			}
			if err := mgr.SessionManager().RenameSession(context.Background(), item.ID, newName); err != nil {
				loadUIBus().PrintError(fmt.Errorf("rename session: %w", err))
				return
			}
			loadUIBus().PrintSystem(i18n.T("system.session_renamed", "id", item.ID[:8], "name", newName))
			refreshItems()
		})
		ov = loadUIBus().ShowPanel(selector, 80, 80)
	}

	openTodos := func() {
		panel := component.NewTodoPanel()
		readItems := func() []component.TodoItem {
			mgr := agentRuntime.Current()
			if mgr == nil {
				return nil
			}
			todos := mgr.TodoStore().Read()
			items := make([]component.TodoItem, 0, len(todos))
			for _, t := range todos {
				items = append(items, component.TodoItem{ID: t.ID, Content: t.Content, Status: string(t.Status), Priority: t.Priority})
			}
			return items
		}
		panel.SetDataProvider(readItems)
		panel.SetItems(readItems())
		panel.SetOnInvalidate(loadUIBus().Host().RequestRender)
		panel.SetOnToggle(func(item component.TodoItem) {
			mgr := agentRuntime.Current()
			if mgr == nil {
				return
			}
			store := mgr.TodoStore()
			current := store.Read()
			for i, t := range current {
				if t.ID == item.ID {
					if t.Status == toolsplanning.TodoCompleted {
						current[i].Status = toolsplanning.TodoPending
					} else {
						current[i].Status = toolsplanning.TodoCompleted
					}
					store.Write(current, false)
					loadUIBus().PrintSystem(fmt.Sprintf("TODO %s: %s", current[i].Status, t.Content[:min(40, len(t.Content))]))
					return
				}
			}
		})
		agentui.NewUIBus(appPtr).ShowPanel(panel, 80, 70)
	}

	openSessionTree := func() {
		mgr := agentRuntime.Current()
		if mgr == nil {
			return
		}
		infos, _ := mgr.SessionManager().ListSessions(context.Background())
		currentID := mgr.SessionManager().CurrentID()
		tree := agentpanels.NewSessionTree()
		tree.SetCurrentID(currentID)
		tree.SetItems(infos)
		var ov chat.OverlayRef
		closeOverlay := func() {
			loadUIBus().ClosePanel(ov)
		}
		tree.SetOnCancel(closeOverlay)
		tree.SetOnSelect(func(sessionID string) {
			closeOverlay()
			mgr := agentRuntime.Current()
			if mgr == nil {
				return
			}
			if err := mgr.SessionManager().ResumeSession(context.Background(), sessionID); err != nil {
				loadUIBus().PrintError(fmt.Errorf("resume session: %w", err))
				return
			}
			snap, _ := mgr.SessionManager().LoadSession(context.Background(), sessionID)
			mgr.Core().State().Restore(snap)
			restoreChatHistory(appPtr, snap.Messages)
			loadUIBus().PrintSystem(i18n.T("system.session_resumed", "id", sessionID[:8]))
			loadUIBus().StatusBar().SetMode(i18n.T("app.session_title", "id", sessionID[:8]))
			loadUIBus().Host().RequestRender()
		})
		ov = loadUIBus().ShowPanel(tree, 80, 80)
	}

	openSkillCenter := func() {
		mgr := agentRuntime.Current()
		if mgr == nil {
			return
		}
		inventory, err := mgr.SkillManager().List()
		if err != nil {
			loadUIBus().PrintError(fmt.Errorf("list skills: %w", err))
			return
		}
		items, skillPaths := buildSkillCenterData(mgr.Config().SkillConfig.AvailableSkills, inventory, mgr.SkillUsage())
		center := component.NewSkillCenter()
		center.SetItems(items)
		center.SetOnInvalidate(loadUIBus().Host().RequestRender)
		center.SetOnSelect(func(item component.SkillItem) {
			content, err := os.ReadFile(skillPaths[item.ID])
			if err != nil {
				if fallback, readErr := mgr.SkillManager().Read(item.ID); readErr == nil {
					content = []byte(fallback)
					err = nil
				}
			}
			if err == nil {
				loadUIBus().PrintSystem(fmt.Sprintf("── Skill: %s ──\n%s", item.Name, content))
			} else {
				loadUIBus().PrintError(fmt.Errorf("failed to read skill %s: %w", item.Name, err))
			}
		})
		agentui.NewUIBus(appPtr).ShowPanel(center, 80, 80)
	}

	openExternalEditorFn := func() {
		ed := loadUIBus().Editor()
		openExternalEditor(ed)
	}

	openKeyHelp := func() {
		help := component.NewKeyHelp(app.Keybindings())
		help.SetTitle("Keybindings — Esc to close")
		agentui.NewUIBus(appPtr).ShowPanel(help, 70, 70)
	}

	selectionAutoScroll := agentui.BindSelectionAutoScroll(app)
	if selectionDragMonitor := agentui.NewSelectionDragMonitor(selectionAutoScroll, app); selectionDragMonitor != nil {
		app.Host().AddChild(selectionDragMonitor)
	}

	app.Host().AddChild(agentui.NewHotkeyRouter(agentui.HotkeyRouterConfig{
		Stop:             app.Stop,
		PrintSystem:      app.PrintSystem,
		OpenSessions:     openSessions,
		OpenSessionTree:  openSessionTree,
		OpenTodos:        openTodos,
		OpenSkillCenter:  openSkillCenter,
		OpenKeyHelp:      openKeyHelp,
		OpenModelPicker:  openModelPicker,
		OpenEditor:       openExternalEditorFn,
		OpenChangedFiles: func() { openChangedFilesPanel(changedFilesTracker, workingDir) },
		Suggestions:      suggestionsMgr,
	}))

	if err := app.Start(); err != nil {
		log.Fatalf("start tui: %v", err)
	}
	<-app.Done()
}
