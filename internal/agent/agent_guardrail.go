package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/covoyage/covo-agent/internal/agent/approval"
	"github.com/covoyage/covo-agent/internal/agent/safety"
	"github.com/covoyage/covonaut/agentcore"
)

func (ca *CovoAgent) CheckWriteSafe(path string) error {
	return safety.IsWriteSafeWithWorkspace(path, ca.homeDir, ca.workDir, ca.workspaceOnly)
}

func (ca *CovoAgent) CheckReadSafe(path string) error {
	return safety.IsPathSafeWithWorkspace(path, ca.homeDir, ca.workDir, ca.workspaceOnly)
}

func (ca *CovoAgent) ToolGuardrail() *ToolGuardrail {
	return ca.toolGuardrail
}

// newGuardrailLifecycleHook resets per-turn guardrail state at the start of each turn.
func newGuardrailLifecycleHook(ca *CovoAgent) agentcore.LifecycleHook {
	return &guardrailLifecycleHook{ca: ca}
}

type guardrailLifecycleHook struct {
	agentcore.BaseLifecycleHook
	ca *CovoAgent
}

func (h *guardrailLifecycleHook) BeforeTurn(_ context.Context, _ *agentcore.AgentRunContext) error {
	h.ca.toolGuardrail.NewTurn()
	return nil
}

// --- Approval system ---

func initApprovalSystem(cfg CovoAgentConfig) *approval.System {
	approvalCfg := approval.Config{
		Mode:       "manual",
		StorageDir: cfg.HomeDir,
		YoloMode:   envBool("COVO_YOLO", false),
		Logger:     cfg.Logger,
	}
	if cfg.ApprovalCfg != nil {
		if cfg.ApprovalCfg.Mode != "" {
			approvalCfg.Mode = cfg.ApprovalCfg.Mode
		}
		if cfg.ApprovalCfg.YoloMode {
			approvalCfg.YoloMode = true
		}
		if cfg.ApprovalCfg.StorageDir != "" {
			approvalCfg.StorageDir = cfg.ApprovalCfg.StorageDir
		}
	}
	sys := approval.NewSystem(approvalCfg)
	approval.LoadPolicyFromDirs(cfg.HomeDir, cfg.WorkingDir)
	return sys
}

// wireSmartApproval connects the LLM provider for smart approval mode.
func (ca *CovoAgent) wireSmartApproval() {
	if ca.approvalSystem == nil {
		return
	}
	// Register smart approval function using the main provider
	ca.approvalSystem.SetSmartApprovalFn(func(ctx context.Context, command, description string) (string, error) {
		if ca.cfg.Provider == nil {
			return "escalate", nil
		}
		prompt := fmt.Sprintf(
			"You are a security reviewer for a terminal AI agent. Assess this command:\n\n"+
				"Command: %s\n"+
				"Flagged reason: %s\n\n"+
				"Rules:\n"+
				"- APPROVE if clearly safe (benign scripts, safe file ops, dev tools, package installs, git ops)\n"+
				"- DENY if genuinely dangerous (recursive delete of important paths, overwriting system files, fork bombs, wiping disks, dropping databases)\n"+
				"- ESCALATE if uncertain\n\n"+
				"Respond with exactly one word: APPROVE, DENY, or ESCALATE", command, description)

		req := &agentcore.ProviderRequest{
			Model: ca.cfg.Model,
			Messages: []agentcore.Message{
				{Role: agentcore.RoleUser, Content: prompt},
			},
			Temperature: 0,
			MaxTokens:   16,
		}
		resp, err := ca.cfg.Provider.Complete(ctx, req)
		if err != nil {
			return "escalate", err
		}
		answer := strings.TrimSpace(strings.ToUpper(resp.Content))
		if answer == "APPROVE" {
			return "approve", nil
		}
		if answer == "DENY" {
			return "deny", nil
		}
		return "escalate", nil
	})
}

// ApprovalSystem returns the approval subsystem.
func (ca *CovoAgent) ApprovalSystem() *approval.System {
	return ca.approvalSystem
}

// isYolo returns true when YOLO mode is active (either global or session-scoped).
func (ca *CovoAgent) isYolo() bool {
	if ca.approvalSystem == nil {
		return false
	}
	return ca.approvalSystem.IsYolo()
}

// PendingApprovalPattern returns the description of the last dangerous pattern detected.
func (ca *CovoAgent) PendingApprovalPattern() string {
	p := ca.pendingPattern
	ca.pendingPattern = "" // consume once
	return p
}

func (ca *CovoAgent) ShellHooks() *ShellHookManager {
	return ca.shellHooks
}
