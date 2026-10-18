package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/covoyage/covo-agent/internal/agent/approval"
	"github.com/covoyage/covo-agent/internal/agent/compression"
	"github.com/covoyage/covo-agent/internal/cyber"
	"github.com/covoyage/covo-agent/internal/doomloop"
	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/covoyage/covo-agent/internal/extension"
	"github.com/covoyage/covo-agent/internal/goal"
	"github.com/covoyage/covo-agent/internal/hunk"
	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/lifecycle"
	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/lsp"
	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/covoyage/covo-agent/internal/repomap"
	"github.com/covoyage/covo-agent/internal/rollout"
	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covo-agent/internal/sandbox"
	"github.com/covoyage/covo-agent/internal/session"
	"github.com/covoyage/covo-agent/internal/snapshot"
	"github.com/covoyage/covo-agent/internal/telemetry"
	agenttools "github.com/covoyage/covo-agent/internal/tools"
	"github.com/covoyage/covo-agent/internal/tools/fileops"
	toolsmedia "github.com/covoyage/covo-agent/internal/tools/media"
	toolsstandingorders "github.com/covoyage/covo-agent/internal/tools/standingorders"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
	"github.com/covoyage/covo-agent/internal/toolset"
	"github.com/covoyage/covo-agent/internal/workspace"
	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/skill"
	"github.com/covoyage/covonaut/tools"
)

// stdinHandoffCallback is a default HandoffCallback that reads user input from stdin.
// It prints the message to stderr and blocks waiting for a line of input on stdin.
func stdinHandoffCallback(ctx context.Context, message string) (string, error) {
	fmt.Fprintf(os.Stderr, "\n=== HANDOFF: %s ===\n> ", message)
	return ReadStdinLine(ctx)
}

