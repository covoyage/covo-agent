package rollout

import (
	"context"
	"log/slog"

	"github.com/covoyage/covonaut/agentcore"
)

// Hook implements agentcore.LifecycleHook and wires agent events to
// the Recorder for automatic rollout capture.
type Hook struct {
	agentcore.BaseLifecycleHook
	recorder *Recorder
	store    *Store
	logger   *slog.Logger
}

// NewHook creates a lifecycle hook that records agent events into a Rollout.
func NewHook(recorder *Recorder, store *Store, logger *slog.Logger) *Hook {
	return &Hook{
		recorder: recorder,
		store:    store,
		logger:   logger,
	}
}

func (h *Hook) BeforeTurn(ctx context.Context, arc *agentcore.AgentRunContext) error {
	h.recorder.BeginTurn()
	return nil
}

func (h *Hook) AfterTurn(ctx context.Context, arc *agentcore.AgentRunContext, info agentcore.TurnInfo) {
	h.recorder.CompleteTurn()
}

func (h *Hook) AfterToolExecution(ctx context.Context, arc *agentcore.AgentRunContext, tec *agentcore.ToolExecutionContext) {
	if tec == nil {
		return
	}
	h.recorder.RecordToolResultsBatch(tec.Results)
}

func (h *Hook) AfterAgentRun(ctx context.Context, arc *agentcore.AgentRunContext, output string, err error) {
	if err != nil {
		h.recorder.SetError(err)
	}
	h.recorder.MarkComplete()
	r := h.recorder.Rollout()

	if h.store != nil {
		if err := h.store.Save(ctx, r); err != nil {
			h.logger.Error("failed to save rollout",
				"id", r.ID,
				"error", err)
		} else {
			h.logger.Info("rollout saved",
				"id", r.ID,
				"turns", len(r.Turns))
		}
	}
}
