package commands

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/covoyage/covo-agent/internal/agent"
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

// submitPrompt dispatches a plain prompt to the agent, handling busy-mode
// (interrupt/queue/steer/block) and context preprocessing first.
func (s *interactiveSession) submitPrompt(ctx context.Context, trimmed string) {
	if s.busy.Load() {
		switch shared.RuntimeState.BusyInputMode() {
		case "interrupt":
			if cf := s.cancelRun.Load(); cf != nil && *cf != nil {
				(*cf)()
				loadUIBus().PrintSystem(i18n.T("system.interrupted"))
			}
		case "queue":
			shared.RuntimeState.SetPendingInput(trimmed)
			loadUIBus().PrintSystem(i18n.T("system.queued"))
		case "steer":
			// Steering: inject as follow-up message
			loadUIBus().PrintSystem(i18n.T("system.steer_unsupported"))
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
		defer s.busy.Store(false)
		defer func() { s.cancelRun.Store(nil) }()
		ag.Run(ctx, trimmed)

		// Process queued input if any
		if queued := shared.RuntimeState.TakePendingInput(); queued != "" {
			if s.app != nil {
				loadUIBus().PrintSystem(i18n.T("system.processing_queued", "text", shared.Truncate(queued, 60)))
			}
			s.handleSubmit(ctx, queued)
		}
	}, nil)
}
