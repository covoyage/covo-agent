package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// inboxDrainHook drains the persistent inbox at the start of each agent run
// and injects pending messages as a system note, so the agent sees
// asynchronous notifications from sub-agents or other sessions without
// needing to call inbox_check explicitly.
//
// Implements a fork-and-forget wake-up: a background sub-agent that
// completed while the parent was idle can drop a message in the inbox; on
// the parent's next run, the message is auto-injected.
type inboxDrainHook struct {
	agentcore.BaseLifecycleHook
	ca *CovoAgent
}

func newInboxDrainHook(ca *CovoAgent) *inboxDrainHook {
	return &inboxDrainHook{ca: ca}
}

// BeforeAgentRun drains the inbox and injects any pending messages.
func (h *inboxDrainHook) BeforeAgentRun(ctx context.Context, arc *agentcore.AgentRunContext) error {
	if h.ca.agentTools == nil {
		return nil
	}
	store := h.ca.agentTools.InboxStore()
	if store == nil {
		return nil
	}
	sessionID := h.ca.SessionManager().CurrentID()
	if sessionID == "" {
		return nil
	}
	msgs, err := store.Drain(sessionID)
	if err != nil {
		// Non-fatal: log via agent state but don't block the run.
		return nil
	}
	if len(msgs) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("[Inbox] You have %d pending asynchronous message(s) from sub-agents or other sessions:\n\n", len(msgs)))
	for i, m := range msgs {
		from := m.SenderSession
		if from == "" {
			from = "unknown"
		}
		b.WriteString(fmt.Sprintf("%d. [from %s, %s]\n   %s\n\n",
			i+1, from, m.CreatedAt.Format(time.RFC3339), m.Message))
	}
	b.WriteString("Review these messages and incorporate any relevant results into your current task.")

	h.ca.core.State().AddMessage(agentcore.Message{
		Role:    agentcore.RoleSystem,
		Content: b.String(),
	})
	return nil
}
