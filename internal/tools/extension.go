package tools

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/covoyage/covo-agent/internal/audit"
	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/covoyage/covo-agent/internal/goal"
	"github.com/covoyage/covo-agent/internal/inbox"
	"github.com/covoyage/covo-agent/internal/kanban"
	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/sandbox"
	toolscanvas "github.com/covoyage/covo-agent/internal/tools/canvas"
	toolscodegraph "github.com/covoyage/covo-agent/internal/tools/codegraph"
	toolscommitments "github.com/covoyage/covo-agent/internal/tools/commitments"
	toolscontext "github.com/covoyage/covo-agent/internal/tools/context"
	externalagent "github.com/covoyage/covo-agent/internal/tools/external_agent"
	toolsfeishu "github.com/covoyage/covo-agent/internal/tools/feishu"
	"github.com/covoyage/covo-agent/internal/tools/fileops"
	toolshardware "github.com/covoyage/covo-agent/internal/tools/hardware"
	"github.com/covoyage/covo-agent/internal/tools/hook"
	toolsmedia "github.com/covoyage/covo-agent/internal/tools/media"
	toolsmemory "github.com/covoyage/covo-agent/internal/tools/memory"
	toolsplanning "github.com/covoyage/covo-agent/internal/tools/planning"
	toolssandbox "github.com/covoyage/covo-agent/internal/tools/sandbox"
	toolssessions "github.com/covoyage/covo-agent/internal/tools/sessions"
	toolsstandingorders "github.com/covoyage/covo-agent/internal/tools/standingorders"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
	toolswarm "github.com/covoyage/covo-agent/internal/tools/swarm"
	toolsworktree "github.com/covoyage/covo-agent/internal/tools/worktree"
	"github.com/covoyage/covonaut/agentcore"
)

// Extension registers agent-specific productivity and media tools.
type Extension struct {
	agentcore.BaseLifecycleHook
	sessionsDir   string
	homeDir       string
	todoStore     *toolsplanning.TodoStore
	planStore     *toolsplanning.PlanStore
	cronStore     *CronStore
	spawnStore    *toolssubagent.SpawnStore
	mcpStore      *MCPStore
	processStore  *toolssandbox.ProcessStore
	goalStoreSQL  *goal.Store
	sessionIDFn   func() string
	memoryStore   *toolsmemory.EnhancedMemoryStore
	vectorMem     *toolsmemory.VectorMem
	subagentReg   *toolssubagent.SubagentRegistry
	feishuClient  *toolsfeishu.FeishuClient
	ftsSearcher   *toolssessions.FTSSearcher
	compressor    *toolscontext.ContextCompressor
	hooks         *hook.HookRegistry
	dreamCycle    *toolsmemory.DreamCycle
	forkGuard     *toolssubagent.ForkGuard
	fragLimiter   *toolscontext.FragmentTokenLimiter
	metrics       *toolMetrics
	fileState     *FileStateRegistry
	eventBus      *hook.EventBus
	permDeferred  *PermissionDeferred
	checkpointW   *CheckpointWriter
	sessionFork   *toolssessions.SessionFork
	runnerMutex   *toolssessions.RunnerMutex
	worktreeMgr   *toolsworktree.WorktreeManager
	inboxStore    *inbox.Store
	auditStore    *audit.Store
	subagentStore *toolssubagent.SubagentStore

	// Expose for embedding into agent loop
	Hooks           *hook.HookRegistry
	swarmBoard      *toolswarm.SwarmBoard
	swarmOrch       *toolswarm.SwarmOrchestrator
	secretsStore    *SecretsStore
	kanbanManager   *kanban.KanbanManager
	commitmentStore *toolscommitments.CommitmentStore
	standingOrders  *toolsstandingorders.StandingOrdersStore
	workshopStore   *evolution.WorkshopStore
	skillMgr        *evolution.SkillManager
	spawnRunner     toolssubagent.SpawnRunner
	subagentRunner  *toolssubagent.SubagentRunner
	handoffCallback HandoffCallback
	askUserCallback AskUserFunc
	sandbox         sandbox.Sandbox
	logger          *slog.Logger
	tools           []*agentcore.Tool
	toolProfile     string
	parentMessages  func() []agentcore.Message
	phaseTransition toolsplanning.PhaseTransitioner
	externalAgents  *externalagent.Registry
}

