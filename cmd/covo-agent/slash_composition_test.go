package main

import (
	"context"
	"sync/atomic"
	"testing"

	runtimeapp "github.com/covoyage/covo-agent/internal/app"
)

func TestSlashCompositionBuilderWiresDynamicAndStaticDependencies(t *testing.T) {
	state := runtimeapp.NewRuntimeState()
	agents := runtimeapp.NewAgentRuntime(nil, nil)
	tracker := NewChangedFilesTracker(nil)
	tracker.recordEntry("changed.go", ActionModified, "edit")
	var busy atomic.Bool

	builder := newSlashContextBuilder(slashCompositionConfig{
		Busy:         &busy,
		Agents:       agents,
		State:        state,
		WorkingDir:   "/workspace",
		HomeDir:      "/home",
		ChangedFiles: tracker,
	})
	requestContext := context.Background()
	built := builder.Build("/status", requestContext, "provider", "model")

	if built.Runtime.Agents != agents || built.Runtime.State != state || built.Runtime.Busy != &busy {
		t.Fatal("runtime dependencies were not wired")
	}
	if built.Runtime.Context != requestContext || built.Runtime.ProviderType != "provider" || built.Runtime.Model != "model" {
		t.Fatal("request-scoped dependencies were not injected")
	}
	if built.Runtime.WorkingDir != "/workspace" || built.Services.HomeDir != "/home" {
		t.Fatal("directory dependencies were not wired")
	}
	if built.IO.ExportSessionHTML == nil || built.UI.RestoreChatHistory == nil || built.Services.ExecuteShellCommand == nil {
		t.Fatal("static command services were not wired")
	}
	if len(built.Services.Personalities) == 0 {
		t.Fatal("personality catalog was not wired")
	}
	built.Services.ResetChangedFiles()
	if got := len(tracker.Entries()); got != 0 {
		t.Fatalf("ResetChangedFiles left %d entries", got)
	}
}
