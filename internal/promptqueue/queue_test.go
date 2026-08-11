package promptqueue

import (
	"strings"
	"testing"
)

func TestQueue_PushPop(t *testing.T) {
	q := New(10)
	if !q.IsEmpty() {
		t.Fatal("expected empty")
	}

	e1 := q.Push("first")
	if q.Len() != 1 {
		t.Fatalf("expected len 1, got %d", q.Len())
	}

	q.Push("second")
	if q.Len() != 2 {
		t.Fatalf("expected len 2, got %d", q.Len())
	}

	// Pop should return first (FIFO)
	popped, ok := q.Pop()
	if !ok {
		t.Fatal("expected pop ok")
	}
	if popped.Text != "first" {
		t.Errorf("expected 'first', got %q", popped.Text)
	}
	if popped.ID != e1.ID {
		t.Error("ID mismatch")
	}

	popped, _ = q.Pop()
	if popped.Text != "second" {
		t.Errorf("expected 'second', got %q", popped.Text)
	}

	if !q.IsEmpty() {
		t.Error("expected empty after popping all")
	}
}

func TestQueue_PushFront(t *testing.T) {
	q := New(10)
	q.Push("first")
	q.PushFront("urgent")

	popped, _ := q.Pop()
	if popped.Text != "urgent" {
		t.Errorf("expected 'urgent' from front, got %q", popped.Text)
	}
}

func TestQueue_Peek(t *testing.T) {
	q := New(10)
	q.Push("first")

	peeked, ok := q.Peek()
	if !ok {
		t.Fatal("expected peek ok")
	}
	if peeked.Text != "first" {
		t.Errorf("expected 'first', got %q", peeked.Text)
	}
	if q.Len() != 1 {
		t.Error("peek should not remove")
	}
}

func TestQueue_Remove(t *testing.T) {
	q := New(10)
	e1 := q.Push("first")
	q.Push("second")

	if !q.Remove(e1.ID) {
		t.Fatal("expected remove to succeed")
	}
	if q.Len() != 1 {
		t.Fatalf("expected len 1, got %d", q.Len())
	}

	popped, _ := q.Pop()
	if popped.Text != "second" {
		t.Errorf("expected 'second', got %q", popped.Text)
	}
}

func TestQueue_Clear(t *testing.T) {
	q := New(10)
	q.Push("a")
	q.Push("b")
	q.Clear()
	if !q.IsEmpty() {
		t.Error("expected empty after clear")
	}
}

func TestQueue_All(t *testing.T) {
	q := New(10)
	q.Push("a")
	q.Push("b")
	q.Push("c")

	all := q.All()
	if len(all) != 3 {
		t.Fatalf("expected 3, got %d", len(all))
	}
	// Modifying the copy should not affect the queue
	all[0].Text = "modified"
	again := q.All()
	if again[0].Text != "a" {
		t.Error("original was modified")
	}
}

func TestQueue_DrainAll(t *testing.T) {
	q := New(10)
	q.Push("a")
	q.Push("b")

	all := q.DrainAll()
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}
	if !q.IsEmpty() {
		t.Error("expected empty after drain")
	}
}

func TestQueue_TryMerge_ShortPrompts(t *testing.T) {
	q := New(10)
	q.Push("add tests")

	// Short follower should merge
	merged := q.TryMerge("also fix lint", 200)
	if !merged {
		t.Error("expected merge to succeed")
	}

	if q.Len() != 1 {
		t.Fatalf("expected 1 entry after merge, got %d", q.Len())
	}

	popped, _ := q.Pop()
	if !popped.Combined {
		t.Error("expected combined flag")
	}
	if !strings.Contains(popped.Text, "add tests") || !strings.Contains(popped.Text, "also fix lint") {
		t.Errorf("merged text should contain both: %q", popped.Text)
	}
}

func TestQueue_TryMerge_LongPrompts(t *testing.T) {
	q := New(10)
	longText := strings.Repeat("x", 300)
	q.Push(longText)

	// Long prompts should not merge
	merged := q.TryMerge("short", 200)
	if merged {
		t.Error("expected no merge for long prompts")
	}
	if q.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", q.Len())
	}
}

func TestQueue_TryMerge_EmptyQueue(t *testing.T) {
	q := New(10)
	merged := q.TryMerge("first prompt", 200)
	if merged {
		t.Error("expected no merge on empty queue")
	}
	if q.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", q.Len())
	}
}

func TestQueue_CanMergeFollower(t *testing.T) {
	q := New(10)
	q.Push("short")

	if !q.CanMergeFollower("also short", 200) {
		t.Error("expected can merge follower")
	}
	if q.CanMergeFollower(strings.Repeat("x", 300), 200) {
		t.Error("expected cannot merge long follower")
	}
}

func TestQueue_CanMergeFront(t *testing.T) {
	q := New(10)
	q.Push("short")

	if !q.CanMergeFront("also short", 200) {
		t.Error("expected can merge front")
	}
}

func TestQueue_CanMergeFollower_EmptyQueue(t *testing.T) {
	q := New(10)
	if q.CanMergeFollower("text", 200) {
		t.Error("expected false on empty queue")
	}
}

func TestQueue_MaxSize(t *testing.T) {
	q := New(3)
	q.Push("a")
	q.Push("b")
	q.Push("c")
	q.Push("d") // should trim oldest

	if q.Len() != 3 {
		t.Fatalf("expected 3, got %d", q.Len())
	}

	// Should have b, c, d (a was trimmed)
	popped, _ := q.Pop()
	if popped.Text != "b" {
		t.Errorf("expected 'b', got %q", popped.Text)
	}
}

func TestQueue_PopEmpty(t *testing.T) {
	q := New(10)
	_, ok := q.Pop()
	if ok {
		t.Error("expected false on empty pop")
	}
}

func TestQueue_PeekEmpty(t *testing.T) {
	q := New(10)
	_, ok := q.Peek()
	if ok {
		t.Error("expected false on empty peek")
	}
}

func TestQueue_Concurrent(t *testing.T) {
	q := New(100)
	done := make(chan struct{})

	// Writer
	go func() {
		for i := 0; i < 50; i++ {
			q.Push("prompt")
		}
		close(done)
	}()

	// Reader
	go func() {
		for i := 0; i < 50; i++ {
			q.Pop()
		}
	}()

	<-done
	// Should not deadlock or panic
}
