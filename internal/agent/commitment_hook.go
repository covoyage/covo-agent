package agent

import (
	"context"
	"fmt"

	"github.com/covoyage/covonaut/agentcore"
)

type commitmentHook struct {
	agentcore.BaseLifecycleHook
	ca *CovoAgent
}

func newCommitmentHook(ca *CovoAgent) *commitmentHook {
	return &commitmentHook{ca: ca}
}

// AfterTurn scans the last assistant message for inferred commitments.
func (h *commitmentHook) AfterTurn(ctx context.Context, arc *agentcore.AgentRunContext, info agentcore.TurnInfo) {
	if len(arc.Messages) == 0 {
		return
	}
	last := arc.Messages[len(arc.Messages)-1]
	if last.Role != agentcore.RoleAssistant {
		return
	}
	source := "session:" + h.ca.sessionMgr.CurrentID()
	h.ca.CommitmentStore().Detect(last.Content, source)
}

// AfterAgentRun prints a summary of pending commitments if any remain.
func (h *commitmentHook) AfterAgentRun(ctx context.Context, arc *agentcore.AgentRunContext, output string, err error) {
	n := h.ca.CommitmentStore().Count()
	if n > 0 && h.ca.baseCfg.Logger != nil {
		msg := fmt.Sprintf("Session ended with %d pending commitment(s). Use /commitments or `covo-agent commitments list` to review.", n)
		h.ca.baseCfg.Logger.Info(msg)
	}
}