// ExtensionConfig configures the agent tools extension.
type ExtensionConfig struct {
	// SessionsDir is the directory where session JSONL files are stored.
	SessionsDir string
	// HomeDir is the agent home directory (~/.covo-agent).
	HomeDir string
	// SpawnRunner is the function used to run spawned child sessions.
	SpawnRunner toolssubagent.SpawnRunner
	// HandoffCallback is the function used to block on user input for human_handoff.
	HandoffCallback HandoffCallback
	// AskUserCallback is the function used to present a structured question to
	// the user for the ask_user tool. May be nil: when nil, ask_user degrades
	// to the caller-provided default (or fails when no default is given), which
	// is the right behaviour for headless/cron/oneshot runs.
	AskUserCallback AskUserFunc
	// Sandbox is the sandbox for isolated command execution.
	Sandbox sandbox.Sandbox
	// Logger for MCP auto-connect and other operations.
	Logger *slog.Logger
	// SubagentRunnerConfig configures the subagent safety wrapper (timeout, heartbeat, depth).
	// Zero value means no safety wrapper — raw SpawnRunner is used directly.
	SubagentRunnerConfig toolssubagent.SubagentRunnerConfig

	// ToolProfile restricts the available tools to a named profile.
	// Supported values: "minimal", "coding", "messaging", "full" (default: "full").
	ToolProfile string

	// WorkDir is the default working directory used for external agent
	// delegation tasks (external_agent tool). May be empty.
	WorkDir string

	// ParentMessages returns the parent agent's current conversation messages,
	// used by "state" and "full" context modes for sessions_spawn. May be nil
	// (set later via SetParentMessages for late binding after the parent agent
	// is fully constructed).
	ParentMessages func() []agentcore.Message

	// PhaseTransitioner is called when exit_plan_mode is invoked, transitioning
	// the agent from Plan to Act execution phase. May be nil if Plan/Act mode
	// is not used.
	PhaseTransitioner toolsplanning.PhaseTransitioner
}

