package commands

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/agent"
	runtimeapp "github.com/covoyage/covo-agent/internal/app"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covo-agent/internal/slashcmd"
)

// handleSubmit is the single entry point for all user input in the TUI:
// pending approval responses, shell escapes, slash commands, and plain
// prompts (which run on a background goroutine).
func (s *interactiveSession) handleSubmit(ctx context.Context, input string) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return
	}

	s.dispatchInput(ctx, trimmed)
}

func (s *interactiveSession) handleQueue(ctx context.Context, input string) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return
	}
	s.enqueuePrompt(trimmed, false)
}

func (s *interactiveSession) dispatchInput(ctx context.Context, trimmed string) {

	// Handle pending approval via text input (y/s/a/n keys).
	if s.permissionGate != nil && s.permissionGate.HasPending() {
		lower := strings.ToLower(trimmed)
		switch lower {
		case "y", "yes", "o", "once":
			s.permissionGate.Respond(ChoiceOnce)
		case "s", "session":
			s.permissionGate.Respond(ChoiceSession)
		case "a", "always":
			s.permissionGate.Respond(ChoiceAlways)
		case "n", "no", "d", "deny":
			s.permissionGate.Respond(ChoiceDeny)
		}
		return
	}

	if strings.HasPrefix(trimmed, "!!") && trimmed != "!!" {
		cmdStr := strings.TrimSpace(trimmed[2:])
		if cmdStr != "" {
			executeShellCommand(ctx, cmdStr, s.workingDir, s.app, &s.busy)
		}
		return
	}

	if strings.HasPrefix(trimmed, bashModePrefix) && !strings.HasPrefix(trimmed, "!/") && trimmed != "!" {
		cmdStr := strings.TrimSpace(trimmed[1:])
		if cmdStr != "" {
			executeShellCommand(ctx, cmdStr, s.workingDir, s.app, &s.busy)
		}
		return
	}

	if strings.HasPrefix(trimmed, "/") {
		if s.handleSlashCommand(ctx, trimmed) {
			return
		}
	}

	s.submitPrompt(ctx, trimmed)
}

// handleSlashCommand processes "/"-prefixed input and reports whether the
// input was consumed. /yolo and /approve are handled inline because they
// touch per-session state; everything else is delegated to the slash command
// framework. Unhandled slash input falls through to the agent as plain text.
func (s *interactiveSession) handleSlashCommand(ctx context.Context, trimmed string) bool {
	// Handle /yolo inline since it needs per-session state
	if trimmed == "/yolo" || strings.HasPrefix(trimmed, "/yolo ") {
		nowYolo := shared.RuntimeState.ToggleSessionYolo()
		if ca := s.agentRuntime.Current(); ca != nil {
			if approvalSys := ca.ApprovalSystem(); approvalSys != nil {
				if nowYolo {
					approvalSys.EnableSessionYolo("cli")
				} else {
					approvalSys.DisableSessionYolo("cli")
				}
			}
		}
		if s.permissionGate != nil {
			s.permissionGate.YoloMode = nowYolo
		}
		if nowYolo {
			loadUIBus().PrintSystem(i18n.T("system.yolo_on"))
		} else {
			loadUIBus().PrintSystem(i18n.T("system.yolo_off"))
		}
		return true
	}

	// Handle /approve inline since it needs per-session permission gate state
	if trimmed == "/approve" || strings.HasPrefix(trimmed, "/approve ") {
		s.handleApproveCommand(trimmed)
		return true
	}
	if s.slashContext == nil {
		return true
	}
	handled := slashcmd.HandleSlashCommand(s.slashContext.Build(trimmed, ctx, s.providerType, s.model))
	if handled {
		return true
	}
	return false
}

