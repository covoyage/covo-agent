package commands

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/covoyage/covonaut/tui"
	"github.com/covoyage/covonaut/tui/chat"
)

func TestInterruptCurrentRunClearsDisplayOnce(t *testing.T) {
	app := tui.NewChatApp(chat.ChatAppConfig{})
	app.Busy("fetching web content...")
	app.SetThinkingTail("old thinking")
	t.Cleanup(app.Idle)
	session := &interactiveSession{app: app}
	session.busy.Store(true)
	var calls atomic.Int32
	var cancel context.CancelFunc = func() {
		calls.Add(1)
		if app.Loader().IsRunning() {
			t.Error("cancel callback still sees active loader")
		}
	}
	session.cancelRun.Store(&cancel)
	if !session.interruptCurrentRun() {
		t.Fatal("active task was not interrupted")
	}
	if session.interruptCurrentRun() || calls.Load() != 1 || session.cancelRun.Load() != nil {
		t.Fatal("repeat interrupt reported another cancellation")
	}
	if app.Loader().IsRunning() {
		t.Fatal("interrupted task still displays running loader")
	}
	if !session.busy.Load() {
		t.Fatal("interrupt prematurely opened task execution gate")
	}
	app.Busy("late tool event")
	if app.Loader().IsRunning() {
		t.Fatal("late event restarted interrupted loader")
	}
}

func TestSideQuestionToolAllowed(t *testing.T) {
	for _, name := range []string{"read", "grep", "glob", "web_search", "web_fetch"} {
		if !sideQuestionToolAllowed(name) {
			t.Fatalf("%s should be allowed", name)
		}
	}
	for _, name := range []string{"bash", "write_file", "edit", "delete_file"} {
		if sideQuestionToolAllowed(name) {
			t.Fatalf("%s should be denied", name)
		}
	}
}