// NewExtension creates a new agent tools extension.
func NewExtension(cfg ExtensionConfig) *Extension {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))
	}

	// Build SubagentRunner if config is non-zero
	var subagent *toolssubagent.SubagentRunner
	if cfg.SubagentRunnerConfig.DefaultTimeout > 0 ||
		cfg.SubagentRunnerConfig.HeartbeatFn != nil ||
		cfg.SubagentRunnerConfig.ProgressCallback != nil {
		subagentCfg := cfg.SubagentRunnerConfig
		if subagentCfg.Logger == nil {
			subagentCfg.Logger = logger
		}
		if subagentCfg.HomeDir == "" {
			subagentCfg.HomeDir = cfg.HomeDir
		}
		subagent = toolssubagent.NewSubagentRunner(subagentCfg)
	}

	// FTS searcher for session search
	ftsSearcher, _ := toolssessions.NewFTSSearcher(cfg.SessionsDir)

	// Persistent inbox store for cross-session async notifications.
	// Opens <homeDir>/inbox.db; non-fatal if it fails (tools will return errors
	// at call time, but agent construction proceeds).
	inboxStore, inboxErr := inbox.NewStore(cfg.HomeDir, 0)
	if inboxErr != nil {
		logger.Warn("inbox store unavailable; inbox tools will error", "err", inboxErr)
	}

	// Persistent audit log store for tool calls and lifecycle events.
	// Opens <homeDir>/audit.db; non-fatal if it fails.
	auditStore, auditErr := audit.NewStore(cfg.HomeDir)
	if auditErr != nil {
		logger.Warn("audit store unavailable; audit tools will error", "err", auditErr)
	}

	// Persistent subagent store for crash recovery and orphan detection.
	// Opens <homeDir>/subagents.db; non-fatal if it fails.
	subagentStore, subStoreErr := toolssubagent.NewSubagentStore(cfg.HomeDir)
	if subStoreErr != nil {
		logger.Warn("subagent store unavailable; persistence disabled", "err", subStoreErr)
	}

	compr := toolscontext.NewContextCompressor(200000, 50)
	compr.FragmentLimiter = toolscontext.NewFragmentTokenLimiter()

	ext := &Extension{
		sessionsDir:     cfg.SessionsDir,
		homeDir:         cfg.HomeDir,
		todoStore:       toolsplanning.NewTodoStore(),
		planStore:       toolsplanning.NewPlanStore(),
		cronStore:       NewCronStore(cfg.HomeDir),
		spawnStore:      toolssubagent.NewSpawnStore(),
		mcpStore:        NewMCPStore(),
		processStore:    toolssandbox.NewProcessStore(),
		goalStoreSQL:    nil,
		sessionIDFn:     func() string { return "" },
		memoryStore:     toolsmemory.NewEnhancedMemoryStore(),
		vectorMem:       toolsmemory.NewVectorMem(cfg.HomeDir),
		subagentReg:     toolssubagent.NewSubagentRegistry(),
		feishuClient:    toolsfeishu.NewFeishuClient(),
		ftsSearcher:     ftsSearcher,
		compressor:      compr,
		hooks:           hook.NewHookRegistry(),
		dreamCycle:      toolsmemory.NewDreamCycle(),
		forkGuard:       toolssubagent.NewForkGuard(3),
		fragLimiter:     toolscontext.NewFragmentTokenLimiter(),
		metrics:         newToolMetrics(),
		fileState:       NewFileStateRegistry(),
		eventBus:        hook.NewEventBus(64),
		permDeferred:    NewPermissionDeferred(),
		checkpointW:     NewCheckpointWriter(cfg.HomeDir + "/checkpoints"),
		sessionFork:     toolssessions.NewSessionFork(),
		runnerMutex:     toolssessions.NewRunnerMutex(),
		worktreeMgr:     toolsworktree.NewWorktreeManager(cfg.HomeDir),
		swarmBoard:      toolswarm.NewSwarmBoard(),
		swarmOrch:       nil,
		secretsStore:    NewSecretsStore(cfg.HomeDir),
		standingOrders:  toolsstandingorders.NewStandingOrdersStore(cfg.HomeDir),
		kanbanManager:   kanban.NewKanbanManager(cfg.HomeDir),
		commitmentStore: toolscommitments.NewCommitmentStore(cfg.HomeDir),
		inboxStore:      inboxStore,
		auditStore:      auditStore,
		subagentStore:   subagentStore,
		spawnRunner:     cfg.SpawnRunner,
		subagentRunner:  subagent,
		handoffCallback: cfg.HandoffCallback,
		askUserCallback: cfg.AskUserCallback,
		sandbox:         cfg.Sandbox,
		logger:          logger,
		toolProfile:     cfg.ToolProfile,
		parentMessages:  cfg.ParentMessages,
		phaseTransition: cfg.PhaseTransitioner,
		externalAgents:  externalagent.NewRegistry(cfg.WorkDir, os.Getenv("COVO_EXTERNAL_AGENTS")),
	}
	// Wire registry into subagent runner
	if ext.subagentRunner != nil {
		ext.subagentRunner.SetRegistry(ext.subagentReg)
	}
	// Wire persistent subagent store (crash recovery + orphan detection).
	// The parentSessionFn is updated when SetGoalStore provides the real
	// session ID provider; until then it returns "" (metadata only).
	if ext.subagentStore != nil {
		ext.subagentReg.SetStore(ext.subagentStore, func() string {
			if ext.sessionIDFn != nil {
				return ext.sessionIDFn()
			}
			return ""
		})
		// Recover orphans from previous process run (non-fatal).
		if orphaned, err := ext.subagentReg.RecoverOrphaned(); err == nil && len(orphaned) > 0 {
			logger.Warn("recovered orphaned subagents from previous run", "count", len(orphaned))
		}
	}
	// Expose hooks for agent loop integration
	ext.Hooks = ext.hooks
	return ext
}

func (e *Extension) Name() string { return "agent-tools" }

// SetHandoffCallback replaces the handoff callback (used by human_handoff tool).
func (e *Extension) SetHandoffCallback(cb HandoffCallback) {
	e.handoffCallback = cb
}

// SetAskUserCallback replaces the structured question callback (used by the
// ask_user tool). Nil disables interactive questions, so ask_user falls back
// to its default answer (or fails without one).
func (e *Extension) SetAskUserCallback(cb AskUserFunc) {
	e.askUserCallback = cb
}

// SetGoalStore sets the SQLite-backed goal store and session ID provider
// for LLM-accessible goal tools, connecting them to the same store used
// by the judge-based stop gate.
func (e *Extension) SetGoalStore(gs *goal.Store, sessionIDFn func() string) {
	e.goalStoreSQL = gs
	e.sessionIDFn = sessionIDFn
}

