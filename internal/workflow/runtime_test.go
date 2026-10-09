package workflow

import (
	"context"
	"testing"
)

func TestExecuteScriptPausesOnAskAndResumes(t *testing.T) {
	dir := t.TempDir()
	src := `
name("demo")
phase("map", "list tests")
ask("confirm")
spawn("fix", "coder")
`
	var ran []string
	run := func(_ context.Context, phase Phase) (*PhaseResult, error) {
		ran = append(ran, phase.ID)
		return &PhaseResult{PhaseID: phase.ID, Status: PhaseCompleted, Output: "ok"}, nil
	}
	first, err := ExecuteScript(context.Background(), ScriptExecOptions{
		Source:   src,
		HomeDir:  dir,
		RunPhase: run,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "paused" || first.PendingAsk == nil || first.PendingAsk.ID != "confirm" {
		t.Fatalf("want paused ask, got %+v", first)
	}
	if len(ran) != 1 || ran[0] != "map" {
		t.Fatalf("ran=%v", ran)
	}

	second, err := ExecuteScript(context.Background(), ScriptExecOptions{
		HomeDir:    dir,
		WorkflowID: first.WorkflowID,
		Answers:    map[string]string{"confirm": "yes"},
		RunPhase:   run,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "ok" {
		t.Fatalf("resume status=%s pending=%+v", second.Status, second.PendingAsk)
	}
	if len(ran) != 2 || ran[1] != "fix" {
		t.Fatalf("ran after resume=%v", ran)
	}
}

func TestExecuteScriptDryRun(t *testing.T) {
	out, err := ExecuteScript(context.Background(), ScriptExecOptions{
		Source: `phase("a", "do")`,
		DryRun: true,
		RunPhase: func(context.Context, Phase) (*PhaseResult, error) {
			t.Fatal("dry run must not execute")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "ok" || len(out.Analysis.Sites) != 1 {
		t.Fatalf("%+v", out)
	}
}