// ReadStdinLine reads a single line from stdin, blocking until input or context cancellation.
func ReadStdinLine(ctx context.Context) (string, error) {
	scanner := bufio.NewScanner(os.Stdin)
	done := make(chan string, 1)
	errCh := make(chan error, 1)
	safego.SafeGo(func() {
		if scanner.Scan() {
			done <- scanner.Text()
		} else {
			errCh <- scanner.Err()
		}
	}, nil)
	select {
	case line := <-done:
		fmt.Fprintln(os.Stderr)
		return line, nil
	case err := <-errCh:
		if err == nil {
			err = fmt.Errorf("input stream closed")
		}
		return "", fmt.Errorf("read user input: %w", err)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type CovoAgent struct {
	core                    *agentcore.Agent
	cfg                     agentcore.Config
	baseCfg                 CovoAgentConfig // original config for rebuild
	mode                    AgentMode
	homeDir                 string
	memory                  *evolution.MemorySystem
	skillMgr                *evolution.SkillManager
	bundleMgr               *evolution.BundleManager
	skillUsage              *evolution.SkillUsageTracker
	loadedSkills            []skill.Skill
	skillDiags              []skill.Diagnostic
	workshopStore           *evolution.WorkshopStore
	curator                 *evolution.Curator
	curatorCancel           context.CancelFunc
	personaMgr              *evolution.PersonaManager
	backgroundReviewer      *BackgroundReviewer
	promptBuilder           *PromptBuilder
	sessionMgr              *session.Manager
	subagentViews           *SubagentViewRegistry
	agentTools              *agenttools.Extension
	toolsetFilter           *toolset.ToolsetFilter
	providerName            string
	model                   string
	credentialPool          *CredentialPool
	rateLimitState          *RateLimitState
	toolGuardrail           *ToolGuardrail
	threatWarnings          []string // internal, never surfaced to user
	thinkScrubber           *StreamingThinkScrubber
	shellHooks              *ShellHookManager
	snapshotMgr             *SnapshotManager
	readTracker             *ReadTracker
	workDir                 string
	fsOps                   *swappableFS
	trajectory              *TrajectoryRecorder
	costTracker             *CostTracker
	approvalSystem          *approval.System
	pendingPattern          string // key/description of last pending approval, read by TUI
	titleGenerated          bool
	permissionChecker       func(ctx context.Context, toolName string, args []byte) bool
	preEditDiffChecker      func(ctx context.Context, toolName, filePath, diffText string) bool
	lspManager              *lsp.Manager
	sandbox                 sandbox.Sandbox
	gitWorktree             *GitWorktree
	failureTracker          *FailureTracker
	goalStore               *goal.Store         // persisted thread goals (survive compaction)
	goalAccounting          *goal.Accounting    // per-turn token/time tracking against goals
	goalSteering            *goal.Steering      // steering item injection
	goalHook                *goalAccountingHook // lifecycle hook wired to agent
	repoMap                 *repomap.RepoMap
	lastRepoMapInjected     string
	sessionSuspendFn        func(key, reason string, ttl time.Duration)
	readOnlyMgr             *ReadOnlyManager
	standingOrders          *toolsstandingorders.StandingOrdersStore
	dreamingEngine          *evolution.DreamingEngine
	dreamingCancel          context.CancelFunc
	workspaceOnly           bool // when true, file tools restricted to workspace directory
	extraWorkspaceRoots     []string
	auxClient               *AuxiliaryClient
	compressionSwitch       *compression.CompressionProviderSwitch
	doomloopDetector        *doomloop.Detector
	doomloopTurn            int         // current turn counter for doom loop tracking
	doomloopDetected        atomic.Bool // whether a doom loop was detected this turn
	doomloopBudgetExhausted bool        // when true, all tool calls are blocked
	hunkTracker             *hunk.Tracker
	rolloutRecorder         *rollout.Recorder
	rolloutStore            *rollout.Store
	surface                 PresentationSurface
	lease                   SessionLease
	commands                *CommandQueue

	// ExecutionPhase (Plan/Act) — orthogonal to AgentMode. In Plan mode,
	// mutating tools are blocked by planModeGateBeforeHook and filtered
	// from the LLM's tool list by the toolset filter.
	executionPhase ExecutionPhase
	phaseMu        sync.RWMutex
}

type CovoAgentConfig struct {
	Mode             AgentMode
	SubagentRole     string
	Provider         agentcore.Provider
	ProviderName     string
	Model            string
	WorkingDir       string
	HomeDir          string
	Platform         string
	Logger           *slog.Logger
	CuratorCfg       evolution.CuratorConfig
	CredentialPool   *CredentialPool
	MaxIterations    int64
	SubMaxIterations int64
	LifecycleHooks   []agentcore.LifecycleHook
	GitWorktree      *GitWorktree
	SpawnRunner      toolssubagent.SpawnRunner
	// SpawnNotifyFunc is invoked when a background subagent spawned by this
	// agent completes (success or failure). Interactive sessions use it to
	// wake the parent conversation (steer mid-run / start a turn); nil keeps
	// the inbox-only behavior.
	SpawnNotifyFunc     toolssubagent.SpawnCompleteFunc
	MemoryProviderName  string
	ProviderMiddlewares []ProviderMiddleware
	ExecutionMode       string
	Concurrency         int64
	ComputerUse         *bool
	ContextEngine       string
	MCPServers          map[string]agenttools.MCPConfig
	ApprovalCfg         *approval.Config
	ToolProfile         string // "minimal", "coding", "messaging", "full"
	ThinkingCfg         *agentcore.ThinkingConfig
	ShowReasoning       bool  // display thinking blocks
	ModelContextLength  int64 // model's context window size

	// MaxTurns bounds the agent's per-run turn loop. 0 (default) means
	// effectively unlimited, matching the main loop; subagent spawns pass an
	// explicit budget only when the model sets max_turns on the tool call.
	MaxTurns int
	// SubagentViews shares a sub-agent view registry across generations:
	// when set (parents pass their own), grandchildren register into the
	// same registry so the TUI dashboard shows the whole tree.
	SubagentViews *SubagentViewRegistry

	// FrequencyPenalty / PresencePenalty reduce the model's tendency to
	// stream the same text over and over (degeneration/repetition loops).
	// 0 = provider default (unset). Only honored by OpenAI-compatible
	// providers; ignored elsewhere. Opt-in, never defaulted automatically
	// (some reasoning models reject non-zero penalties).
	FrequencyPenalty float64
	PresencePenalty  float64
	ResponseFormat   *agentcore.ResponseFormat

	ThinkingMode             string // "collapsed" / "truncated" / "full"
	SkillURLs                []string
	SkillExtraDirs           []string                 // extra skill roots from config, lowest priority
	SkillDisabled            []string                 // skill names excluded from the model catalog
	SkillConfigGet           func() map[string]string // current skills.config values
	SkillConfigSet           func(key, value string) error
	LeanTools                bool                     // hide long-tail schemas; keep them via tool_search
	SystemPrompt             string                   // replaces SOUL.md in context tier
	AppendSystemPrompt       string                   // appended to context tier
	WorkspaceOnly            bool                     // when true, file tools restricted to workspace directory
	ExtraWorkspaceRoots      []string                 // additional directories treated as workspace roots
	ToolsetOverride          []string                 // restricted toolsets for child/sub agents (replaces COVO_TOOLSETS env var)
	ToolNameFilter           func(string) bool        // optional per-tool-name predicate (headless --tools/--disallowed-tools); nil = no filtering
	Auxiliary                *AuxiliaryConfig         // per-task auxiliary model overrides
	AuxiliaryProviderBuilder AuxiliaryProviderBuilder // builds providers from auxiliary config

	// SessionSuspendFunc is called when a session should be suspended
	// (e.g. after a rate limit). The gateway provides this callback.
	SessionSuspendFunc func(key, reason string, ttl time.Duration)

	// ParentRolloutID links this agent's rollout to the rollout of the agent
	// that spawned it. Only meaningful for subagents when COVO_ROLLOUT=true.
	ParentRolloutID string

	// Surface selects prompt/tool presentation for this process (terminal,
	// acp, gateway, exec). Empty defaults to terminal.
	Surface PresentationSurface
	// Remote, when set, routes file and bash tools to another host.
	Remote *workspace.RemoteSpec
}

type skillConfigStore struct {
	get func() map[string]string
	set func(key, value string) error
}

func (s skillConfigStore) Get() map[string]string {
	if s.get == nil {
		return nil
	}
	return s.get()
}

func (s skillConfigStore) Set(key, value string) error {
	if s.set == nil {
		return fmt.Errorf("skill config store is not available")
	}
	return s.set(key, value)
}

// repetitionRecoveryConfig builds the localized soft repetition-loop
// recovery ladder (see agentcore.RepetitionRecoveryConfig): mild nudge on
// the first detection, a stronger "replan" nudge on the next, then runLoop
// gives up with a terminal error. Shared across all three detectors
// (mid-stream degeneration, cross-turn repeated text, cross-turn repeated
// tool calls) — kind is accepted for potential future per-kind wording but
// currently maps to the same two-tier message, which reads naturally for
// all three cases.
func repetitionRecoveryConfig() *agentcore.RepetitionRecoveryConfig {
	return &agentcore.RepetitionRecoveryConfig{
		MaxAttempts: 2,
		Prompt: func(kind agentcore.RepetitionKind, attempt int64) string {
			if attempt > 0 {
				return i18n.T("system.repetition_recovery_strong")
			}
			return i18n.T("system.repetition_recovery_mild")
		},
	}
}

func webSearchToolConfigFromAgent(providerName string) *tools.WebSearchToolConfig {
	cfg := &tools.WebSearchToolConfig{
		Provider:         strings.TrimSpace(os.Getenv("WEB_SEARCH_PROVIDER")),
		APIURL:           strings.TrimSpace(os.Getenv("WEB_SEARCH_API_URL")),
		APIKey:           strings.TrimSpace(os.Getenv("WEB_SEARCH_API_KEY")),
		ChatProviderName: providerName,
	}
	if strings.EqualFold(providerName, "gemini") {
		cfg.GeminiAPIKey = firstEnv(
			"GEMINI_API_KEY",
			"GOOGLE_API_KEY",
			"API_KEY",
		)
	}
	return cfg
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

func browserToolConfigFromEnv() *tools.BrowserToolConfig {
	cloudProvider := browserCloudProviderFromEnv()
	cfg := &tools.BrowserToolConfig{
		Headless:            browserHeadlessFromEnv(),
		AllowPrivate:        envBool("BROWSER_ALLOW_PRIVATE_URLS", false),
		CommandTimeout:      envDurationSeconds("BROWSER_COMMAND_TIMEOUT", 0),
		CDPURL:              os.Getenv("BROWSER_CDP_URL"),
		CamofoxURL:          os.Getenv("CAMOFOX_URL"),
		CloudProvider:       cloudProvider,
		Engine:              os.Getenv("AGENT_BROWSER_ENGINE"),
		AutoLocalForPrivate: envBool("BROWSER_AUTO_LOCAL_FOR_PRIVATE_URLS", cloudProvider != ""),
		RecordSessions:      envBool("BROWSER_RECORD_SESSIONS", false),
		RecordingDir:        os.Getenv("BROWSER_RECORDING_DIR"),
		DialogTimeout:       envDurationSeconds("BROWSER_DIALOG_TIMEOUT", 0),
		VisionModel:         os.Getenv("BROWSER_VISION_MODEL"),
		UserAgent:           os.Getenv("BROWSER_USER_AGENT"),
		AcceptLanguage:      os.Getenv("BROWSER_ACCEPT_LANGUAGE"),
		ProxyURL:            os.Getenv("BROWSER_PROXY_URL"),
		ViewportWidth:       envInt("BROWSER_VIEWPORT_WIDTH", 0),
		ViewportHeight:      envInt("BROWSER_VIEWPORT_HEIGHT", 0),
	}
	if policy := os.Getenv("BROWSER_DIALOG_POLICY"); policy != "" {
		cfg.DialogPolicy = tools.DialogPolicy(policy)
	}
	return cfg
}

func browserCloudProviderFromEnv() string {
	if provider := os.Getenv("BROWSER_CLOUD_PROVIDER"); provider != "" {
		return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(provider)), "-", "_")
	}
	if os.Getenv("BROWSER_USE_API_KEY") != "" {
		return "browser_use"
	}
	if os.Getenv("BROWSERBASE_API_KEY") != "" && os.Getenv("BROWSERBASE_PROJECT_ID") != "" {
		return "browserbase"
	}
	if os.Getenv("FIRECRAWL_API_KEY") != "" {
		return "firecrawl"
	}
	return ""
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

// browserHeadlessFromEnv matches Hermes: local browser is headless unless the
// user opts into a visible automation window. AGENT_BROWSER_HEADED=1 / true
// (or BROWSER_HEADLESS=false) launches a headed Chromium the agent still
// drives via CDP — not the OS default browser.
func browserHeadlessFromEnv() bool {
	headed := strings.TrimSpace(os.Getenv("AGENT_BROWSER_HEADED"))
	if headed != "" {
		parsed, err := strconv.ParseBool(headed)
		if err == nil {
			return !parsed
		}
	}
	return envBool("BROWSER_HEADLESS", true)
}

func envDurationSeconds(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

// terminalToolConfigFromEnv builds the persistent terminal config. Set
// TERMINAL_TOOLS=off to disable the family entirely; TERMINAL_SHELL
// overrides the shell used for new sessions.
// ResolveExecMode maps an execution.mode setting to an executor mode.
// Parallel is the default and the widest setting: every tool call joins the
// bounded rolling pool unless the tool explicitly declared
// ConcurrencySafe=false, in which case it runs alone as a serial barrier.
// "mixed" selects strict declarative scheduling (undeclared tools fail
// closed to serial barriers) and "serial" downgrades to fully serial
// execution. A config value takes precedence over the EXECUTION_MODE
// environment variable, which honors "serial" and "mixed" when no config
// value is set.
func ResolveExecMode(cfgMode string) agentcore.ExecutionMode {
	switch cfgMode {
	case "serial":
		return agentcore.ModeSerial
	case "mixed":
		return agentcore.ModeMixed
	}
	if cfgMode == "" {
		switch os.Getenv("EXECUTION_MODE") {
		case "serial":
			return agentcore.ModeSerial
		case "mixed":
			return agentcore.ModeMixed
		}
	}
	return agentcore.ModeParallel
}

func terminalToolConfigFromEnv() *tools.TerminalToolConfig {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TERMINAL_TOOLS"))) {
	case "off", "false", "0", "no":
		return nil
	}
	shell := strings.TrimSpace(os.Getenv("TERMINAL_SHELL"))
	if shell == "" {
		return &tools.TerminalToolConfig{}
	}
	return &tools.TerminalToolConfig{Shell: shell}
}

// filterInjectableParentMessages returns the subset of parent conversation
// messages that are safe to inject into a spawned child's initial state in
// "full" context mode. Only standard user/assistant messages are kept:
//   - tool results (role=tool) carry a ToolCallID that must follow a matching
//     assistant tool_calls message; the child has no such chain, so injecting
//     them would confuse providers or trigger errors.
//   - system messages are dropped because the child already has its own system
//     prompt; duplicating it skews behavior and wastes tokens.
func filterInjectableParentMessages(msgs []agentcore.Message) []agentcore.Message {
	filtered := make([]agentcore.Message, 0, len(msgs))
	for _, msg := range msgs {
		if msg.Role == agentcore.RoleTool || msg.Role == agentcore.RoleSystem {
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
}

func restrictChildToolsets(requested, allowed []string) ([]string, error) {
	if len(allowed) == 0 {
		return requested, nil
	}
	if len(requested) == 0 {
		return append([]string(nil), allowed...), nil
	}
	permitted := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		permitted[name] = true
	}
	var filtered []string
	for _, name := range requested {
		if permitted[name] {
			filtered = append(filtered, name)
		}
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("child toolsets are outside the parent's configured capabilities")
	}
	return filtered, nil
}

func NewCovoAgent(cfg CovoAgentConfig) (*CovoAgent, error) {
	// Auto-create GitWorktree if COVO_WORKTREE=true and not explicitly provided
	if cfg.GitWorktree == nil && os.Getenv("COVO_WORKTREE") == "true" && cfg.WorkingDir != "" {
		gw := NewGitWorktree(cfg.WorkingDir)
		if gw.Enabled() {
			cfg.GitWorktree = gw
		}
	}
	if cfg.GitWorktree != nil && cfg.GitWorktree.Enabled() {
		if wtPath, err := cfg.GitWorktree.Ensure(); err == nil && wtPath != cfg.WorkingDir {
			cfg.WorkingDir = wtPath
		}
	}

	var memory *evolution.MemorySystem
	if cfg.MemoryProviderName != "" {
		if factory, ok := evolution.GetMemoryProvider(cfg.MemoryProviderName); ok {
			memCfg := evolution.MemoryProviderConfig{HomeDir: cfg.HomeDir}
			p, err := factory(memCfg)
			if err != nil {
				return nil, fmt.Errorf("init memory provider %q: %w", cfg.MemoryProviderName, err)
			}
			memory = evolution.NewMemorySystemWithProvider(p)
		} else {
			return nil, fmt.Errorf("unknown memory provider %q (available: %v)", cfg.MemoryProviderName, evolution.MemoryProviderNames())
		}
	} else {
		memory = evolution.NewMemorySystem(cfg.HomeDir)
	}
	if err := memory.Init(); err != nil {
		return nil, fmt.Errorf("init memory: %w", err)
	}

	skillsDir := cfg.HomeDir + "/skills"
	skillUsage := evolution.NewSkillUsageTracker(skillsDir)
	_ = skillUsage.Load()

	skillMgr := evolution.NewSkillManager(skillsDir, skillUsage)
	if err := skillMgr.Init(); err != nil {
		return nil, fmt.Errorf("init skill manager: %w", err)
	}

	bundleMgr := evolution.NewBundleManager(cfg.HomeDir)
	_ = bundleMgr.Init()

	promptBuilder := NewPromptBuilder(memory, cfg.WorkingDir)

	// Inject system prompt overrides
	if cfg.SystemPrompt != "" {
		promptBuilder.SetSystemPrompt(cfg.SystemPrompt)
	}
	appendPrompt := cfg.AppendSystemPrompt
	if extra := cfg.Surface.PromptDirective(); extra != "" {
		appendPrompt = strings.TrimSpace(appendPrompt + "\n\n" + extra)
	}
	if appendPrompt != "" {
		promptBuilder.SetAppendSystemPrompt(appendPrompt)
	}

	// Runtime metadata for system prompt
	if cfg.Model != "" {
		promptBuilder.SetModelName(cfg.Model)
	}

	// Workspace-only mode for file operations
	workspaceOnly := cfg.WorkspaceOnly
	extraWorkspaceRoots := append([]string(nil), cfg.ExtraWorkspaceRoots...)
	promptBuilder.SetFSWorkspaceOnly(workspaceOnly)

	// Repository map: code structure awareness for the model
	var repoMap *repomap.RepoMap
	if cfg.WorkingDir != "" {
		repoMap = repomap.New(cfg.WorkingDir)
	}

	// Read-only file protection: patterns from built-in defaults,
	// COVO_READ_ONLY env var, and .covoignore in project root.
	readOnlyMgr := NewReadOnlyManager(cfg.WorkingDir)

	// Initialize persona manager for Honcho-style user modeling
	personaMgr, _ := evolution.NewPersonaManager(cfg.HomeDir)
	promptBuilder.SetPersonaManager(personaMgr)

	// Standing orders: persistent behavioral instructions injected every session.
	standingOrders := toolsstandingorders.NewStandingOrdersStore(cfg.HomeDir)
	promptBuilder.SetStandingOrdersStore(standingOrders)

	// Skill Workshop: proposal-based skill management
	workshopStore := evolution.NewWorkshopStore(cfg.HomeDir)
	if err := workshopStore.Init(); err != nil {
		return nil, fmt.Errorf("initialize workshop: %w", err)
	}
	if err := workshopStore.BindSkillManager(skillMgr); err != nil {
		return nil, fmt.Errorf("recover workshop: %w", err)
	}

	sessionMgr, err := session.NewManager(cfg.HomeDir)
	if err != nil {
		return nil, fmt.Errorf("init session manager: %w", err)
	}

	// Initialize goal store (separate table, survives ALL compaction).
	// Shares the session store's SQLite connection.
	goalStore, err := goal.NewStore(sessionMgr.DB().DB())
	if err != nil {
		return nil, fmt.Errorf("init goal store: %w", err)
	}

	// Initialize rollout store (opt-in via COVO_ROLLOUT=true).
	var rolloutSt *rollout.Store
	if envBool("COVO_ROLLOUT", false) {
		rolloutSt, err = rollout.NewStore(cfg.HomeDir)
		if err != nil {
			// Non-fatal: rollout tracing degrades gracefully without persistence.
			if cfg.Logger != nil {
				cfg.Logger.Warn("rollout store init failed, tracing will be in-memory only", "error", err)
			}
			rolloutSt = nil
		}
	}

	sandboxCfg := sandbox.ConfigFromEnv()
	if cfg.Mode.IsCyberFamily() && sandboxCfg.Type == sandbox.TypeDocker {
		if sandboxCfg.PersistentID == "" {
			sandboxCfg.PersistentID = "cyber:" + cfg.WorkingDir
		}
		sandboxCfg.WorkDir = "/workspace"
		sandboxCfg.Network = true
		sandboxCfg.Mounts = append(sandboxCfg.Mounts, sandbox.Mount{Source: cfg.WorkingDir, Target: "/workspace", ReadOnly: true})
	}
	sb, sbErr := sandbox.New(sandboxCfg)
	if sbErr != nil {
		sb = nil
	}

	doomloopDetector := doomloop.New(doomloop.DefaultConfig())
	hunkTracker := hunk.NewTracker(cfg.WorkingDir)

	// Late-bound to this agent's rollout recorder ID after the provider chain
	// is built. Spawned children capture this to link their rollouts to the
	// parent's (only meaningful when COVO_ROLLOUT=true).
	var parentRolloutID string
	var parentRecorder *rollout.Recorder

	// Subagent view registry: powers the TUI's live subagent transcript views.
	// Parents share theirs with children so the dashboard shows all generations.
	subagentViews := cfg.SubagentViews
	if subagentViews == nil {
		subagentViews = NewSubagentViewRegistry()
	}

	// Default SpawnRunner: creates a child agentcore.Agent with restricted toolsets
	var ca *CovoAgent
	spawnRunner := cfg.SpawnRunner
	if spawnRunner == nil {
		provider := cfg.Provider
		model := cfg.Model
		homeDir := cfg.HomeDir
		workingDir := cfg.WorkingDir
		providerName := cfg.ProviderName
		execMode := cfg.ExecutionMode
		concurrency := cfg.Concurrency
		computerUse := cfg.ComputerUse
		spawnRunner = func(ctx context.Context, task string, toolsetNames []string, maxTurns int) (string, error) {
			// Allow provider/model override from context (set by sessions_spawn tool)
			childProvider := provider
			if override := toolssubagent.SubagentProviderFromContext(ctx); override != "" {
				childProvider = nil // will be resolved by NewCovoAgent from ProviderName
				providerName = override
			}
			childModel := model
			if override := toolssubagent.SubagentModelFromContext(ctx); override != "" {
				childModel = override
			}

			// Inherit parent credential pool
			childCredPool := cfg.CredentialPool

			childWorkingDir := workingDir
			var childWorktree *GitWorktree
			if toolssubagent.SubagentIsolationFromContext(ctx) == "worktree" {
				base := workingDir
				if cfg.GitWorktree != nil && cfg.GitWorktree.BaseDir() != "" {
					base = cfg.GitWorktree.BaseDir()
				}
				if gw := NewIsolatedGitWorktree(base); gw.Enabled() {
					childWorktree = gw
					childWorkingDir = base
				}
			}

			childMode := cfg.Mode
			if !childMode.Valid() {
				childMode = ModeGeneral
			}
			if cyberSpawnRequested(ctx, toolsetNames) {
				childMode = ModeCyber
			}
			childRole, err := resolveCyberChildRole(childMode, toolssubagent.SubagentRoleFromContext(ctx))
			if err != nil {
				return "", err
			}
			childPlatform := "minimal"
			if childMode == ModeCyber {
				childPlatform = "cli"
				if len(toolsetNames) > 0 {
					toolsetNames = toolset.EnsureCyberToolset(toolsetNames)
				}
			}
			childCfg := CovoAgentConfig{
				SkillURLs:           append([]string(nil), cfg.SkillURLs...),
				Mode:                childMode,
				SubagentRole:        childRole,
				Provider:            childProvider,
				ProviderName:        providerName,
				Model:               childModel,
				WorkingDir:          childWorkingDir,
				HomeDir:             homeDir,
				Platform:            childPlatform,
				LeanTools:           cfg.LeanTools || childMode.LeanDefault(),
				ExecutionMode:       execMode,
				Concurrency:         concurrency,
				ComputerUse:         computerUse,
				CredentialPool:      childCredPool,
				ToolsetOverride:     toolsetNames,
				WorkspaceOnly:       cfg.WorkspaceOnly,
				ExtraWorkspaceRoots: append([]string(nil), cfg.ExtraWorkspaceRoots...),
				ToolNameFilter:      cfg.ToolNameFilter,
				ToolProfile:         cfg.ToolProfile,
				ApprovalCfg:         cfg.ApprovalCfg,
				SkillDisabled:       append([]string(nil), cfg.SkillDisabled...),
				SkillExtraDirs:      append([]string(nil), cfg.SkillExtraDirs...),
				GitWorktree:         childWorktree,
				// 0 (max_turns omitted) resolves to unbounded, same as the
				// main loop; an explicit budget is honored verbatim.
				MaxTurns:      maxTurns,
				SubagentViews: subagentViews,
				// Inherit auxiliary config from parent so spawned children
				// use the same auxiliary models for title/review/judge tasks.
				Auxiliary:                cfg.Auxiliary,
				AuxiliaryProviderBuilder: cfg.AuxiliaryProviderBuilder,
				SkillConfigGet:           cfg.SkillConfigGet,
				SkillConfigSet:           cfg.SkillConfigSet,
				// Link the child's rollout to the parent agent's rollout when
				// rollout tracing is enabled.
				ParentRolloutID: parentRolloutID,
			}
			if cfg.MCPServers != nil {
				childCfg.MCPServers = make(map[string]agenttools.MCPConfig, len(cfg.MCPServers))
				for name, server := range cfg.MCPServers {
					server.Args = append([]string(nil), server.Args...)
					server.Env = append([]string(nil), server.Env...)
					childCfg.MCPServers[name] = server
				}
			}
			childCfg.ToolsetOverride, err = restrictChildToolsets(toolsetNames, cfg.ToolsetOverride)
			if err != nil {
				return "", err
			}
			child, err := NewCovoAgent(childCfg)
			if err != nil {
				return "", fmt.Errorf("spawn: create child agent: %w", err)
			}
			defer child.Close()
			cyber.RecordRetestSession(ctx, child.SessionManager().CurrentID())
			child.SetExecutionPhase(ca.ExecutionPhase())

			// Record the subagent lifecycle into the parent's rollout so the
			// parent trace links to the child at per-turn granularity.
			var subagentResult string
			if parentRecorder != nil {
				childSession := child.SessionManager().CurrentID()
				childRolloutID := ""
				if cr := child.RolloutRecorder(); cr != nil {
					childRolloutID = cr.ID()
				}
				parentRecorder.RecordSubagentEvent(rollout.SubagentEdgeSpawn, childRolloutID, childSession, task)
			}
			defer func() {
				if parentRecorder != nil {
					childRolloutID := ""
					if cr := child.RolloutRecorder(); cr != nil {
						childRolloutID = cr.ID()
					}
					kind := rollout.SubagentEdgeResult
					msg := subagentResult
					if err != nil {
						kind = rollout.SubagentEdgeTimeout
						msg = err.Error()
					}
					parentRecorder.RecordSubagentEvent(kind, childRolloutID, child.SessionManager().CurrentID(), msg)
				}
			}()

			// Subagents run without a user present to approve dangerous commands.
			// Mark as non-interactive so the manual-approval fallback auto-denies
			// instead of hanging forever.
			if child.ApprovalSystem() != nil {
				child.ApprovalSystem().SetNonInteractive(true)
			}

			childCtx, cancel := context.WithCancel(ctx)
			defer cancel()

			// "full" context mode: inject the parent's conversation messages
			// into the child's state so it inherits the full parent context.
			// Only standard user/assistant messages are injected: tool results
			// require a matching assistant tool_calls chain (which the child
			// does not have) and system messages would duplicate the child's
			// own system prompt.
			if parentMsgs := toolssubagent.ParentMessagesFromContext(ctx); len(parentMsgs) > 0 {
				injectable := filterInjectableParentMessages(parentMsgs)
				if toolssubagent.IsStructuredFork(ctx) {
					injectable = parentMsgs
				}
				// "turns:N" context mode: keep only the most recent N
				// injectable parent messages.
				if n := toolssubagent.ParentMessageTurnsFromContext(ctx); n > 0 && len(injectable) > n {
					injectable = injectable[len(injectable)-n:]
				}
				for _, msg := range injectable {
					child.Core().State().AddMessage(msg)
				}
			}

			_ = child.Core().Config()
			viewID := toolssubagent.SubagentViewRefFromContext(childCtx)
			subagentViews.RegisterWithID(viewID, task, toolssubagent.SubagentDepthFromContext(ctx)+1,
				func() []agentcore.Message { return child.Core().State().Messages() })
			// Guarantee the view status flips even if the child run panics —
			// a leaked "running" entry would keep the footer strip alive
			// forever.
			defer func() {
				if r := recover(); r != nil {
					subagentViews.MarkFinished(viewID, "failed")
					panic(r)
				}
			}()
			childCtx = toolssubagent.WithSubagentRole(childCtx, childRole)
			subagentResult, err = child.core.Run(childCtx, task)
			if err != nil {
				// A manual interrupt cancels the child context — report the
				// view status as interrupted, not failed.
				if errors.Is(err, context.Canceled) {
					subagentViews.MarkFinished(viewID, "interrupted")
				} else {
					subagentViews.MarkFinished(viewID, "failed")
				}
				return "", fmt.Errorf("spawn: child run: %w", err)
			}
			subagentViews.MarkFinished(viewID, "completed")
			return subagentResult, nil
		}
	}

	cyberTransport := NewSafeClient().Transport
	if cfg.Mode.IsCyberFamily() {
		cyberTransport, err = NewCyberHTTPTransport()
		if err != nil {
			return nil, err
		}
	}
	toolsExt := agenttools.NewExtension(agenttools.ExtensionConfig{
		SessionsDir:       filepath.Join(cfg.HomeDir, "sessions"),
		HomeDir:           cfg.HomeDir,
		HandoffCallback:   stdinHandoffCallback,
		Sandbox:           sb,
		SpawnRunner:       spawnRunner,
		SpawnNotifyFunc:   cfg.SpawnNotifyFunc,
		ToolProfile:       cfg.ToolProfile,
		CyberMode:         cfg.Mode.IsCyberFamily(),
		SubagentRole:      cfg.SubagentRole,
		WorkDir:           cfg.WorkingDir,
		ProviderName:      cfg.ProviderName,
		WebFetchTransport: cyberTransport,
	})

	// Wire the standing orders store into the extension (same instance used
	// by PromptBuilder for prompt injection).
	toolsExt.SetStandingOrdersStore(standingOrders)

	// Wire the Skill Workshop store and skill manager into the extension.
	toolsExt.SetWorkshopStore(workshopStore)
	toolsExt.SetSkillManager(skillMgr)

	ca = &CovoAgent{
		baseCfg:          cfg,
		mode:             cfg.Mode,
		homeDir:          cfg.HomeDir,
		subagentViews:    subagentViews,
		memory:           memory,
		skillMgr:         skillMgr,
		bundleMgr:        bundleMgr,
		skillUsage:       skillUsage,
		workshopStore:    workshopStore,
		personaMgr:       personaMgr,
		promptBuilder:    promptBuilder,
		sessionMgr:       sessionMgr,
		agentTools:       toolsExt,
		providerName:     cfg.ProviderName,
		model:            cfg.Model,
		sandbox:          sb,
		gitWorktree:      cfg.GitWorktree,
		goalStore:        goalStore,
		goalAccounting:   goal.NewAccounting(goalStore),
		goalSteering:     goal.NewSteering(),
		repoMap:          repoMap,
		readOnlyMgr:      readOnlyMgr,
		standingOrders:   standingOrders,
		sessionSuspendFn: cfg.SessionSuspendFunc,
		doomloopDetector: doomloopDetector,
		hunkTracker:      hunkTracker,
		rolloutStore:     rolloutSt,
		surface:          NormalizeSurface(string(cfg.Surface)),
		commands:         NewCommandQueue(),
	}

	// Wire the SQLite goal store to the extension so LLM goal tools
	// (create_goal, get_goal, update_goal) use the same store as the
	// stop gate judge.
	toolsExt.SetGoalStore(goalStore, func() string {
		return ca.SessionManager().CurrentID()
	})

	// Wire parent messages provider for sessions_spawn "state"/"full" context
	// modes. Late-bound: ca.core is set after this point, but the closure is
	// only invoked at spawn time (well after construction completes).
	toolsExt.SetParentMessages(func() []agentcore.Message {
		if ca.core == nil {
			return nil
		}
		return ca.core.State().Messages()
	})

	// Wire the Plan→Act phase transitioner. When exit_plan_mode is invoked
	// and the user approves, this transitions the agent from Plan to Act,
	// unblocking mutating tools.
	toolsExt.SetPhaseTransitioner(func() {
		ca.SetExecutionPhase(PhaseAct)
	})

	// Wire hunk tracker to fileops hooks so file changes are attributed.
	// Use the agent pointer as a unique ID so only the active agent's tracker receives notifications.
	fileops.SetAfterWriteHook(fmt.Sprintf("agent-%p", ca), func(path, toolName, toolCallID string) {
		if ca.hunkTracker != nil {
			ca.hunkTracker.RecordAgentEdit(path, toolName, toolCallID)
		}
	})

	// Wire Plan mode directive injection into the system prompt.
	promptBuilder.SetPlanModeChecker(func() bool { return ca.IsPlanMode() })

	if cfg.CredentialPool != nil {
		ca.credentialPool = cfg.CredentialPool
	} else {
		ca.credentialPool = LoadCredentialPoolFromEnvVars(cfg.ProviderName)
	}

	ca.rateLimitState = NewRateLimitState()
	ca.toolGuardrail = NewToolGuardrail()
	ca.approvalSystem, err = initApprovalSystem(cfg)
	if err != nil {
		return nil, fmt.Errorf("initialize approval policy: %w", err)
	}
	ca.thinkScrubber = NewStreamingThinkScrubber()
	ca.shellHooks = NewShellHookManager(cfg.HomeDir, envBool("COVO_ACCEPT_HOOKS", false))
	ca.shellHooks.LoadProjectHooksFile(cfg.WorkingDir)
	var claudeHookPaths []string
	if !envBool("COVO_CLAUDE_HOOKS_DISABLED", false) {
		claudeHookPaths = claudeHooksPaths(cfg.WorkingDir, cfg.HomeDir)
		if n := ca.shellHooks.LoadClaudeHooks(cfg.WorkingDir, cfg.HomeDir); n > 0 {
			if cfg.Logger != nil {
				cfg.Logger.Info("loaded Claude Code hooks", "count", n)
			}
		}
	}
	var codexHookPaths []string
	if !envBool("COVO_CODEX_HOOKS_DISABLED", false) {
		codexHookPaths = codexHooksPaths(cfg.WorkingDir, cfg.HomeDir)
		if n := ca.shellHooks.LoadCodexHooks(cfg.WorkingDir, cfg.HomeDir); n > 0 {
			if cfg.Logger != nil {
				cfg.Logger.Info("loaded Codex hooks", "count", n)
			}
		}
	}
	reloadPaths := append(append([]string{}, claudeHookPaths...), codexHookPaths...)
	ca.shellHooks.StartHotReload(cfg.WorkingDir, reloadPaths...)
	// File-level snapshot service (isolated git repo, content-addressed).
	// Enables /undo, revert-to-snapshot, and /rewind (chat + workspace rollback).
	// Non-fatal if git unavailable.
	if snapSvc, err := snapshot.NewService(cfg.WorkingDir, cfg.HomeDir); err == nil {
		ca.snapshotMgr = NewSnapshotManager(snapSvc)
	} else {
		ca.snapshotMgr = NewSnapshotManager(nil)
	}
	ca.snapshotMgr.SetStoreDir(cfg.HomeDir)
	// Capture a baseline snapshot so there's always an initial state to
	// rewind to, even before any file-mutating tool has run. The full
	// staging walk runs in the background so startup stays fast on large
	// work trees; the first file-mutating tool waits for it if needed.
	ca.snapshotMgr.TrackBaselineAsync()
	ca.readTracker = NewReadTracker()
	ca.workDir = cfg.WorkingDir
	ca.workspaceOnly = cfg.WorkspaceOnly
	ca.extraWorkspaceRoots = extraWorkspaceRoots
	ca.trajectory = NewTrajectoryRecorder(cfg.Model, "", filepath.Join(cfg.HomeDir, "trajectories"))
	ca.lspManager = lsp.NewManager(lsp.ManagerConfig{
		Enabled:     envBool("LSP_ENABLED", true),
		IdleTimeout: 10 * time.Minute,
	})

	// Wire @problems context reference to LSP diagnostics.
	if ca.lspManager != nil {
		lspMgr := ca.lspManager
		SetProblemsProvider(func(cwd string) string {
			return lspMgr.CollectAllDiagnostics(cwd)
		})
	}

	ca.costTracker = NewCostTracker(cfg.ProviderName, cfg.Model)
	ca.failureTracker = NewFailureTracker(cfg.Logger)

	if cfg.CuratorCfg.Enabled {
		curatorLogger := cfg.Logger
		if curatorLogger == nil {
			curatorLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))
		}
		ca.curator = evolution.NewCurator(skillsDir, skillUsage, cfg.CuratorCfg, curatorLogger)
		// Link curator to memory for nudge operations
		ca.curator.SetMemory(memory)
		ca.curator.SetSkillManager(skillMgr)
		ca.curator.SetWorkshopStore(workshopStore)

		// Enable extraction and nudges if configured
		if cfg.CuratorCfg.EnableSkillExtraction {
			ca.curator.SetSkillManager(skillMgr) // re-ensure extractor is created
		}
		if cfg.CuratorCfg.EnableNudge {
			ca.curator.SetNudgeSystem(cfg.CuratorCfg.NudgeConfig, cfg.HomeDir)
		}

		ctx, cancel := context.WithCancel(context.Background())
		ca.curatorCancel = cancel
		go ca.curator.Start(ctx)
	}

	// Initialize dreaming engine for timed memory consolidation
	if ca.memory != nil {
		dreamCfg := evolution.DefaultDreamingConfig()
		if v := os.Getenv("COVO_DREAMING_INTERVAL"); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				dreamCfg.Interval = d
			}
		}
		if os.Getenv("COVO_DREAMING_ENABLED") == "true" {
			dreamCfg.Enabled = true
		}
		dreamLogger := cfg.Logger
		if dreamLogger == nil {
			dreamLogger = slog.Default()
		}
		ca.dreamingEngine = evolution.NewDreamingEngine(ca.memory, dreamCfg, cfg.HomeDir, dreamLogger)
		if dreamCfg.Enabled {
			dreamCtx, dreamCancel := context.WithCancel(context.Background())
			ca.dreamingCancel = dreamCancel
			go ca.startDreamingLoop(dreamCtx)
		}
	}

	// Link curator and kanban to prompt builder for context injection
	if ca.curator != nil {
		promptBuilder.SetCurator(ca.curator)
	}
	if km := ca.agentTools.KanbanManager(); km != nil {
		promptBuilder.SetKanbanManager(km)
	}
	if ca.agentTools != nil {
		promptBuilder.SetFindingCatalog(ca.agentTools.FindingCatalog())
	}

	// Build and apply provider middleware chain (outermost first)
	ca.setupProviderChain(&cfg)
	if ca.rolloutRecorder != nil {
		parentRolloutID = ca.rolloutRecorder.ID()
		parentRecorder = ca.rolloutRecorder
	}

	agentCfg := ca.buildAgentConfig(cfg)
	ca.cfg = agentCfg

	// Wire smart approval LLM after provider is available
	ca.wireSmartApproval()

	// Build the auxiliary client for routing lightweight LLM calls (title
	// generation, background review, goal judging, etc.) to independently
	// configured providers/models when available, falling back to the main
	// provider otherwise.
	ca.auxClient = NewAuxiliaryClient(ca.compressionSwitch, cfg.Model, cfg.Auxiliary, cfg.AuxiliaryProviderBuilder, cfg.Logger)
	ca.agentTools.SetImageAnalyzer(ca.auxClient.DescribeImage)

	// Route auxiliary LLM calls (title/review/judge and calls to dedicated
	// auxiliary providers) through the rollout recorder when tracing is
	// enabled, so they are captured even though their provider is not the
	// recorder-wrapped main chain.
	if ca.rolloutRecorder != nil {
		ca.auxClient.SetRolloutRecorder(ca.rolloutRecorder)
	}

	// Wire the auxiliary compression provider to the switch. The switch routes
	// full-compaction LLM calls to a separate provider when auxiliary.compression
	// configures one; a model-only (or absent) override reuses the main chain and
	// the switch guards that self-reference (see CompressionProviderSwitch.Complete).
	// The recorder sits above the switch and captures these calls atomically, so
	// no additional wrapping is needed here.
	ca.compressionSwitch.SetAux(ca.auxClient.Provider(TaskCompression), ca.auxClient.Model(TaskCompression))

	ca.core = agentcore.New(agentCfg)

	// Stamp per-tool concurrency declarations for mixed execution mode.
	// Undeclared tools stay fail-closed (exclusive); see concurrency.go.
	agenttools.ApplyConcurrencyDeclarations(ca.core)
	agenttools.ApplyToolContracts(ca.core)
	agenttools.ApplyMutationLocks(ca.core, ca.workDir)
	agenttools.ApplyReadSummaries(ca.core)

	// Wire BackgroundReviewer to curator for post-session self-improvement
	ca.backgroundReviewer = NewBackgroundReviewerFromFunc(
		func(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
			return ca.invokeForReview(ctx, systemPrompt, userPrompt)
		},
		ReviewCombined,
	)
	if ca.curator != nil {
		ca.curator.SetReviewer(ca.backgroundReviewer)
	}

	// Wrap the context engine in EnhancedContextEngine (carried state,
	// boundary markers, compression switch).
	ca.wrapContextEngine(cfg)

	// Auto-connect MCP servers defined in config.
	if len(cfg.MCPServers) > 0 && ca.agentTools != nil {
		ca.agentTools.AutoConnectMCPServers(context.Background(), cfg.MCPServers)
	}

	// Claude Code protocol: fire SessionStart once the session is set up.
	// Hook failures are non-fatal (the agent still starts); async hooks run
	// in the background and never block construction.
	if ca.shellHooks != nil {
		ca.shellHooks.Invoke("SessionStart", ca.hookEventBase("SessionStart"))
	}

	return ca, nil
}

