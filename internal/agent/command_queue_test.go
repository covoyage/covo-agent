package agent

import (
	"context"
	"testing"
	"time"
)

func TestCommandQueuePriorityOrder(t *testing.T) {
	q := NewCommandQueue()
	later := q.Enqueue("tui", ModePrompt, PriorityLater, "later")
	next := q.Enqueue("tui", ModePrompt, PriorityNext, "next")
	now := q.Enqueue("acp", ModeControlOnly, PriorityNow, "now")
	if q.Len() != 3 {
		t.Fatalf("len=%d", q.Len())
	}
	ctx := context.Background()
	first, err := q.dequeue(ctx)
	if err != nil || first != now {
		t.Fatalf("first=%v err=%v", first, err)
	}
	second, err := q.dequeue(ctx)
	if err != nil || second != next {
		t.Fatalf("second=%v err=%v", second, err)
	}
	third, err := q.dequeue(ctx)
	if err != nil || third != later {
		t.Fatalf("third=%v err=%v", third, err)
	}
}

func TestCommandQueueWaitAndFinish(t *testing.T) {
	q := NewCommandQueue()
	cmd := q.Enqueue("tui", ModePrompt, PriorityNext, "hi")
	go func() {
		got, err := q.dequeue(context.Background())
		if err != nil {
			t.Errorf("dequeue: %v", err)
			return
		}
		got.finish("ok", nil)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := cmd.Wait(ctx)
	if err != nil || out != "ok" {
		t.Fatalf("wait=%q %v", out, err)
	}
}

func TestCommandQueueCloseRejects(t *testing.T) {
	q := NewCommandQueue()
	q.Close()
	cmd := q.Enqueue("tui", ModePrompt, PriorityNext, "x")
	if _, err := cmd.Wait(context.Background()); err == nil {
		t.Fatal("closed queue should reject")
	}
}
