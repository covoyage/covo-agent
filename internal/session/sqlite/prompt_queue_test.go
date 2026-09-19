package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/promptqueue"
	"github.com/covoyage/covonaut/agentcore"
)

func TestPromptQueueSaveLoadRoundTrip(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	entries := []promptqueue.Entry{
		{
			ID:        "AAA11111",
			Text:      "first",
			QueuedAt:  time.UnixMilli(1_700_000_000_000),
			Combined:  false,
			SourceIDs: nil,
		},
		{
			ID:        "BBB22222",
			Text:      "merged\n---\nnext",
			QueuedAt:  time.UnixMilli(1_700_000_000_100),
			Combined:  true,
			SourceIDs: []string{"old"},
		},
	}
	if err := store.SavePromptQueue(ctx, "sess-a", entries); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadPromptQueue(ctx, "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Text != "first" || got[1].Combined != true || len(got[1].SourceIDs) != 1 {
		t.Fatalf("got %+v", got)
	}
	if got[0].QueuedAt.UnixMilli() != 1_700_000_000_000 {
		t.Fatalf("queued_at=%d", got[0].QueuedAt.UnixMilli())
	}

	other, err := store.LoadPromptQueue(ctx, "sess-b")
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("other session should be empty, got %+v", other)
	}

	if err := store.SavePromptQueue(ctx, "sess-a", nil); err != nil {
		t.Fatal(err)
	}
	cleared, err := store.LoadPromptQueue(ctx, "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared) != 0 {
		t.Fatalf("cleared=%+v", cleared)
	}
}

func TestLatestSessionID(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.EnsureSession(ctx, "empty", "/tmp/proj"); err != nil {
		t.Fatal(err)
	}
	id, err := store.LatestSessionID(ctx, "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("empty session should be ignored, got %q", id)
	}
	if err := store.EnsureSession(ctx, "used", "/tmp/proj"); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(ctx, "used", agentcore.Message{Role: "user", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	id, err = store.LatestSessionID(ctx, "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	if id != "used" {
		t.Fatalf("latest = %q", id)
	}
}

func TestPromptQueueCountAndCascade(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.SavePromptQueue(ctx, "keep-me", []promptqueue.Entry{{ID: "1", Text: "queued", QueuedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	n, err := store.PromptQueueCount(ctx, "keep-me")
	if err != nil || n != 1 {
		t.Fatalf("count=%d err=%v", n, err)
	}
	if err := store.DeleteSession(ctx, "keep-me"); err != nil {
		t.Fatal(err)
	}
	n, err = store.PromptQueueCount(ctx, "keep-me")
	if err != nil || n != 0 {
		t.Fatalf("after delete count=%d err=%v", n, err)
	}
}