func (ca *CovoAgent) invokeForReview(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	if ca.auxClient == nil {
		// Fallback for edge cases (e.g. test harness without auxClient)
		req := &agentcore.ProviderRequest{
			Model: ca.cfg.Model,
			Messages: []agentcore.Message{
				{Role: agentcore.RoleSystem, Content: systemPrompt},
				{Role: agentcore.RoleUser, Content: userPrompt},
			},
			Temperature: 0.7,
			MaxTokens:   4096,
		}
		resp, err := ca.cfg.Provider.Complete(ctx, req)
		if err != nil {
			return "", err
		}
		return resp.Content, nil
	}
	return ca.auxClient.Complete(ctx, TaskReview, systemPrompt, userPrompt, 4096, 0.7)
}

// setupProviderChain applies the full provider middleware stack and wraps
// the result in a CompressionProviderSwitch. Called by both NewCovoAgent
// and Rebuild to ensure the provider chain is consistent after a rebuild.
func (ca *CovoAgent) setupProviderChain(cfg *CovoAgentConfig) {
	var mws []ProviderMiddleware
	// Outermost: trace every LLM call that bypasses agentcore's own model span
	// (context compression, titles, review, guardrails, aux providers). Skips
	// when a model span already exists, so agent turns are never double-traced.
	mws = append(mws, agentcore.NewModelSpanMiddleware(telemetry.AgentTracer()))
	// Metrics record every model call (including agent turns), so this
	// middleware deliberately does not skip.
	mws = append(mws, agentcore.NewModelMetricsMiddleware(telemetry.MetricsRecorder()))
	mws = append(mws, cfg.ProviderMiddlewares...)
	mws = append(mws, NewPromptCachingMiddleware(
		os.Getenv("COVO_PROMPT_CACHE") != "false",
		os.Getenv("COVO_PROMPT_CACHE_TTL"),
	))
	mws = append(mws, NewRateLimitTrackingMiddleware(ca.rateLimitState))
	mws = append(mws, NewCostTrackingMiddleware(ca.costTracker, telemetry.MetricsRecorder(), cfg.Logger))
	mws = append(mws, NewStreamHealthMiddleware(cfg.Logger, 0, 0))
	mws = append(mws, NewErrorRecoveryMiddleware(ca))
	cfg.Provider = ApplyProviderMiddleware(cfg.Provider, mws)

	// Wrap the middleware-enhanced provider in a compression switch so
	// EnhancedContextEngine can route compression LLM calls to the auxiliary
	// compression provider when one is configured.
	ca.compressionSwitch = compression.NewCompressionProviderSwitch(cfg.Provider)
	cfg.Provider = ca.compressionSwitch

	// Rollout tracing: opt-in via COVO_ROLLOUT=true. Placed OUTSIDE the
	// compression switch so ALL LLM calls (agent turns, compression, titles,
	// reviews, auxiliary model calls) are captured regardless of routing.
	//
	// NOTE: auxiliary calls routed to dedicated auxiliary providers are wired
	// to the recorder by the caller once the AuxiliaryClient exists (see
	// NewCovoAgent/Rebuild) — setupProviderChain runs before that client is
	// built, so its recorder cannot be set here.
	if envBool("COVO_ROLLOUT", false) {
		ca.rolloutRecorder = rollout.NewRecorder(rollout.RecorderConfig{
			Provider:  cfg.ProviderName,
			Model:     cfg.Model,
			SessionID: ca.sessionMgr.CurrentID(),
			CWD:       cfg.WorkingDir,
			Logger:    cfg.Logger,
			ParentID:  cfg.ParentRolloutID,
		})
		ca.rolloutRecorder.SetInner(cfg.Provider)
		cfg.Provider = ca.rolloutRecorder
	}
}

