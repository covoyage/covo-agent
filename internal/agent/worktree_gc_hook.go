package agent

import (
	"context"

	"github.com/covoyage/covonaut/agentcore"
)

type worktreeGCHook struct {
	agentcore.BaseLifecycleHook
	ca *CovoAgent
}

func newWorktreeGCHook(ca *CovoAgent) *worktreeGCHook {
	return &worktreeGCHook{ca: ca}
}

func (h *worktreeGCHook) BeforeAgentRun(ctx context.Context, arc *agentcore.AgentRunContext) error {
	h.ca.WorktreeManager().PruneStale()
	return nil
}