// SetParentMessages wires the parent agent's message provider, used by
// "state" and "full" context modes in sessions_spawn / sessions_spawn_batch.
// Late-bound: the parent agent's core may not exist when the extension is
// first constructed, so this is called after the parent agent is ready.
func (e *Extension) SetParentMessages(fn func() []agentcore.Message) {
	e.parentMessages = fn
}

// SetPhaseTransitioner sets the callback invoked when exit_plan_mode is
// approved. This must be called before the extension's Init method runs
// (i.e., before the agent starts) for the transitioner to take effect.
func (e *Extension) SetPhaseTransitioner(fn toolsplanning.PhaseTransitioner) {
	e.phaseTransition = fn
}

// SetInboxStore replaces the default inbox store with an externally-provided
// one (e.g. reusing the session store's *sql.DB to co-locate tables).
func (e *Extension) SetInboxStore(s *inbox.Store) {
	if e.inboxStore != nil {
		_ = e.inboxStore.Close()
	}
	e.inboxStore = s
}

// InboxStore returns the inbox store for agent-layer integration (e.g.
// auto-drain on turn start). May be nil if construction failed.
func (e *Extension) InboxStore() *inbox.Store { return e.inboxStore }

// AuditStore returns the audit log store for agent-layer integration
// (e.g. the audit lifecycle hook). May be nil if construction failed.
func (e *Extension) AuditStore() *audit.Store { return e.auditStore }

// SubagentStore returns the persistent subagent store for crash recovery
// and orphan detection. May be nil if construction failed.
func (e *Extension) SubagentStore() *toolssubagent.SubagentStore { return e.subagentStore }

// SetAuditStore replaces the default audit store with an externally-provided one.
func (e *Extension) SetAuditStore(s *audit.Store) {
	if e.auditStore != nil {
		_ = e.auditStore.Close()
	}
	e.auditStore = s
}

// EventBus returns the event bus for plugin/event subscription. Activated
// (published to) by the agent-layer audit hook and tool lifecycle hooks.
func (e *Extension) EventBus() *hook.EventBus { return e.eventBus }

// PublishEvent is a convenience wrapper for publishing events on the
// extension's event bus. Safe to call even if the event bus is nil.
func (e *Extension) PublishEvent(et hook.EventType, data any) {
	if e.eventBus != nil {
		e.eventBus.Publish(et, data)
	}
}