// wrapContextEngine wraps the agentcore core's context engine in the
// EnhancedContextEngine, wiring the carried state provider and compression
// switch. Called by both NewCovoAgent and Rebuild.
func (ca *CovoAgent) wrapContextEngine(cfg CovoAgentConfig) {
	if engine := ca.core.ContextEngine(); engine != nil {
		engineName := cfg.ContextEngine
		if engineName == "" {
			engineName = os.Getenv("COVO_CONTEXT_ENGINE")
		}
		if engineName == "" {
			engineName = "enhanced"
		}
		if factory := compression.GetContextEngineFactory(engineName); factory != nil {
			wrapped := factory(engine)
			if enh, ok := wrapped.(*compression.EnhancedContextEngine); ok {
				enh.SetStateProvider(ca.carriedStateForCompaction)
				enh.SetCompressionSwitch(ca.compressionSwitch)
				enh.SetImageAssetDir(func() string {
					if ca.sessionMgr == nil {
						return ""
					}
					id := ca.sessionMgr.CurrentID()
					if id == "" || filepath.Base(id) != id || id == "." || id == ".." {
						return ""
					}
					return filepath.Join(cfg.HomeDir, "session-assets", id)
				})
			}
			ca.core.SetContextEngine(wrapped)
		}
	}
}

