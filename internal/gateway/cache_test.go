package gateway

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type mockAgent struct {
	id          int32
	closeCalled atomic.Bool
}

func (m *mockAgent) Run(_ context.Context, _ string) (string, error) {
	return "ok", nil
}

func (m *mockAgent) Close() {
	m.closeCalled.Store(true)
}

var nextAgentID atomic.Int32

func TestNewAgentCache_Defaults(t *testing.T) {
	c := NewAgentCache(0, 0, nil)
	if c.maxSize != 128 {
		t.Errorf("expected default maxSize 128, got %d", c.maxSize)
	}
	if c.ttl != time.Hour {
		t.Errorf("expected default ttl 1h, got %v", c.ttl)
	}
}

func TestNewAgentCache_Custom(t *testing.T) {
	c := NewAgentCache(10, 5*time.Minute, nil)
	if c.maxSize != 10 {
		t.Errorf("expected maxSize 10, got %d", c.maxSize)
	}
	if c.ttl != 5*time.Minute {
		t.Errorf("expected ttl 5m, got %v", c.ttl)
	}
}

func TestAgentCache_GetOrCreate_New(t *testing.T) {
	var created int32
	c := NewAgentCache(10, time.Hour, func(_ context.Context) (Agent, error) {
		created++
		return &mockAgent{}, nil
	})
	ctx := context.Background()

	agent, err := c.GetOrCreate(ctx, "key1")
	if err != nil {
		t.Fatal(err)
	}
	if agent == nil {
		t.Fatal("expected non-nil agent")
	}
	if created != 1 {
		t.Errorf("expected 1 factory call, got %d", created)
	}
	if c.Size() != 1 {
		t.Errorf("expected cache size 1, got %d", c.Size())
	}
}

func TestAgentCache_GetOrCreate_Cached(t *testing.T) {
	var created int32
	c := NewAgentCache(10, time.Hour, func(_ context.Context) (Agent, error) {
		created++
		id := nextAgentID.Add(1)
		return &mockAgent{id: id}, nil
	})
	ctx := context.Background()

	a1, _ := c.GetOrCreate(ctx, "key1")
	a2, _ := c.GetOrCreate(ctx, "key1")

	if a1 != a2 {
		t.Error("expected same agent instance for same key")
	}
	if created != 1 {
		t.Errorf("expected 1 factory call, got %d", created)
	}
}

func TestAgentCache_GetOrCreate_MultipleKeys(t *testing.T) {
	var created int32
	c := NewAgentCache(10, time.Hour, func(_ context.Context) (Agent, error) {
		created++
		return &mockAgent{}, nil
	})
	ctx := context.Background()

	_, _ = c.GetOrCreate(ctx, "k1")
	_, _ = c.GetOrCreate(ctx, "k2")
	_, _ = c.GetOrCreate(ctx, "k3")

	if created != 3 {
		t.Errorf("expected 3 factory calls, got %d", created)
	}
	if c.Size() != 3 {
		t.Errorf("expected cache size 3, got %d", c.Size())
	}
}

func TestAgentCache_EvictStale(t *testing.T) {
	var agents []*mockAgent
	c := NewAgentCache(10, time.Hour, func(_ context.Context) (Agent, error) {
		a := &mockAgent{}
		agents = append(agents, a)
		return a, nil
	})
	ctx := context.Background()

	_, _ = c.GetOrCreate(ctx, "k1")
	_, _ = c.GetOrCreate(ctx, "k2")
	if c.Size() != 2 {
		t.Fatalf("expected size 2, got %d", c.Size())
	}

	// Set lastUsed to the past by directly manipulating the cache
	for _, ca := range c.agents {
		ca.lastUsed = time.Now().Add(-2 * time.Hour)
	}

	c.evictStale()
	if c.Size() != 0 {
		t.Errorf("expected size 0 after evicting stale agents, got %d", c.Size())
	}
}

func TestAgentCache_Close(t *testing.T) {
	var agents []*mockAgent
	c := NewAgentCache(10, time.Hour, func(_ context.Context) (Agent, error) {
		a := &mockAgent{}
		agents = append(agents, a)
		return a, nil
	})
	ctx := context.Background()

	_, _ = c.GetOrCreate(ctx, "k1")
	_, _ = c.GetOrCreate(ctx, "k2")
	c.Close()

	if c.Size() != 0 {
		t.Errorf("expected size 0 after close, got %d", c.Size())
	}
	for i, a := range agents {
		if !a.closeCalled.Load() {
			t.Errorf("agent %d was not closed", i)
		}
	}
}

func TestAgentCache_Size(t *testing.T) {
	c := NewAgentCache(10, time.Hour, func(_ context.Context) (Agent, error) {
		return &mockAgent{}, nil
	})
	if c.Size() != 0 {
		t.Errorf("expected initial size 0, got %d", c.Size())
	}
	c.agents["x"] = &cachedAgent{}
	if c.Size() != 1 {
		t.Errorf("expected size 1, got %d", c.Size())
	}
}