func (e *Extension) Init(_ context.Context, agent *agentcore.Agent) error {
	// Load persisted cron jobs
	_ = e.cronStore.Load()

	agentCfg := agent.Config()
	provider := agentCfg.Provider
	model := agentCfg.Model

	// Wire spill → FTS indexer so spills become session_search results.
	if e.ftsSearcher != nil {
		SetSpillIndexer(func(sessionID, name, purpose, content, spillPath string) error {
			const previewLen = 2000
			return e.ftsSearcher.IndexSpill(sessionID, name, purpose, content, spillPath, previewLen)
		})
	}

	e.swarmOrch = toolswarm.NewSwarmOrchestrator(e.spawnRunner, e.subagentRunner, func() []string {
		return agent.ToolNames()
	})
	// Wire journal for crash-recovery resume of orchestration plans.
	e.swarmOrch.SetJournal(toolswarm.NewWorkflowJournal(filepath.Join(e.homeDir, "workflows")))

	e.tools = []*agentcore.Tool{
		// Batch 1: session_search, todo, clarify, send_message, moa
		toolssessions.BuildSessionSearchTool(e.sessionsDir, e.ftsSearcher),
		toolsplanning.BuildTodoTool(e.todoStore),
		toolsplanning.BuildClarifyTool(),
		buildSendMessageTool(),
		buildMoATool(MoAConfig{Provider: provider, DefaultModel: model}),
		// Batch 2: cronjob, tts, image_generate
		buildCronjobTool(e.cronStore),
		toolsmedia.BuildTtsTool(),
		toolsmedia.BuildImageGenerateTool(),
		// Batch 3: pdf, update_plan, exit_plan_mode, video_generate, music_generate, transcribe
		toolsmedia.BuildPdfTool(),
		toolsplanning.BuildUpdatePlanTool(e.planStore),
		toolsplanning.BuildExitPlanModeTool(e.planStore, e.phaseTransition),
		toolsmedia.BuildVideoGenerateTool(),
		toolsmedia.BuildMusicGenerateTool(),
		toolsmedia.BuildTranscribeTool(),
		// Voice interaction: wake word listening + push-to-talk
		toolsmedia.BuildVoiceListenTool(),
		toolsmedia.BuildVoiceRecordTool(),
		// Batch 4: diffs, sessions_spawn, sessions_spawn_batch, edit_block
		fileops.BuildDiffsTool(),
		toolssubagent.BuildSessionsSpawnTool(e.spawnRunner, e.subagentRunner, func() []string {
			return agent.ToolNames()
		}, func() []agentcore.Message {
			if e.parentMessages == nil {
				return nil
			}
			return e.parentMessages()
		}),
		toolssubagent.BuildSessionsSpawnBatchTool(e.spawnRunner, e.subagentRunner, func() []string {
			return agent.ToolNames()
		}, func() []agentcore.Message {
			if e.parentMessages == nil {
				return nil
			}
			return e.parentMessages()
		}),
		fileops.BuildEditBlockTool(),
		// Batch 5: mcp
		buildMCPTool(e.mcpStore),
		// Batch 6: process, monitor, sandbox, swarm, swarm_orchestrate, secrets
		toolssandbox.BuildProcessTool(e.processStore),
		toolssandbox.BuildMonitorTool(e.processStore),
		toolssandbox.BuildSandboxTool(e.sandbox),
		toolswarm.BuildSwarmTool(e.swarmBoard),
		toolswarm.BuildSwarmOrchestrateTool(e.swarmOrch),
		buildSecretsTool(e.secretsStore),
		// Batch 7: tool_search, tool_describe, tool_call
		buildToolSearchTool(func() []agentcore.ToolDefinition {
			// Rebuild definitions from current registry state
			names := agent.ToolNames()
			defs := make([]agentcore.ToolDefinition, 0, len(names))
			for _, name := range names {
				if tool, ok := agent.GetTool(name); ok {
					defs = append(defs, agentcore.ToolDefinition{
						Name:        tool.Name,
						Description: tool.Description,
						Parameters:  tool.Parameters,
					})
				}
			}
			return defs
		}),
		buildToolDescribeTool(func() []agentcore.ToolDefinition {
			names := agent.ToolNames()
			defs := make([]agentcore.ToolDefinition, 0, len(names))
			for _, name := range names {
				if tool, ok := agent.GetTool(name); ok {
					defs = append(defs, agentcore.ToolDefinition{
						Name:        tool.Name,
						Description: tool.Description,
						Parameters:  tool.Parameters,
					})
				}
			}
			return defs
		}),
		buildToolCallTool(agent),
		// Batch 8: apply_patch
		fileops.BuildApplyPatchTool(),
		// Batch 9: human_handoff
		buildHumanHandoffTool(e.handoffCallback),
		// Batch 9b: ask_user (structured questions with options + default)
		buildAskUserTool(e.askUserCallback),
		// Batch 9c: spill (explicit result offload with lineage) + spill_list
		BuildSpillTool(e.homeDir, func() string {
			if e.sessionIDFn != nil {
				return e.sessionIDFn()
			}
			return ""
		}),
		BuildSpillListTool(e.homeDir, func() string {
			if e.sessionIDFn != nil {
				return e.sessionIDFn()
			}
			return ""
		}),
		// Batch 10: llm_task
		buildLLMTaskTool(provider, model),
		// Batch 11: kanban
		kanban.BuildKanbanTool(e.kanbanManager),

		// Batch 12: goal tracking (create_goal, get_goal, update_goal)
		toolsplanning.BuildCreateGoalTool(e.goalStoreSQL, e.sessionIDFn),
		toolsplanning.BuildGetGoalTool(e.goalStoreSQL, e.sessionIDFn),
		toolsplanning.BuildUpdateGoalTool(e.goalStoreSQL, e.sessionIDFn),

		// Batch 12b: persistent inbox (inbox_send, inbox_check) + audit log
		buildInboxSendTool(e.inboxStore, e.sessionIDFn),
		buildInboxCheckTool(e.inboxStore, e.sessionIDFn),
		buildAuditQueryTool(e.auditStore, e.sessionIDFn),

		// Batch 13: session tools (sessions_yield, sessions_send)
		toolssessions.BuildSessionsYieldTool(nil), // yieldFn wired post-init
		toolssessions.BuildSessionsSendTool(nil),  // sendFn wired post-init

		// Batch 14: memory enhancement (memory_recall, memory_store, memory_forget)
		toolsmemory.BuildMemoryRecallTool(e.memoryStore),
		toolsmemory.BuildMemoryStoreTool(e.memoryStore),
		toolsmemory.BuildMemoryForgetTool(e.memoryStore),

		// Batch 14b: semantic vector memory
		toolsmemory.BuildMemorySemanticSearchTool(e.vectorMem),
		toolsmemory.BuildMemorySemanticStoreTool(e.vectorMem),
		toolsmemory.BuildMemorySemanticForgetTool(e.vectorMem),
		toolsmemory.BuildMemorySemanticStatsTool(e.vectorMem),

		// Batch 15: subagent introspection
		toolssubagent.BuildSubagentsTool(e.subagentReg),

		// Batch 16: feishu/lark integration (doc, wiki, bitable, drive)
		toolsfeishu.BuildFeishuDocReadTool(e.feishuClient),
		toolsfeishu.BuildFeishuDocCreateTool(e.feishuClient),
		toolsfeishu.BuildFeishuWikiListTool(e.feishuClient),
		toolsfeishu.BuildFeishuWikiNodesTool(e.feishuClient),
		toolsfeishu.BuildFeishuBitableListTool(e.feishuClient),
		toolsfeishu.BuildFeishuBitableRecordsTool(e.feishuClient),
		toolsfeishu.BuildFeishuDriveListTool(e.feishuClient),
		toolsfeishu.BuildFeishuDriveDownloadTool(e.feishuClient),

		// Batch 17: canvas (HTML/SVG/Mermaid/PlantUML visualization)
		toolscanvas.BuildCanvasTool(),
		toolscanvas.BuildLiveCanvasTool(),
		toolscanvas.BuildStopCanvasTool(),

		// Batch 18: remote_exec + audit_read + deploy + structured_output + parse_duration
		toolssandbox.BuildRemoteExecTool(),
		toolssandbox.BuildAuditReadTool(),
		buildDeployTool(),
		buildStructuredOutputTool(),
		buildParseDurationTool(),

		// Batch 19: context compression
		toolscontext.BuildContextCompressTool(e.compressor),
		toolscontext.BuildContextCompressConfigTool(e.compressor),
		buildWebFetchTool(),
		buildReactionTool(),

		// append_file
		fileops.BuildAppendFileTool(),

		// Batch 20: dashboard + workflow + fork + dream memory + worktree + computer_use
		toolssubagent.BuildDashboardTool(e.subagentReg),
		toolssubagent.BuildWorkflowTool(e.spawnRunner, e.subagentReg),
		toolssubagent.BuildAdvancedWorkflowTool(e.spawnRunner, e.subagentReg, e.homeDir),
		toolssubagent.BuildSessionsForkTool(e.spawnRunner, e.subagentReg, e.forkGuard),
		toolsmemory.BuildMemoryExtractTool(e.dreamCycle),
		toolsmemory.BuildMemoryDreamTool(e.dreamCycle),
		toolsmemory.BuildMemoryDreamStatsTool(e.dreamCycle),
		toolsworktree.BuildEnterWorktreeTool(e.worktreeMgr),
		toolsworktree.BuildExitWorktreeTool(e.worktreeMgr),
		buildComputerUseTool(),

		// Batch 21: hardware (i2c, spi, serial)
		toolshardware.BuildI2CTool(),
		toolshardware.BuildSPITool(),
		toolshardware.BuildSerialTool(),
		toolssubagent.BuildInterruptAgentTool(e.subagentReg),
		toolssubagent.BuildSessionsWaitTool(e.subagentReg),
		toolssubagent.BuildCloseAgentTool(e.subagentReg),
		toolssubagent.BuildSendInputTool(e.subagentReg),

		// Batch 22: tmux integration
		toolssandbox.BuildTmuxTool(),

		// Batch 23: test generation
		buildGenerateTestsTool(),

		// Batch 24: standing orders
		toolsstandingorders.BuildStandingOrdersTool(e.standingOrders),

		// Batch 25: skill workshop
		BuildSkillWorkshopTool(e.workshopStore, e.skillMgr),

		// Batch 26: unified file search (combines glob + grep)
		fileops.BuildSearchFilesTool(),

		// Batch 27: code graph (Go package dependency analysis)
		toolscodegraph.BuildCodeGraphTool(),

		// Batch 29: run_code (Code Mode — execute Go programs with tool SDK)
		BuildRunCodeToolWithRegistry(func(name string) (*agentcore.Tool, bool) {
			return agent.GetTool(name)
		}, func() []string {
			return agent.ToolNames()
		}),
	}

	// Batch 28: external coding agents (Claude Code, Codex, opencode).
	// Only exposed when COVO_EXTERNAL_AGENTS enables at least one provider.
	if e.externalAgents != nil && e.externalAgents.AnyEnabled() {
		e.tools = append(e.tools, externalagent.BuildExternalAgentTool(e.externalAgents))
	}

	// Apply tool profile filter if not "full"
	if e.toolProfile != "" && e.toolProfile != "full" {
		e.tools = filterToolsByProfile(e.tools, e.toolProfile)
	}

	agent.RegisterTools(e.tools...)
	return nil
}