// turnBudget resolves the per-run turn limit. 0 (unset) means effectively
// unlimited, matching the main loop; an explicit value bounds the run.
func turnBudget(maxTurns int) int64 {
	if maxTurns > 0 {
		return int64(maxTurns)
	}
	return math.MaxInt64
}

func (ca *CovoAgent) buildAgentConfig(cfg CovoAgentConfig) agentcore.Config {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelWarn)}))
	}

	// Parallel is the default: every tool call joins the bounded rolling
	// pool, so concurrent execution is the norm without user configuration.
	// Tools that explicitly declared ConcurrencySafe=false still run alone
	// as serial barriers inside the parallel executor. Opt out further with
	// execution.mode: mixed (undeclared tools fail closed to serial
	// barriers) or serial (fully sequential), via config or the
	// EXECUTION_MODE environment variable.
	execMode := ResolveExecMode(cfg.ExecutionMode)

	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		if v := os.Getenv("TOOL_CONCURRENCY"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				concurrency = n
			}
		}
	}
	if concurrency <= 0 {
		concurrency = 10
	}

	computerUse := envBool("COMPUTER_USE_ENABLED", false)
	if cfg.ComputerUse != nil {
		computerUse = *cfg.ComputerUse
	}

	if ca.fsOps == nil {
		ca.fsOps = newSwappableFS()
	}
	if cfg.Remote.Enabled() {
		remoteFS := &workspace.RemoteFS{Spec: cfg.Remote}
		ca.fsOps.set(remoteFS)
	}
	readCfg := &tools.ReadToolConfig{Operations: ca.fsOps}
	if cfg.ModelContextLength > 0 {
		// 10% of context window in tokens × 4 bytes/tok = context * 0.4
		maxBytes := int64(float64(cfg.ModelContextLength) * 0.4)
		if maxBytes < 50*1024 {
			maxBytes = 50 * 1024
		}
		readCfg.MaxBytes = maxBytes
	}
	readCfg.MediaHandler = func(ctx context.Context, kind tools.ReadMediaKind, paths []string, input tools.ReadToolInput) (any, error) {
		var analyze toolsmedia.ImageAnalyzer
		if ca.auxClient != nil {
			analyze = ca.auxClient.DescribeImage
		}
		transport := NewSafeClient().Transport
		switch kind {
		case tools.ReadMediaImage:
			if len(paths) == 1 && !isRemoteMediaPath(paths[0]) && strings.TrimSpace(input.Prompt) == "" {
				return agenttools.AnalyzeImage(ctx, paths[0], cfg.ProviderName, analyze)
			}
			return toolsmedia.AnalyzeImages(ctx, toolsmedia.AnalyzeImagesInput{
				Paths:     paths,
				Prompt:    input.Prompt,
				WorkDir:   cfg.WorkingDir,
				Analyze:   analyze,
				Transport: transport,
			})
		case tools.ReadMediaPDF:
			if len(paths) != 1 {
				return nil, fmt.Errorf("pdf read accepts one path")
			}
			if input.Info {
				return toolsmedia.PDFInfo(ctx, paths[0], transport)
			}
			return toolsmedia.ExtractPDF(ctx, paths[0], input.Pages, input.MaxPages, transport)
		case tools.ReadMediaVideo:
			if len(paths) != 1 {
				return nil, fmt.Errorf("video read accepts one path")
			}
			return toolsmedia.AnalyzeVideo(ctx, toolsmedia.AnalyzeVideoInput{
				Path:         paths[0],
				Prompt:       input.Prompt,
				Frames:       input.Frames,
				StartSeconds: input.StartSeconds,
				EndSeconds:   input.EndSeconds,
				WorkDir:      cfg.WorkingDir,
				Analyze:      analyze,
				Transport:    transport,
			})
		default:
			return nil, fmt.Errorf("unsupported read media kind %q", kind)
		}
	}
	var bashCfg *tools.BashToolConfig
	if cfg.Remote.Enabled() {
		bashCfg = &tools.BashToolConfig{Operations: &workspace.RemoteBash{Spec: cfg.Remote}}
	}
	var editCfg *tools.EditToolConfig
	if cfg.Remote.Enabled() {
		editCfg = &tools.EditToolConfig{Operations: &workspace.RemoteFS{Spec: cfg.Remote}}
	}
	browserCfg := browserToolConfigFromEnv()
	if cfg.Mode.IsCyberFamily() {
		browserCfg.NetworkObservation = cyberBrowserNetworkConfig(ca.FindingCatalog(), logger)
	}
	toolExt := agenttools.NewBrowserToolsExtension(tools.ExtensionConfig{
		WorkingDir: cfg.WorkingDir,
		Read:       readCfg,
		Edit:       editCfg,
		WriteFile:  &tools.WriteFileToolConfig{Operations: ca.fsOps},
		Bash:       bashCfg,
		Browser:    browserCfg,
		WebSearch:  webSearchToolConfigFromAgent(cfg.ProviderName),
		// Persistent terminal sessions: PTY-backed shells whose working
		// directory, environment, and background jobs survive across calls,
		// complementing the per-invocation bash tool. Env opt-out keeps
		// existing setups unchanged.
		Terminal: terminalToolConfigFromEnv(),
		ExecuteCode: &tools.ExecuteCodeToolConfig{
			PythonCommand:  os.Getenv("EXECUTE_CODE_PYTHON"),
			CommandTimeout: envDurationSeconds("EXECUTE_CODE_TIMEOUT", 0),
			MaxOutputBytes: 1 * 1024 * 1024, // 1MB
			// Programmatic Tool Calling (PTC): lets execute_code scripts call
			// back into a whitelisted subset of agent tools via RPC without
			// those responses entering the LLM's context. Dispatches through
			// InvokeTool (not a raw Func call) so audit logging, guardrails,
			// and other hooks still apply exactly as for normal model-issued
			// tool calls. ca.core doesn't exist yet at this point in
			// construction -- this closure captures ca and resolves lazily,
			// by which time ca.core is set (a few lines below, after
			// agentcore.New runs).
			ToolInvoker: func(ctx context.Context, name string, args json.RawMessage) (string, error) {
				if ca.core == nil {
					return "", fmt.Errorf("agent not ready")
				}
				return ca.core.InvokeTool(ctx, name, args)
			},
		},
		ComputerUse: computerUse,
	}, func(ctx context.Context, prompt, mediaType, dataURL string) (string, error) {
		if ca.auxClient == nil {
			return "", fmt.Errorf("vision model is not configured")
		}
		return ca.auxClient.DescribeImage(ctx, prompt, mediaType, dataURL)
	}, agenttools.BrowserOptions{ProfileDir: os.Getenv("BROWSER_PROFILE_DIR")})

	pluginRoots := plugin.DiscoverHome(cfg.HomeDir).SkillRoots()
	skillExtra := append(append([]string{}, cfg.SkillExtraDirs...), pluginRoots...)
	skillPaths := skillLoadPaths(cfg.HomeDir+"/skills", cfg.WorkingDir, skillExtra)
	if len(cfg.SkillURLs) > 0 {
		syncer := evolution.NewSkillURLSyncer(
			evolution.UrlSkillCacheDefault(cfg.HomeDir), logger)
		skillPaths = append(skillPaths, syncer.Sync(cfg.SkillURLs)...)
	}
	if ca.skillMgr != nil {
		ca.skillMgr.SetExtraRoots(skillPaths)
	}
	loadedSkills, skillDiags, _ := skill.Load(skillPaths...)
	for _, d := range skillDiags {
		logger.Info("skill diagnostic", "path", d.Path, "message", d.Message)
	}
	ca.loadedSkills = loadedSkills
	ca.skillDiags = skillDiags
	loadedSkills = filterLoadedSkills(loadedSkills, cfg.SkillDisabled, runtime.GOOS)

	skillExt := agentcore.NewSkillExtension(loadedSkills, nil)

	evolutionExt := evolution.NewEvolutionExtension(ca.memory, ca.skillMgr, ca.bundleMgr, ca.skillUsage, func() string { return ca.sessionMgr.CurrentID() })
	evolutionExt.SetWorkshopStore(ca.workshopStore)
	if cfg.SkillConfigGet != nil || cfg.SkillConfigSet != nil {
		evolutionExt.SetSkillConfigStore(skillConfigStore{
			get: cfg.SkillConfigGet,
			set: cfg.SkillConfigSet,
		})
	}

	systemPrompt := ca.promptBuilder.Build(cfg.Mode)

	// --- Toolset filtering ---
	platform := cfg.Platform
	if platform == "" {
		platform = "cli"
	}

	availability := toolset.NewToolAvailability()
	ca.registerToolChecks(availability)

	cachedFilter := toolset.NewCachedFilter()
	platformToolsets := toolset.NewPlatformToolsets(platform)

	// Toolset override: from config field (spawnRunner) or env var (CLI compatibility)
	if len(cfg.ToolsetOverride) > 0 {
		names := cfg.ToolsetOverride
		if cfg.Mode == ModeCyber {
			names = toolset.EnsureCyberToolset(names)
		}
		platformToolsets.SetOverride(platform, names)
	} else if override := os.Getenv("COVO_TOOLSETS"); override != "" {
		var names []string
		for _, n := range splitAndTrim(override) {
			names = append(names, n)
		}
		if cfg.Mode == ModeCyber {
			names = toolset.EnsureCyberToolset(names)
		}
		platformToolsets.SetOverride(platform, names)
	} else if cfg.Mode == ModeCyber {
		platformToolsets.SetOverride(platform, toolset.EnsureCyberToolset(platformToolsets.ToolsetNames(platform)))
	}

	toolsetFilter := toolset.NewToolsetFilter(toolset.ToolsetFilterConfig{
		Platform:     platformToolsets,
		Availability: availability,
		Filter:       cachedFilter,
		NameFilter:   cfg.ToolNameFilter,
		ContextFilter: func(ctx context.Context, name string) bool {
			if !cfg.Mode.IsCyberFamily() {
				return true
			}
			instanceRole := strings.TrimSpace(cfg.SubagentRole)
			if instanceRole == "" {
				instanceRole = "parent"
			}
			return cyber.AllowAgentTool(instanceRole, toolssubagent.SubagentRoleFromContext(ctx), name)
		},
		ToolNames: func() []string {
			if ca.core != nil {
				return ca.core.ToolNames()
			}
			return nil
		},
		PlatformName:    func() string { return platform },
		PlanModeChecker: func() bool { return ca.IsPlanMode() },
		LeanMode:        cfg.LeanTools,
		LeanCyber:       cfg.Mode.IsCyberFamily(),
		CyberMode:       cfg.Mode.IsCyberFamily(),
		Logger:          logger,
	})
	ca.toolsetFilter = toolsetFilter
	ca.agentTools.SetToolDiscoveryPolicy(func(ctx context.Context, names []string) []string {
		return toolsetFilter.DiscoveryPreview(ctx, names).Kept
	})

	lifecycleHooks := []agentcore.LifecycleHook{
		newLifecycleContributorBridge(ca, lifecycle.Global()),
		newGuardrailLifecycleHook(ca),
	}
	// Auto-persist session state after each turn
	lifecycleHooks = append(lifecycleHooks, ca.newAutoSaveHook())
	// Rollout tracing: capture turn boundaries and tool results when enabled.
	if ca.rolloutRecorder != nil {
		rolloutHook := rollout.NewHook(ca.rolloutRecorder, ca.rolloutStore, cfg.Logger)
		lifecycleHooks = append(lifecycleHooks, rolloutHook)
	}
	for _, pluginHook := range cfg.LifecycleHooks {
		lifecycleHooks = append(lifecycleHooks, pluginHook)
	}
	if ca.failureTracker != nil {
		lifecycleHooks = append(lifecycleHooks, NewFailureReviewLifecycleHook(ca.failureTracker, ca))
	}
	// Goal accounting: hooks into token usage, tool finish, and turn lifecycle.
	ca.goalHook = newGoalAccountingHook(ca)
	lifecycleHooks = append(lifecycleHooks, ca.goalHook)

	// Doom loop turn tracking: increment turn counter and reset per-turn history.
	lifecycleHooks = append(lifecycleHooks, newDoomloopLifecycleHook(ca))

	// Inbox auto-drain: inject pending async messages at run start.
	lifecycleHooks = append(lifecycleHooks, newInboxDrainHook(ca))

	// Stop gate: nudge the model to finish outstanding todos before stopping
	// (deterministic task gate; see stop_gate.go). Disable with COVO_STOP_GATE=false.
	if envBool("COVO_STOP_GATE", true) {
		lifecycleHooks = append(lifecycleHooks, newStopGateHook(ca))
	}

	// Verification guard: nudge the model to test/build after editing code files.
	// Disable with COVO_VERIFICATION_GUARD=false.
	if envBool("COVO_VERIFICATION_GUARD", true) {
		lifecycleHooks = append(lifecycleHooks, newVerificationGuardHook(ca))
	}

	// Commitment inference: scan assistant responses for commitment-like patterns
	// and persist them for later review.
	lifecycleHooks = append(lifecycleHooks, newCommitmentHook(ca))

	// Worktree GC: prune stale git worktree tracking entries on startup.
	lifecycleHooks = append(lifecycleHooks, newWorktreeGCHook(ca))

	// Heartbeat: periodic proactive check-in during long runs.
	// Enabled via COVO_HEARTBEAT_INTERVAL env var (e.g. "30m", "1h").
	if hb := NewHeartbeatHookFromEnv(logger); hb != nil {
		lifecycleHooks = append(lifecycleHooks, hb)
	}

	// Extensions provide tools. The LSP navigation tool (definition/references/
	// hover) is added when LSP is enabled so the model can navigate code by
	// symbol semantics instead of grep.
	extensions := []agentcore.Extension{toolExt, skillExt, evolutionExt, ca.agentTools}
	if ca.lspManager != nil {
		extensions = append(extensions, newLSPNavExtension(ca.lspManager, cfg.WorkingDir))
	}
	// Runtime extensions: load tools from ~/.covo-agent/extensions/.
	if cfg.HomeDir != "" {
		extDir := filepath.Join(cfg.HomeDir, "extensions")
		extMgr := extension.NewManager(extDir)
		extensions = append(extensions, extension.NewAgentExtension(extMgr))
	}

	lifecycleHooks = append(lifecycleHooks, toolsetFilter)

	return agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Name:             string(cfg.Mode),
			Model:            cfg.Model,
			Provider:         cfg.Provider,
			Streaming:        true,
			Thinking:         cfg.ThinkingCfg,
			FrequencyPenalty: cfg.FrequencyPenalty,
			PresencePenalty:  cfg.PresencePenalty,
			ResponseFormat:   cfg.ResponseFormat,
		},
		SystemPrompt: systemPrompt,
		SkillConfig: agentcore.SkillConfig{
			AvailableSkills:  loadedSkills,
			SkillPaths:       skillPaths,
			SkillDiagnostics: skillDiags,
		},
		ExecutionConfig: agentcore.ExecutionConfig{
			ExecutionMode:      execMode,
			Concurrency:        concurrency,
			MaxTurns:           turnBudget(cfg.MaxTurns),
			ValidateArguments:  true,
			ArgumentRepairFunc: RepairToolCallArguments,
			SteeringMode:       agentcore.SteeringAll,
			FollowUpMode:       agentcore.SteeringAll,
			RepetitionRecovery: repetitionRecoveryConfig(),
			GlobalBefore: []agentcore.BeforeHook{
				ca.planModeGateBeforeHook(),
				ca.customModeToolGateBeforeHook(),
				agentcore.LoggingBeforeHook(logger),
				ca.auditBeforeHook(),
				ca.shellHookBeforeHook(),
				ca.toolGuardrailBeforeHook(),
				ca.fileSafetyBeforeHook(),
				ca.bashSafetyBeforeHook(),
				ca.diffApprovalBeforeHook(),
				ca.lspBeforeHook(),
				ca.readTracker.PriorReadBeforeHook(),
				ca.permissionGateBeforeHook(),
				ca.trajectoryBeforeHook(),
			},
			GlobalAfter: []agentcore.AfterHook{
				agentcore.LoggingAfterHook(logger),
				ca.auditAfterHook(),
				ca.toolGuardrailAfterHook(),
				ca.classifyAfterHook(),
				ca.readTracker.PriorReadAfterHook(),
				ca.trajectoryAfterHook(),
				ca.snapshotAfterHook(),
				ca.doomloopAfterHook(),
			},
			Middleware: []agentcore.Middleware{
				ca.rateLimitToolMiddleware(),
				agentcore.TimeoutMiddleware(120 * time.Second),
			},
		},
		CompactionConfig: compression.BuildEnhancedCompactionConfig(cfg.ModelContextLength),
		RetryConfig: &agentcore.RetryConfig{
			MaxRetries:  3,
			BaseDelayMs: 1000,
			MaxDelayMs:  15000,
		},
		BeforeToolCall:     ca.combinedBeforeToolCall(),
		AfterToolCall:      ca.chainAfterToolCall(),
		PostProcessResults: ca.turnBudgetPostProcess(),
		Store:              ca.sessionMgr.Store(),
		Lifecycle:          agentcore.LifecycleChain(lifecycleHooks),
		Extensions:         extensions,
		Tracer:             telemetry.AgentTracer(),
		Metrics:            telemetry.MetricsRecorder(),
	}
}

