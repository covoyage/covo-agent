package agent

import (
	"context"

	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/covoyage/covo-agent/internal/goal"
	"github.com/covoyage/covo-agent/internal/hunk"
	"github.com/covoyage/covo-agent/internal/kanban"
	"github.com/covoyage/covo-agent/internal/session"
	agenttools "github.com/covoyage/covo-agent/internal/tools"
	toolscommitments "github.com/covoyage/covo-agent/internal/tools/commitments"
	toolsplanning "github.com/covoyage/covo-agent/internal/tools/planning"
	toolsstandingorders "github.com/covoyage/covo-agent/internal/tools/standingorders"
	toolsworktree "github.com/covoyage/covo-agent/internal/tools/worktree"
	"github.com/covoyage/covo-agent/internal/toolset"
	"github.com/covoyage/covonaut/agentcore"
)

func (ca *CovoAgent) Core() *agentcore.Agent {
	return ca.core
}

func (ca *CovoAgent) SetPermissionChecker(checker func(ctx context.Context, toolName string, args []byte) bool) {
	ca.permissionChecker = checker
}

// SetPreEditDiffChecker installs a callback that is invoked before
// file-mutating tools run. The callback receives a unified diff of the
// proposed changes and should return true to approve, false to reject.
// When nil (default), no pre-edit diff approval is performed.
func (ca *CovoAgent) SetPreEditDiffChecker(checker func(ctx context.Context, toolName, filePath, diffText string) bool) {
	ca.preEditDiffChecker = checker
}

// SetHandoffCallback replaces the human_handoff callback (e.g. for TUI mode).
func (ca *CovoAgent) SetHandoffCallback(cb agenttools.HandoffCallback) {
	ca.agentTools.SetHandoffCallback(cb)
}

// SetAskUserCallback replaces the ask_user callback (e.g. for TUI mode).
// When nil, ask_user falls back to its default answer, or fails if the model
// did not provide one — the right behaviour for headless/cron/oneshot runs.
func (ca *CovoAgent) SetAskUserCallback(cb agenttools.AskUserFunc) {
	ca.agentTools.SetAskUserCallback(cb)
}

func (ca *CovoAgent) Config() agentcore.Config {
	return ca.cfg
}

func (ca *CovoAgent) Mode() AgentMode {
	return ca.mode
}

func (ca *CovoAgent) Memory() *evolution.MemorySystem {
	return ca.memory
}

func (ca *CovoAgent) SkillManager() *evolution.SkillManager {
	return ca.skillMgr
}

func (ca *CovoAgent) SkillUsage() *evolution.SkillUsageTracker {
	return ca.skillUsage
}

// PersonaManager returns the Honcho-style user dialect modeling manager.
func (ca *CovoAgent) PersonaManager() *evolution.PersonaManager {
	return ca.personaMgr
}

func (ca *CovoAgent) Curator() *evolution.Curator {
	return ca.curator
}

// KanbanManager returns the kanban board manager.
func (ca *CovoAgent) KanbanManager() *kanban.KanbanManager {
	return ca.agentTools.KanbanManager()
}

// EnableCuratorExtraction enables automatic skill extraction from trajectories.
func (ca *CovoAgent) EnableCuratorExtraction() {
	if ca.curator != nil {
		ca.curator.SetSkillManager(ca.skillMgr)
	}
}

// EnableCuratorNudge enables periodic self-improvement nudges.
func (ca *CovoAgent) EnableCuratorNudge(cfg evolution.NudgeConfig) {
	if ca.curator != nil {
		ca.curator.SetNudgeSystem(cfg, ca.homeDir)
	}
}

func (ca *CovoAgent) PromptBuilder() *PromptBuilder {
	return ca.promptBuilder
}

// newAutoSaveHook creates a lifecycle hook that persists session state after each turn.
// This ensures sessions are always recoverable via message-level SQLite persistence.
func (ca *CovoAgent) newAutoSaveHook() agentcore.LifecycleHook {
	return &autoSaveHook{ca: ca}
}

type autoSaveHook struct {
	agentcore.BaseLifecycleHook
	ca *CovoAgent
}

func (h *autoSaveHook) AfterTurn(ctx context.Context, arc *agentcore.AgentRunContext, info agentcore.TurnInfo) {
	sessionID := h.ca.sessionMgr.CurrentID()
	if sessionID != "" && h.ca.core != nil {
		_ = h.ca.core.SaveState(ctx, sessionID)
	}
}

func (ca *CovoAgent) SessionManager() *session.Manager {
	return ca.sessionMgr
}

// GoalStore returns the persisted goal store.
func (ca *CovoAgent) GoalStore() *goal.Store {
	return ca.goalStore
}

// GoalAccounting returns the token/time budget tracker.
func (ca *CovoAgent) GoalAccounting() *goal.Accounting {
	return ca.goalAccounting
}

// GoalSteering returns the steering instruction generator.
func (ca *CovoAgent) GoalSteering() *goal.Steering {
	return ca.goalSteering
}

// HunkTracker returns the file change tracker with source attribution.
func (ca *CovoAgent) HunkTracker() *hunk.Tracker {
	return ca.hunkTracker
}

// GoalAccountingState returns the in-memory accounting state (for lifecycle integration).
func (ca *CovoAgent) GoalAccountingState() *goal.AccountingState {
	if ca.goalHook != nil {
		return ca.goalHook.AccountingState()
	}
	return nil
}

func (ca *CovoAgent) TodoStore() *toolsplanning.TodoStore {
	return ca.agentTools.TodoStore()
}

func (ca *CovoAgent) PlanStore() *toolsplanning.PlanStore {
	return ca.agentTools.PlanStore()
}

func (ca *CovoAgent) CommitmentStore() *toolscommitments.CommitmentStore {
	return ca.agentTools.CommitmentStore()
}

func (ca *CovoAgent) WorktreeManager() *toolsworktree.WorktreeManager {
	return ca.agentTools.WorktreeManager()
}

func (ca *CovoAgent) ToolsetFilter() *toolset.ToolsetFilter {
	return ca.toolsetFilter
}

// SnapshotManager returns the file-level snapshot manager, enabling /undo,
// revert-to-snapshot, and /rewind (chat + workspace rollback). May be nil or
// disabled if git is unavailable.
func (ca *CovoAgent) SnapshotManager() *SnapshotManager {
	return ca.snapshotMgr
}

func (ca *CovoAgent) ProviderName() string {
	return ca.providerName
}

func (ca *CovoAgent) Model() string {
	return ca.model
}

func (ca *CovoAgent) WorkDir() string {
	return ca.workDir
}

func (ca *CovoAgent) HomeDir() string {
	return ca.homeDir
}

func (ca *CovoAgent) StandingOrdersStore() *toolsstandingorders.StandingOrdersStore {
	return ca.standingOrders
}