func (e *Extension) Dispose() error {
	if e.ftsSearcher != nil {
		e.ftsSearcher.Close()
	}
	if e.auditStore != nil {
		_ = e.auditStore.Close()
	}
	if e.inboxStore != nil {
		_ = e.inboxStore.Close()
	}
	if e.subagentStore != nil {
		_ = e.subagentStore.Close()
	}
	return nil
}

func (e *Extension) Tools() []*agentcore.Tool { return e.tools }

// TodoStore returns the shared todo store for prompt injection.
func (e *Extension) TodoStore() *toolsplanning.TodoStore {
	return e.todoStore
}

func (e *Extension) CommitmentStore() *toolscommitments.CommitmentStore {
	return e.commitmentStore
}

// SetStandingOrdersStore replaces the default standing orders store.
func (e *Extension) SetStandingOrdersStore(s *toolsstandingorders.StandingOrdersStore) {
	e.standingOrders = s
}

// StandingOrdersStore returns the standing orders store.
func (e *Extension) StandingOrdersStore() *toolsstandingorders.StandingOrdersStore {
	return e.standingOrders
}

// SetWorkshopStore sets the Skill Workshop proposal store.
func (e *Extension) SetWorkshopStore(s *evolution.WorkshopStore) {
	e.workshopStore = s
}