// registerToolChecks registers availability check functions for tools that
// require external dependencies (API keys, CLI tools, etc.).
func (ca *CovoAgent) registerToolChecks(avail *toolset.ToolAvailability) {
	// Tools requiring FAL_KEY
	for _, name := range []string{"image_generate", "video_generate", "music_generate"} {
		toolName := name // capture
		avail.RegisterCheck(toolName, func() (string, bool) {
			if os.Getenv("FAL_KEY") == "" {
				return "FAL_KEY not set", false
			}
			return "", true
		})
	}

	// TTS requires edge-tts Python package
	avail.RegisterCheck("tts", func() (string, bool) {
		if _, err := lookPathSafe("edge-tts"); err == nil {
			return "", true
		}
		if _, err := lookPathSafe("python3"); err == nil {
			if cmdErr := exec.Command("python3", "-m", "edge_tts", "--version").Run(); cmdErr == nil {
				return "", true
			}
		}
		return "edge-tts not installed", false
	})

	// transcribe requires whisper-cli, whisper python, or OPENAI_API_KEY
	avail.RegisterCheck("transcribe", func() (string, bool) {
		if _, err := lookPathSafe("whisper-cli"); err == nil {
			return "", true
		}
		if _, err := lookPathSafe("whisper"); err == nil {
			return "", true
		}
		if os.Getenv("OPENAI_API_KEY") != "" {
			return "", true
		}
		return "no transcription backend available (install whisper.cpp, openai-whisper, or set OPENAI_API_KEY)", false
	})

	// sandbox requires docker, ssh, or falls back to local
	avail.RegisterCheck("sandbox", func() (string, bool) {
		if ca.sandbox != nil {
			return "", true
		}
		return "sandbox not configured (set DOCKER_IMAGE, SSH_HOST, or use local)", false
	})

	// web_search has a default no-key HTTP backend; API-backed search can be
	// configured for higher reliability but is not required for availability.
}

