package slashcmd

import (
	"sync/atomic"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"

	runtimeapp "github.com/covoyage/covo-agent/internal/app"
)

func TestHandleBusyAcceptsSteerAndCollect(t *testing.T) {
	state := runtimeapp.NewRuntimeState()
	app := chat.NewChatApp(chat.ChatAppConfig{})
	sctx := &SlashContext{
		Input:   "/busy steer",
		Runtime: RuntimeDependencies{State: state},
		UI:      UIDependencies{App: app},
	}
	if !handleBusy(sctx, []string{"/busy", "steer"}) {
		t.Fatal("expected handled")
	}
	if got := state.BusyInputMode(); got != "steer" {
		t.Fatalf("mode=%q", got)
	}
	sctx.Input = "/busy collect"
	handleBusy(sctx, []string{"/busy", "collect"})
	if got := state.BusyInputMode(); got != "collect" {
		t.Fatalf("mode=%q", got)
	}
	handleBusy(sctx, []string{"/busy", "nope"})
	if got := state.BusyInputMode(); got != "collect" {
		t.Fatalf("invalid mode mutated state: %q", got)
	}
}

func TestHandleSteerForegroundRequiresBusy(t *testing.T) {
	var busy atomic.Bool
	state := runtimeapp.NewRuntimeState()
	app := chat.NewChatApp(chat.ChatAppConfig{})
	sctx := &SlashContext{
		Input: "/steer please stop looping",
		Runtime: RuntimeDependencies{
			Busy:   &busy,
			State:  state,
			Agents: runtimeapp.NewAgentRuntime(nil, nil),
		},
		UI: UIDependencies{App: app},
	}
	if !handleSteer(sctx, []string{"/steer", "please", "stop", "looping"}) {
		t.Fatal("expected handled")
	}
	busy.Store(true)
	if !handleSteer(sctx, []string{"/steer", "please", "stop", "looping"}) {
		t.Fatal("expected handled when busy")
	}
}

func TestLooksLikeBackgroundTaskID(t *testing.T) {
	if !looksLikeBackgroundTaskID("bg-12") {
		t.Fatal("bg-12")
	}
	if looksLikeBackgroundTaskID("please") {
		t.Fatal("please is not a bg id")
	}
	if looksLikeBackgroundTaskID("bg-") {
		t.Fatal("bg-")
	}
}

func TestHandleSteerDoesNotTreatTextAsBackgroundID(t *testing.T) {
	var busy atomic.Bool
	busy.Store(true)
	app := chat.NewChatApp(chat.ChatAppConfig{})
	sctx := &SlashContext{
		Input: "/steer change approach",
		Runtime: RuntimeDependencies{
			Busy:   &busy,
			Agents: runtimeapp.NewAgentRuntime(nil, nil),
		},
		UI: UIDependencies{App: app},
	}
	handleSteer(sctx, []string{"/steer", "change", "approach"})
}

func TestHandleVerboseAndThinking(t *testing.T) {
	app := chat.NewChatApp(chat.ChatAppConfig{})
	sctx := &SlashContext{UI: UIDependencies{App: app}}
	handleVerbose(sctx, []string{"/verbose", "full"})
	if got := app.History().VerboseLevel(); got != "full" {
		t.Fatalf("verbose=%q", got)
	}
	handleVerbose(sctx, []string{"/verbose", "off"})
	if got := app.History().VerboseLevel(); got != "off" {
		t.Fatalf("verbose=%q", got)
	}
	handleThinking(sctx, []string{"/thinking", "truncated"})
	handleThinking(sctx, []string{"/thinking", "nope"})
	handleThinking(sctx, []string{"/thinking", "stream", "off"})
	if cur, ok := app.History().ReasoningRenderer().(*chat.DefaultReasoningRenderer); !ok || cur.Stream {
		t.Fatal("expected thinking stream off")
	}
}

func TestClearSteeringAPI(t *testing.T) {
	ag := agentcore.New(agentcore.Config{})
	ag.Steer(agentcore.Message{Role: agentcore.RoleUser, Content: "nudge"})
	if ag.SteeringPending() == 0 {
		t.Fatal("expected pending steer")
	}
	ag.ClearSteering()
	if ag.SteeringPending() != 0 {
		t.Fatal("expected cleared")
	}
}
