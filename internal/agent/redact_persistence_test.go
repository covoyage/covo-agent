package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/audit"
	agenttools "github.com/covoyage/covo-agent/internal/tools"
)

const redactTestSecret = "sk-live-1234567890abcd"

func TestTrajectoryAfterHookRedactsPersistedResults(t *testing.T) {
	ca := &CovoAgent{trajectory: NewTrajectoryRecorder("test-model", "", t.TempDir())}

	hook := ca.trajectoryAfterHook()
	hook(context.Background(), &agentcore.HookContext{ToolName: "bash"}, "token: "+redactTestSecret, nil)

	entries := ca.trajectory.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("expected 1 trajectory entry, got %d", len(entries))
	}
	if strings.Contains(entries[0].Content, redactTestSecret) {
		t.Errorf("trajectory entry contains the raw secret: %q", entries[0].Content)
	}
}

func TestTrajectoryBeforeHookRedactsPersistedInput(t *testing.T) {
	ca := &CovoAgent{trajectory: NewTrajectoryRecorder("test-model", "", t.TempDir())}

	hook := ca.trajectoryBeforeHook()
	args, _ := json.Marshal("echo " + redactTestSecret)
	err := hook(context.Background(), &agentcore.HookContext{ToolName: "user", Arguments: args})
	if err != nil {
		t.Fatalf("hook: %v", err)
	}

	entries := ca.trajectory.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("expected 1 trajectory entry, got %d", len(entries))
	}
	if strings.Contains(entries[0].Content, redactTestSecret) {
		t.Errorf("trajectory entry contains the raw secret: %q", entries[0].Content)
	}
}

func TestAuditHookRedactsPersistedPreviews(t *testing.T) {
	ext := agenttools.NewExtension(agenttools.ExtensionConfig{HomeDir: t.TempDir()})
	store := ext.AuditStore()
	if store == nil {
		t.Fatal("extension did not create an audit store")
	}
	defer store.Close()

	h := &auditHook{ca: &CovoAgent{agentTools: ext}}

	err := h.before(context.Background(), &agentcore.HookContext{
		ToolName:  "bash",
		Arguments: []byte(`{"command":"echo ` + redactTestSecret + `"}`),
	})
	if err != nil {
		t.Fatalf("before hook: %v", err)
	}
	h.after(context.Background(), &agentcore.HookContext{ToolName: "bash"}, "token: "+redactTestSecret, nil)

	for _, eventType := range []string{"tool:start", "tool:end"} {
		entries, err := store.Query(audit.QueryFilter{EventType: eventType})
		if err != nil {
			t.Fatalf("query %s: %v", eventType, err)
		}
		if len(entries) == 0 {
			t.Fatalf("expected at least one %s entry", eventType)
		}
		data := entries[0].Data
		if strings.Contains(data, redactTestSecret) {
			t.Errorf("%s entry contains the raw secret: %s", eventType, data)
		}
	}
}
