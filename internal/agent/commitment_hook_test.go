package agent

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
	agenttools "github.com/covoyage/covo-agent/internal/tools"
)

func TestCommitmentHookAfterAgentRun(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	homeDir := t.TempDir()

	// Create the extension first so agentTools has a commitment store.
	ext := agenttools.NewExtension(agenttools.ExtensionConfig{HomeDir: homeDir})
	ca := &CovoAgent{
		baseCfg:    CovoAgentConfig{Logger: logger},
		agentTools: ext,
	}

	hook := newCommitmentHook(ca)

	arc := &agentcore.AgentRunContext{}
	ctx := context.Background()

	// No commitments — should not log
	hook.AfterAgentRun(ctx, arc, "", nil)
	if buf.Len() > 0 {
		t.Errorf("expected no log output, got: %s", buf.String())
	}

	// Add a commitment via the shared store
	ca.CommitmentStore().Detect("I'll check the database.", "test")

	hook.AfterAgentRun(ctx, arc, "", nil)
	if buf.Len() == 0 {
		t.Fatal("expected log output, got none")
	}
	if !bytes.Contains(buf.Bytes(), []byte("pending commitment")) {
		t.Errorf("expected log to mention 'pending commitment', got: %s", buf.String())
	}
}

func TestCommitmentHookAfterAgentRunNilLogger(t *testing.T) {
	homeDir := t.TempDir()
	ext := agenttools.NewExtension(agenttools.ExtensionConfig{HomeDir: homeDir})
	ca := &CovoAgent{
		baseCfg:    CovoAgentConfig{Logger: nil},
		agentTools: ext,
	}

	hook := newCommitmentHook(ca)

	ca.CommitmentStore().Detect("I'll investigate the error.", "test")

	ctx := context.Background()
	arc := &agentcore.AgentRunContext{}

	// Should not panic despite nil Logger
	hook.AfterAgentRun(ctx, arc, "", nil)
}
