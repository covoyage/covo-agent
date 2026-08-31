package tui

import (
	"testing"

	"github.com/covoyage/covonaut/tui/core"

	"github.com/covoyage/covo-agent/internal/promptqueue"
)

func TestQueuePaneEmptyRender(t *testing.T) {
	pane := NewQueuePane(promptqueue.New(8))
	lines := pane.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected empty-queue copy")
	}
}

func TestQueuePaneKeys(t *testing.T) {
	queue := promptqueue.New(8)
	first := queue.Push("alpha")
	second := queue.Push("beta")
	pane := NewQueuePane(queue)

	var sent, removed promptqueue.Entry
	closed := false
	pane.SetOnSendNow(func(entry promptqueue.Entry) { sent = entry })
	pane.SetOnRemove(func(entry promptqueue.Entry) { removed = entry })
	pane.SetOnClose(func() { closed = true })

	pane.Update(core.KeyMsg{Data: "\x1b[B"})
	pane.Update(core.KeyMsg{Data: "\r"})
	if sent.ID != second.ID {
		t.Fatalf("enter sent %+v, want %s", sent, second.ID)
	}

	pane.Update(core.KeyMsg{Data: "\x7f"})
	if removed.ID != second.ID {
		t.Fatalf("backspace removed %+v, want %s", removed, second.ID)
	}

	pane.Update(core.KeyMsg{Data: "\x1b"})
	if !closed {
		t.Fatal("escape should close pane")
	}
	if first.ID == "" {
		t.Fatal("expected queued entries")
	}
}
