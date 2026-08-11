package agent

import (
	"context"
	"log/slog"

	"github.com/covoyage/covonaut/agentcore"
)

// snapshotAfterHook returns an AfterHook that captures a file-level snapshot
// after each file-mutating tool call. The snapshot is content-addressed and
// stored in an isolated git repository (see internal/snapshot), enabling
// /undo and revert-to-snapshot without polluting the user's working repo.
//
// Captures a snapshot at step boundaries: every file-mutating tool call
// produces a PatchPart {hash, files} persisted in the session history, and
// revert operates at the file level using git checkout.
//
// The hook is non-fatal: errors are logged but never fail the tool call,
// because snapshotting is a best-effort recovery feature, not a correctness
// requirement.
func (ca *CovoAgent) snapshotAfterHook() agentcore.AfterHook {
	return func(ctx context.Context, hc *agentcore.HookContext, result string, err error) {
		if ca.snapshotMgr == nil || !ca.snapshotMgr.Enabled() {
			return
		}
		if !ShouldSnapshot(hc.ToolName) {
			return
		}
		// Capture snapshot regardless of err: even a partially-applied edit
		// may have mutated files, and we want the recorded state to match
		// the working tree.
		msgIdx := 0
		if ca.core != nil {
			msgIdx = len(ca.core.State().Messages())
		}
		if trackErr := ca.snapshotMgr.Track(hc.ToolName, msgIdx); trackErr != nil {
			if logger := ca.logger(); logger != nil {
				logger.Debug("snapshot track failed",
					"tool", hc.ToolName,
					"error", trackErr,
				)
			}
		}
	}
}

// logger returns the agent's structured logger if one is configured.
func (ca *CovoAgent) logger() *slog.Logger {
	if ca.baseCfg.Logger != nil {
		return ca.baseCfg.Logger
	}
	return nil
}