// handleApproveCommand implements the /approve slash command.
func (s *interactiveSession) handleApproveCommand(trimmed string) {
	if s.permissionGate == nil {
		loadUIBus().PrintSystem("(approval gate not available)")
		return
	}
	parts := strings.Fields(trimmed)
	if len(parts) < 2 {
		loadUIBus().PrintSystem(s.permissionGate.FormatApprovalStatus())
		return
	}
	arg := strings.ToLower(parts[1])
	if arg == "all" {
		// Toggle all categories
		allOn := len(s.permissionGate.AutoApprovedCategories()) == len(categoryOrder)
		for _, cat := range categoryOrder {
			if allOn {
				// Turn all off
				if s.permissionGate.IsCategoryAutoApproved(cat) {
					s.permissionGate.ToggleCategoryAutoApprove(cat)
				}
			} else {
				// Turn all on
				if !s.permissionGate.IsCategoryAutoApproved(cat) {
					s.permissionGate.ToggleCategoryAutoApprove(cat)
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
	now := s.permissionGate.ToggleCategoryAutoApprove(cat)
	if now {
		loadUIBus().PrintSystem(fmt.Sprintf("✅ Auto-approve ON: %s", categoryLabel(cat)))
	} else {
		loadUIBus().PrintSystem(fmt.Sprintf("🔒 Manual approval: %s", categoryLabel(cat)))
	}
}

func busyInputMode() string {
	switch mode := shared.RuntimeState.BusyInputMode(); mode {
	case "interrupt", "queue", "steer", "collect", "block":
		return mode
	default:
		return "block"
	}
}

func reportQueueFull() {
	n, max := 0, 0
	if q := shared.RuntimeState.PromptQueue(); q != nil {
		n, max = q.Len(), q.MaxSize()
	}
	loadUIBus().PrintSystem(i18n.T("system.queue_full", "count", strconv.Itoa(n), "max", strconv.Itoa(max)))
}

func (s *interactiveSession) enqueuePrompt(text string, front bool) bool {
	var result runtimeapp.EnqueueResult
	if front {
		result = shared.RuntimeState.EnqueuePromptFront(text)
	} else {
		result = shared.RuntimeState.EnqueuePrompt(text)
	}
	if result.Full {
		reportQueueFull()
		return false
	}
	loadUIBus().PrintSystem(i18n.T("system.queued"))
	return true
}

func (s *interactiveSession) enqueueMerged(text string) bool {
	result := shared.RuntimeState.EnqueueMerged(text)
	if result.Full {
		reportQueueFull()
		return false
	}
	if result.Merged {
		loadUIBus().PrintSystem(i18n.T("system.queued_merged"))
		return true
	}
	loadUIBus().PrintSystem(i18n.T("system.queued"))
	return true
}

func (s *interactiveSession) setSideCancel(cancel context.CancelFunc) {
	if cancel == nil {
		s.cancelSide.Store(nil)
		return
	}
	s.cancelSide.Store(&cancel)
}

func (s *interactiveSession) cancelSideRun() {
	if cf := s.cancelSide.Load(); cf != nil && *cf != nil {
		(*cf)()
	}
	s.cancelSide.Store(nil)
}

func (s *interactiveSession) interruptCurrentRun() bool {
	cf := s.cancelRun.Swap(nil)
	if cf == nil || *cf == nil {
		return false
	}
	if s.agentRuntime != nil {
		if ag := s.agentRuntime.Core(); ag != nil {
			ag.ClearSteering()
		}
	}
	if s.app != nil {
		s.app.Interrupt()
	}
	(*cf)()
	return true
}

func (s *interactiveSession) steerCurrentRun(text string) bool {
	if s.agentRuntime == nil {
		loadUIBus().PrintSystem(i18n.T("system.no_active_agent"))
		return false
	}
	ag := s.agentRuntime.Core()
	if ag == nil {
		loadUIBus().PrintSystem(i18n.T("system.no_active_agent"))
		return false
	}
	ag.Steer(agentcore.Message{Role: agentcore.RoleUser, Content: text})
	loadUIBus().PrintSystem(i18n.T("system.steered", "text", shared.Truncate(text, 60)))
	return true
}

// onSubagentComplete wakes the parent conversation when a background subagent
// finishes: mid-run the result is steered into the current turn; when idle a
// turn is started directly so the result is never left polling in the inbox.
// Called from the spawn-completion goroutine — submitPrompt is already
// invoked off the UI thread for queued prompts after each run, so this
// follows existing practice.
func (s *interactiveSession) onSubagentComplete(childID, task, output string, err error) {
	if s.agentRuntime == nil {
		return
	}
	status := "completed"
	body := output
	if err != nil {
		status = "failed"
		body = err.Error()
	}
	text := fmt.Sprintf(
		"[subagent %s %s] task: %s\nresult:\n%s\n\nIncorporate this result into your work before concluding. If the parent task still has unfinished work, continue it now instead of waiting for further input.",
		childID, status, shared.Truncate(task, 200), shared.Truncate(body, 4000))

	if s.busy.Load() {
		if ag := s.agentRuntime.Core(); ag != nil {
			ag.Steer(agentcore.Message{Role: agentcore.RoleUser, Content: text})
			loadUIBus().PrintSystem(fmt.Sprintf("↩ subagent %s %s — steered into current run", shared.Truncate(childID, 12), status))
		}
		return
	}
	loadUIBus().PrintSystem(fmt.Sprintf("↩ subagent %s %s — waking session", shared.Truncate(childID, 12), status))
	s.submitPrompt(context.Background(), text)
}

// submitPrompt dispatches a plain prompt to the agent, handling busy-mode
// (interrupt/queue/steer/collect/block) and context preprocessing first.
func (s *interactiveSession) submitPrompt(ctx context.Context, trimmed string) {
	if s.busy.Load() {
		switch busyInputMode() {
		case "interrupt":
			if !s.enqueuePrompt(trimmed, true) {
				return
			}
			if s.interruptCurrentRun() {
				loadUIBus().PrintSystem(i18n.T("system.interrupt_next", "text", shared.Truncate(trimmed, 60)))
			}
		case "queue":
			s.enqueuePrompt(trimmed, false)
		case "collect":
			s.enqueueMerged(trimmed)
		case "steer":
			s.steerCurrentRun(trimmed)
		default: // "block"
			loadUIBus().PrintSystem(i18n.T("system.busy"))
		}
		return
	}

	ag := s.agentRuntime.Core()
	if ag == nil {
		newCA, err := s.replaceAgent(s.requestFor(s.mode, s.llm, s.providerType, s.model), false)
		if err != nil {
			log.Printf("replace agent: %v", err)
			return
		}
		ag = newCA.Core()
	}

	ctxLen := int64(128000)
	if s.modelContextLen > 0 {
		ctxLen = s.modelContextLen
	}
	if ca := s.agentRuntime.Current(); ca != nil {
		if ce := ca.Core().ContextEngine(); ce != nil {
			if cl := ce.ContextLength(); cl > 0 {
				ctxLen = cl
			}
		}
	}

	result := agent.PreprocessContextReferences(trimmed, s.workingDir, ctxLen)
	if len(result.Warnings) > 0 && s.app != nil {
		for _, w := range result.Warnings {
			loadUIBus().PrintSystem("⚠ " + w)
		}
	}
	if result.Blocked {
		return
	}
	trimmed = result.Message

	// Expand [image:name] placeholders to full file paths from pasted images.
	s.pendingImages.Range(func(key, value any) bool {
		name, _ := key.(string)
		path, _ := value.(string)
		if name != "" && path != "" {
			trimmed = strings.ReplaceAll(trimmed, "[image:"+name+"]", "[image: "+path+"]")
		}
		s.pendingImages.Delete(key)
		return true
	})

	ctx, cancel := context.WithCancel(ctx)
	s.cancelRun.Store(&cancel)
	s.busy.Store(true)
	safego.SafeGo(func() {
		ag.Run(ctx, trimmed)
		cancel()
		// Clear the interrupt handle before closing the busy window and
		// handing over to queued prompts, so the fresh turn below can
		// install its own cancel without this goroutine nil-ing it later.
		s.cancelRun.Store(nil)

		// Close the busy window BEFORE handing over to queued prompts. If
		// submitPrompt ran while busy was still set, the queued message
		// would hit the busy branch and be re-queued forever instead of
		// executing.
		s.busy.Store(false)

		if queued := shared.RuntimeState.TakePendingInput(); queued != "" {
			if s.app != nil {
				loadUIBus().PrintSystem(i18n.T("system.processing_queued", "text", shared.Truncate(queued, 60)))
			}
			// Submit as a fresh turn. WithoutCancel drops this run's
			// cancellation (user interrupt) while keeping context values, so
			// a prompt queued before an interrupt still runs afterwards.
			s.submitPrompt(context.WithoutCancel(ctx), queued)
		}
	}, nil)
}