func lookPathSafe(name string) (string, error) {
	return exec.LookPath(name)
}

func isRemoteMediaPath(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func splitAndTrim(s string) []string {
	var result []string
	for _, part := range strings.Split(s, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func (ca *CovoAgent) Close() {
	// Wait out the async baseline snapshot: after Close no new work arrives,
	// and the background goroutine must not outlive the store it writes to
	// (its data dir may be torn down right after this, e.g. in tests).
	if ca.snapshotMgr != nil {
		ca.snapshotMgr.Shutdown()
	}

	if ca.trajectory != nil {
		ca.trajectory.Save(false)
	}

	// Fire post-session curator pipeline: background review, skill extraction,
	// and nudge evaluation. Runs async internally; does not block Close().
	if ca.curator != nil {
		ca.curator.OnSessionEnd(trajectoryToMaps(ca.Trajectory().Snapshot()), ca.auxLLMCall())
	}

	if ca.curatorCancel != nil {
		ca.curatorCancel()
	}
	if ca.dreamingCancel != nil {
		ca.dreamingCancel()
	}
	if ca.shellHooks != nil {
		ca.shellHooks.Stop()
	}
	if ca.commands != nil {
		ca.commands.Close()
	}
	if ca.core != nil {
		ca.core.Close()
	}
	if ca.sessionMgr != nil {
		ca.sessionMgr.Close()
	}
	if ca.gitWorktree != nil {
		if preserved, err := ca.gitWorktree.Cleanup(); preserved != "" {
			fmt.Fprintf(os.Stderr, "\n  Worktree preserved at: %s (has unpushed commits)\n", preserved)
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "\n  Worktree cleanup: %v\n", err)
		}
	}
}

func (ca *CovoAgent) Run(ctx context.Context, input string) (result string, err error) {
	// Observability root span: carries the session id so model/tool spans nest
	// under one trace per run and Langfuse can group by session. No-op when
	// telemetry is disabled.
	if tracer := telemetry.AgentTracer(); tracer != nil {
		var span agentcore.Span
		ctx, span, _ = agentcore.StartComponentRun(ctx, tracer, "covo_agent", "run")
		defer span.End()
		if sm := ca.SessionManager(); sm != nil {
			if sessionID := sm.CurrentID(); sessionID != "" {
				span.SetAttributes(
					agentcore.Attr("session.id", sessionID),
					agentcore.Attr("langfuse.session.id", sessionID),
				)
			}
		}
	}

	if ca.trajectory != nil {
		ca.trajectory.RecordUser(RedactSensitiveTextForce(input))
	}

	// Recover from panics to prevent stack traces from corrupting the TUI.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("agent panic recovered: %v", r)
		}
	}()

	// Mark active goal for this run (so the hook tracks usage against it).
	g, _ := ca.goalStore.Get(ctx, ca.sessionMgr.CurrentID())
	if g != nil && g.Status == goal.StatusActive {
		ca.goalHook.AccountingState().MarkGoalActive(g.GoalID)

		// Inject active goal as steering context — once per run, not accumulatively.
		if ca.goalHook.AccountingState().SteeringInjected("continuation") {
			steering := ca.goalSteering.ContinuationPrompt(g)
			ca.core.State().AddMessage(agentcore.Message{
				Role:    agentcore.RoleSystem,
				Content: steering,
			})
		}
	}

	// Inject repo map as system context for code structure awareness.
	// Only injects on first run or when the map changes, to avoid context bloat.
	if ca.repoMap != nil {
		if mapContent := ca.repoMap.Build(); mapContent != "" && mapContent != ca.lastRepoMapInjected {
			ca.core.State().AddMessage(agentcore.Message{
				Role:    agentcore.RoleSystem,
				Content: mapContent,
			})
			ca.lastRepoMapInjected = mapContent
		}
	}

	// Claude Code protocol: UserPromptSubmit fires on every user message
	// before the run starts. A "deny"/"block" decision aborts the run.
	if err := ca.claudeHooksCheckUserPrompt(input); err != nil {
		return "", err
	}

	result, err = ca.runQueued(ctx, ModePrompt, input, func(ctx context.Context, input string) (string, error) {
		return ca.core.Run(ctx, input)
	})

	if ca.trajectory != nil {
		ca.trajectory.RecordAssistant(result, nil)
		ca.trajectory.Save(err == nil)
	}

	if err == nil && result != "" && !ca.titleGenerated {
		ca.titleGenerated = true
		ca.MaybeAutoTitle(ctx, input, result)
		ca.MaybeAutoSummary(ctx, input, result)
	}

	return result, err
}