// SetSkillManager sets the skill manager for applying workshop proposals.
func (e *Extension) SetSkillManager(sm *evolution.SkillManager) {
	e.skillMgr = sm
}

func (e *Extension) WorktreeManager() *toolsworktree.WorktreeManager {
	return e.worktreeMgr
}

// PlanStore returns the shared plan store for prompt injection.
func (e *Extension) PlanStore() *toolsplanning.PlanStore {
	return e.planStore
}

// CronStore returns the shared cron store for the scheduler.
func (e *Extension) CronStore() *CronStore {
	return e.cronStore
}

// SpawnStore returns the shared spawn store for tracking child sessions.
func (e *Extension) SpawnStore() *toolssubagent.SpawnStore {
	return e.spawnStore
}

// KanbanManager returns the kanban board manager for the agent.
func (e *Extension) KanbanManager() *kanban.KanbanManager {
	return e.kanbanManager
}

// AutoConnectMCPServers reads MCP server configs from a map and connects
// to each one. Logs warnings on failure but does not return an error.
func (e *Extension) AutoConnectMCPServers(ctx context.Context, servers map[string]MCPConfig) {
	for name, srv := range servers {
		if srv.Command == "" {
			continue
		}
		n, err := e.mcpStore.AutoConnect(ctx, name, srv.Command, srv.Args, srv.Env, srv.Timeout)
		if err != nil {
			e.logger.Warn("MCP auto-connect failed", "name", name, "err", err)
		} else {
			e.logger.Info("MCP auto-connect succeeded", "name", name, "tools", n)
		}
	}
}

// NewCronScheduler creates a cron scheduler using the extension's store.
func (e *Extension) NewCronScheduler(runner CronRunner) *CronScheduler {
	return NewCronScheduler(e.cronStore, runner)
}

// MCPConfig represents an MCP server configuration.
type MCPConfig struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args,omitempty"`
	Env     []string `yaml:"env,omitempty"` // env var names to pass through (resolved from os.Getenv at launch)
	Timeout int      `yaml:"timeout,omitempty"`
}
