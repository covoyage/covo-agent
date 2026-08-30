package commands

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"github.com/covoyage/covo-agent/internal/diffrender"
	agentui "github.com/covoyage/covo-agent/internal/tui"
	agentpanels "github.com/covoyage/covo-agent/internal/tui/panels"
)

// wirePermissionGate creates the permission gate and attaches it to the
// initial agent, including the pre-edit diff approval flow.
func (s *interactiveSession) wirePermissionGate() {
	s.permissionGate = NewPermissionGate(s.app)
	s.permissionGate.YoloMode = shared.RuntimeState.SessionYolo()
	s.agent.SetPermissionChecker(s.permissionGate.Checker())

	// Wire pre-edit diff approval: when the agent attempts to modify files,
	// show a diff and ask the user to approve before the tool runs.
	// Uses the existing approval overlay for the UI.
	s.agent.SetPreEditDiffChecker(s.checkPreEditDiff)

	// Wire approval system to permission gate for session/permanent approvals
	if approvalSys := s.agent.ApprovalSystem(); approvalSys != nil {
		s.permissionGate.ApprovalSystem = shared.NewApprovalBridge(approvalSys)
	}
	s.permissionGate.PendingPatternProvider = s.agent.PendingApprovalPattern
}

// checkPreEditDiff shows the proposed edit and blocks until the user approves
// or rejects it.
func (s *interactiveSession) checkPreEditDiff(ctx context.Context, toolName, filePath, diffText string) bool {
	// Print the diff to the chat history for context, colorized for
	// readability (diff-semantic colors + token-level syntax highlighting
	// when COVO_SYNTAX_HIGHLIGHT is on and the language is recognized).
	loadUIBus().PrintSystem(fmt.Sprintf("── Proposed Edit: %s → %s ──", toolName, filepath.Base(filePath)))
	loadUIBus().PrintSystem(diffrender.Colorize(diffText, diffrender.SyntaxEnabled()))

	// Show the approval overlay.
	approved := make(chan bool, 1)
	s.permissionGate.showApprovalOverlay(
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
}

// wireApprovalOverlay anchors the approval picker overlay near the bottom of
// the screen so approval feels inline.
func (s *interactiveSession) wireApprovalOverlay() {
	s.permissionGate.showApprovalOverlay = func(prompt string, onChoose func(ApprovalChoice), onCancel func()) {
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
}

// handleHistoryJump opens the rewind dialog so the user can jump back to an
// earlier conversation turn (double-ESC on an empty, idle editor).
func (s *interactiveSession) handleHistoryJump() {
	ag := s.agentRuntime.Core()
	if ag == nil {
		return
	}
	ca := s.agentRuntime.Current()
	showRewindDialog(s.app,
		func() agentcore.StateSnapshot { return ag.State().Snapshot() },
		func(snap agentcore.StateSnapshot) {
			ag.State().Restore(snap)
			if ca == nil {
				return
			}
			sm := ca.SnapshotManager()
			if sm == nil || !sm.Enabled() {
				return
			}
			entry, ok := sm.FindClosest(len(snap.Messages))
			if !ok {
				return
			}
			if err := sm.Restore(entry.Hash); err == nil {
				s.app.PrintSystem("✅ Workspace restored to snapshot: " + entry.ToolName)
			}
		})
}

// wireHandoff routes human-handoff requests through the TUI instead of
// stderr/stdin.
func (s *interactiveSession) wireHandoff() {
	s.agent.SetHandoffCallback(s.handleHandoff)
}

func (s *interactiveSession) handleHandoff(ctx context.Context, message string) (string, error) {
	loadUIBus().PrintSystem("── HANDOFF ──")
	loadUIBus().PrintSystem(message)
	return agent.ReadStdinLine(ctx)
}

// wireAskUser routes ask_user tool questions through the TUI: an option
// picker when the model supplied options, otherwise an inline prompt. An
// unanswered or cancelled question falls back to the model-provided default.
func (s *interactiveSession) wireAskUser() {
	s.agent.SetAskUserCallback(s.handleAskUser)
}

func (s *interactiveSession) handleAskUser(ctx context.Context, question string, options []string, defaultValue string) (string, error) {
	fallback := func(err error) (string, error) {
		if defaultValue != "" {
			return defaultValue, nil
		}
		return "", err
	}

	host := loadUIBus().Host()
	if len(options) > 0 && host != nil {
		chosen := make(chan string, 1)
		var ov chat.OverlayRef
		picker := agentpanels.NewAskUserPicker(question, options,
			func(answer string) {
				chosen <- answer
				if ov != nil {
					host.RemoveOverlay(ov)
				}
				host.Focus(loadUIBus().Editor())
			},
			func() {
				chosen <- ""
				if ov != nil {
					host.RemoveOverlay(ov)
				}
				host.Focus(loadUIBus().Editor())
			},
		)
		ov = agentui.NewAnchoredOverlay(picker, 4, 50, 50, 50, 25)
		host.PushOverlay(ov)
		select {
		case answer := <-chosen:
			if strings.TrimSpace(answer) == "" {
				return fallback(fmt.Errorf("ask_user: no answer selected"))
			}
			return answer, nil
		case <-ctx.Done():
			return fallback(ctx.Err())
		}
	}

	loadUIBus().PrintSystem("── " + question + " ──")
	answer, err := agent.ReadStdinLine(ctx)
	if err != nil {
		return fallback(err)
	}
	if strings.TrimSpace(answer) == "" {
		return fallback(fmt.Errorf("ask_user: no answer provided"))
	}
	return answer, nil
}