// RunDirect runs a single Core().Run under a tracing root span (without the
// session-manager wiring of Run). Used by headless, oneshot, review, background
// tasks, and cron jobs so model/tool spans export as one trace per invocation.
func (ca *CovoAgent) RunDirect(ctx context.Context, input string) (string, error) {
	return ca.RunDirectWithSession(ctx, input, "")
}

// RunDirectWithSession is RunDirect with an explicit session id (used when the
// caller wants a dedicated observability session, e.g. "bg-<task>"). When
// sessionID is empty it falls back to the agent's current session manager id.
func (ca *CovoAgent) RunDirectWithSession(ctx context.Context, input, sessionID string) (result string, err error) {
	if tracer := telemetry.AgentTracer(); tracer != nil {
		var span agentcore.Span
		ctx, span, _ = agentcore.StartComponentRun(ctx, tracer, "covo_agent", "run")
		defer span.End()
		if sessionID == "" {
			if sm := ca.SessionManager(); sm != nil {
				sessionID = sm.CurrentID()
			}
		}
		if sessionID != "" {
			span.SetAttributes(
				agentcore.Attr("session.id", sessionID),
				agentcore.Attr("langfuse.session.id", sessionID),
			)
		}
	}

	// Recover from panics so a background task, cron job, or headless run
	// cannot crash a process that may host the interactive TUI.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("agent panic recovered: %v", r)
		}
	}()

	// Claude Code protocol: headless/oneshot inputs get the same
	// UserPromptSubmit gate as interactive runs.
	if err := ca.claudeHooksCheckUserPrompt(input); err != nil {
		return "", err
	}

	return ca.runQueued(ctx, ModePrompt, input, func(ctx context.Context, input string) (string, error) {
		return ca.Core().Run(ctx, input)
	})
}

func (ca *CovoAgent) acquireLease() error {
	owner := string(ca.surface)
	if owner == "" {
		owner = string(SurfaceTerminal)
	}
	if _, err := ca.lease.Claim(owner); err != nil {
		return err
	}
	return ca.lease.begin(0)
}

func (ca *CovoAgent) State() *agentcore.AgentState {
	return ca.core.State()
}

// runQueued admits a turn through the session command queue. Same-owner
// callers wait instead of failing when a turn is already in flight. A
// different owner is still rejected by the lease.
func (ca *CovoAgent) runQueued(ctx context.Context, mode CommandMode, input string, run func(context.Context, string) (string, error)) (string, error) {
	if ca == nil || ca.commands == nil || run == nil {
		if err := ca.acquireLease(); err != nil {
			return "", err
		}
		defer ca.lease.end()
		return run(ctx, input)
	}
	owner := string(ca.surface)
	if owner == "" {
		owner = string(SurfaceTerminal)
	}
	cmd := ca.commands.Enqueue(owner, mode, commandPriorityFor(mode), input)
	if !ca.commands.tryStartWorker() {
		return cmd.Wait(ctx)
	}
	ca.drainCommands(run)
	ca.commands.endWorker()
	if ca.commands.Len() > 0 && ca.commands.tryStartWorker() {
		go func() {
			defer ca.commands.endWorker()
			ca.drainCommands(run)
		}()
	}
	return cmd.Wait(ctx)
}

func (ca *CovoAgent) drainCommands(run func(context.Context, string) (string, error)) {
	if ca == nil || ca.commands == nil {
		return
	}
	for {
		cmd, err := ca.commands.tryDequeue()
		if err != nil || cmd == nil {
			return
		}
		if _, claimErr := ca.lease.Claim(cmd.Owner); claimErr != nil {
			cmd.finish("", claimErr)
			continue
		}
		if beginErr := ca.lease.begin(0); beginErr != nil {
			cmd.finish("", beginErr)
			continue
		}
		out, runErr := run(context.Background(), cmd.Input)
		ca.lease.end()
		cmd.finish(out, runErr)
		if ca.commands.Len() == 0 {
			return
		}
	}
}

// AuxClient returns the auxiliary client used for routing lightweight LLM
// calls (title generation, background review, goal judging, etc.) to
// independently configured providers/models.
func (ca *CovoAgent) AuxClient() *AuxiliaryClient {
	return ca.auxClient
}

func (ca *CovoAgent) Rebuild(mode AgentMode, provider agentcore.Provider, providerName, model, workingDir, homeDir string) error {
	ca.mode = mode
	ca.providerName = providerName
	ca.model = model
	ca.workDir = workingDir

	// Preserve existing lifecycle hooks from previous config
	var existingHooks []agentcore.LifecycleHook
	if chain, ok := ca.cfg.Lifecycle.(agentcore.LifecycleChain); ok && len(chain) > 1 {
		existingHooks = append(existingHooks, chain[1:]...)
	}

	cfg := ca.baseCfg
	cfg.Mode = mode
	cfg.Provider = ca.baseCfg.Provider // start from original (unwrapped) provider
	cfg.ProviderName = providerName
	cfg.Model = model
	cfg.WorkingDir = workingDir
	cfg.HomeDir = homeDir
	cfg.LifecycleHooks = existingHooks
	if env := strings.ToLower(strings.TrimSpace(os.Getenv("COVO_TOOLS_LEAN"))); env == "" {
		cfg.LeanTools = mode.LeanDefault() || len(cfg.MCPServers) > 1
	}

	// Re-apply the full provider middleware chain + compression switch
	// so the rebuilt agent has the same provider stack as NewCovoAgent.
	ca.setupProviderChain(&cfg)

	if ca.core != nil {
		ca.core.Close()
	}
	ca.cfg = ca.buildAgentConfig(cfg)
	ca.core = agentcore.New(ca.cfg)
	agenttools.ApplyConcurrencyDeclarations(ca.core)
	agenttools.ApplyToolContracts(ca.core)
	agenttools.ApplyMutationLocks(ca.core, ca.workDir)
	agenttools.ApplyReadSummaries(ca.core)

	// Re-wrap the context engine in EnhancedContextEngine (carried state,
	// boundary markers, compression switch).
	ca.wrapContextEngine(cfg)

	// Update the auxiliary client with the new main provider/model so
	// model-only overrides re-resolve to the new provider. Full auxiliary
	// provider overrides are preserved.
	if ca.auxClient != nil {
		ca.auxClient.SetMainProvider(ca.compressionSwitch, cfg.Model)
		// Re-wire the auxiliary compression provider to the new switch (the
		// switch guards the fallback self-reference; the recorder above it
		// captures compaction calls atomically).
		ca.compressionSwitch.SetAux(ca.auxClient.Provider(TaskCompression), ca.auxClient.Model(TaskCompression))
		// Re-wire the rollout recorder (create a fresh recorder for the
		// rebuilt provider chain; as with construction, this must happen
		// after the auxiliary client exists).
		if ca.rolloutRecorder != nil {
			ca.auxClient.SetRolloutRecorder(ca.rolloutRecorder)
		}
	}
	return nil
}

func (ca *CovoAgent) startDreamingLoop(ctx context.Context) {
	ca.baseCfg.Logger.Info("dreaming engine started",
		"interval", ca.dreamingEngine.Config().Interval)
	ticker := time.NewTicker(ca.dreamingEngine.Config().Interval)
	defer ticker.Stop()

	ca.runDreamingSweep()

	for {
		select {
		case <-ctx.Done():
			ca.baseCfg.Logger.Info("dreaming engine stopped")
			return
		case <-ticker.C:
			ca.runDreamingSweep()
		}
	}
}

func (ca *CovoAgent) runDreamingSweep() {
	if ca.dreamingEngine == nil || ca.baseCfg.Logger == nil {
		return
	}
	entry, err := ca.dreamingEngine.Run(context.Background())
	if err != nil {
		ca.baseCfg.Logger.Warn("dreaming sweep failed", "error", err)
		return
	}
	ca.baseCfg.Logger.Info("dreaming sweep complete",
		"entries_in", entry.EntriesIn,
		"entries_out", entry.EntriesOut,
		"stale", entry.StaleFound,
		"conflicts", entry.Conflicts,
		"summary", entry.Summary)
}

func (ca *CovoAgent) DreamingEngine() *evolution.DreamingEngine {
	return ca.dreamingEngine
}

// RolloutRecorder returns the rollout recorder, or nil if rollout tracing is disabled.
func (ca *CovoAgent) RolloutRecorder() *rollout.Recorder {
	return ca.rolloutRecorder
}

// RolloutStore returns the rollout store, or nil if rollout tracing is disabled.
func (ca *CovoAgent) RolloutStore() *rollout.Store {
	return ca.rolloutStore
}

func (ca *CovoAgent) LifecycleHooks() []agentcore.LifecycleHook {
	lifecycle := ca.cfg.Lifecycle
	if chain, ok := lifecycle.(agentcore.LifecycleChain); ok {
		// skip the first which is always toolsetFilter
		if len(chain) > 1 {
			return chain[1:]
		}
		return nil
	}
	return nil
}

// FindingCatalog returns the cyber ledger, or nil when tools are not wired.
func (ca *CovoAgent) FindingCatalog() *cyber.Catalog {
	if ca == nil || ca.agentTools == nil {
		return nil
	}
	return ca.agentTools.FindingCatalog()
}
