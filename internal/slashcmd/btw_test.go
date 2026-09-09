package slashcmd

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"

	"github.com/covoyage/covo-agent/internal/agent"
	runtimeapp "github.com/covoyage/covo-agent/internal/app"
)

func TestHandleBtwUsageAndCancel(t *testing.T) {
	app := chat.NewChatApp(chat.ChatAppConfig{})
	sctx := &SlashContext{
		Input: "/btw",
		UI:    UIDependencies{App: app},
	}
	if !handleBtw(sctx, []string{"/btw"}) {
		t.Fatal("expected handled")
	}

	cancelled := false
	sctx.Runtime.CancelSide = func() { cancelled = true }
	sctx.Input = "/btw cancel"
	handleBtw(sctx, []string{"/btw", "cancel"})
	if !cancelled {
		t.Fatal("expected CancelSide")
	}
	sctx.Input = "/btw clear"
	cancelled = false
	handleBtw(sctx, []string{"/btw", "clear"})
	if !cancelled {
		t.Fatal("expected CancelSide on clear")
	}
}

func TestHandleBtwRunsWhileMainBusyAndRejectsSecond(t *testing.T) {
	app := chat.NewChatApp(chat.ChatAppConfig{})
	var busy, sideBusy atomic.Bool
	busy.Store(true)
	started := make(chan struct{})
	release := make(chan struct{})
	sctx := &SlashContext{
		Input: "/btw what color is the sky",
		Runtime: RuntimeDependencies{
			Busy:     &busy,
			SideBusy: &sideBusy,
			Agents:   runtimeapp.NewAgentRuntime(nil, nil),
			CreateAgent: func(agent.AgentMode) *agent.CovoAgent {
				close(started)
				<-release
				return nil
			},
		},
		UI: UIDependencies{App: app},
	}
	if !handleBtw(sctx, []string{"/btw", "what", "color", "is", "the", "sky"}) {
		t.Fatal("expected handled while main busy")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("side agent was not started")
	}
	if !sideBusy.Load() {
		t.Fatal("expected side busy while first btw runs")
	}

	sctx.Input = "/btw second question"
	handleBtw(sctx, []string{"/btw", "second", "question"})
	close(release)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && sideBusy.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	if sideBusy.Load() {
		t.Fatal("expected side busy to clear")
	}

	var sideUser, sideAssistant bool
	for _, msg := range app.History().Messages() {
		if msg.Meta != "side" {
			continue
		}
		if msg.Role == chat.RoleUser {
			sideUser = true
		}
		if msg.Role == chat.RoleAssistant || msg.Role == chat.RoleError {
			sideAssistant = true
		}
	}
	if !sideUser || !sideAssistant {
		t.Fatalf("expected side user+reply, got %+v", app.History().Messages())
	}
}

func TestHandleBtwRegistersAndClearsSideCancel(t *testing.T) {
	app := chat.NewChatApp(chat.ChatAppConfig{})
	var sideBusy atomic.Bool
	var gotCancel, cleared atomic.Bool
	started := make(chan struct{})
	release := make(chan struct{})
	sctx := &SlashContext{
		Input: "/btw question",
		Runtime: RuntimeDependencies{
			SideBusy: &sideBusy,
			CreateAgent: func(agent.AgentMode) *agent.CovoAgent {
				close(started)
				<-release
				return nil
			},
			SetSideCancel: func(c context.CancelFunc) {
				if c == nil {
					cleared.Store(true)
					return
				}
				gotCancel.Store(true)
			},
		},
		UI: UIDependencies{App: app},
	}
	handleBtw(sctx, []string{"/btw", "question"})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("side agent was not started")
	}
	if !gotCancel.Load() {
		t.Fatal("expected SetSideCancel with cancel func")
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !cleared.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	if !cleared.Load() {
		t.Fatal("expected SetSideCancel(nil) after run")
	}
}

func TestParseBtwRest(t *testing.T) {
	cases := []struct {
		in      string
		persist bool
		q       string
	}{
		{"", false, ""},
		{"what color", false, "what color"},
		{"persist", true, ""},
		{"persist sky", true, "sky"},
		{"PERSIST  sky", true, "sky"},
		{"persist\tsky", true, "sky"},
	}
	for _, tc := range cases {
		persist, q := parseBtwRest(tc.in)
		if persist != tc.persist || q != tc.q {
			t.Errorf("parseBtwRest(%q) = (%v, %q), want (%v, %q)", tc.in, persist, q, tc.persist, tc.q)
		}
	}
}

func TestPersistBtwToMainWritesSideMessages(t *testing.T) {
	core := agentcore.New(agentcore.Config{})
	var appended []agentcore.Message
	persistBtwToMain(core, func(msg agentcore.Message) {
		appended = append(appended, msg)
	}, "sky?", "blue")
	msgs := core.State().Messages()
	if len(msgs) != 2 {
		t.Fatalf("core msgs=%d", len(msgs))
	}
	if msgs[0].Role != agentcore.RoleUser || msgs[0].Content != "sky?" || msgs[0].Name != "side" {
		t.Fatalf("user = %+v", msgs[0])
	}
	if msgs[1].Role != agentcore.RoleAssistant || msgs[1].Content != "blue" || msgs[1].Name != "side" {
		t.Fatalf("assistant = %+v", msgs[1])
	}
	if len(appended) != 2 {
		t.Fatalf("appended=%d", len(appended))
	}
}

func TestHandleBtwPersistWithoutQuestionShowsUsage(t *testing.T) {
	app := chat.NewChatApp(chat.ChatAppConfig{})
	sctx := &SlashContext{
		Input: "/btw persist",
		UI:    UIDependencies{App: app},
	}
	if !handleBtw(sctx, []string{"/btw", "persist"}) {
		t.Fatal("expected handled")
	}
}
