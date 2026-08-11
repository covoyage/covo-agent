package gateway

import (
	"container/list"
	"context"
	"sync"
	"time"
)

type AgentFactory func(ctx context.Context) (Agent, error)

type Agent interface {
	Run(ctx context.Context, input string) (string, error)
	Close()
}

type cachedAgent struct {
	agent    Agent
	lastUsed time.Time
	elem     *list.Element
}

type AgentCache struct {
	mu      sync.Mutex
	maxSize int
	ttl     time.Duration
	agents  map[string]*cachedAgent
	lru     *list.List
	factory AgentFactory
}

func NewAgentCache(maxSize int, ttl time.Duration, factory AgentFactory) *AgentCache {
	if maxSize <= 0 {
		maxSize = 128
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &AgentCache{
		maxSize: maxSize,
		ttl:     ttl,
		agents:  make(map[string]*cachedAgent),
		lru:     list.New(),
		factory: factory,
	}
}

func (c *AgentCache) GetOrCreate(ctx context.Context, key string) (Agent, error) {
	c.mu.Lock()
	if ca, ok := c.agents[key]; ok {
		ca.lastUsed = time.Now()
		c.lru.MoveToFront(ca.elem)
		agent := ca.agent
		c.mu.Unlock()
		return agent, nil
	}
	c.mu.Unlock()

	agent, err := c.factory(ctx)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if ca, ok := c.agents[key]; ok {
		agent.Close()
		ca.lastUsed = time.Now()
		c.lru.MoveToFront(ca.elem)
		return ca.agent, nil
	}

	ca := &cachedAgent{
		agent:    agent,
		lastUsed: time.Now(),
	}
	ca.elem = c.lru.PushFront(key)
	c.agents[key] = ca

	if c.lru.Len() > c.maxSize {
		c.evictLRU()
	}

	return agent, nil
}

func (c *AgentCache) evictLRU() {
	for c.lru.Len() > c.maxSize {
		elem := c.lru.Back()
		if elem == nil {
			return
		}
		key := elem.Value.(string)
		ca, ok := c.agents[key]
		if !ok {
			c.lru.Remove(elem)
			continue
		}
		if time.Since(ca.lastUsed) < c.ttl {
			return
		}
		c.lru.Remove(elem)
		delete(c.agents, key)
		ca.agent.Close()
	}
}

func (c *AgentCache) evictStale() {
	c.mu.Lock()

	var toClose []Agent
	for key, ca := range c.agents {
		if time.Since(ca.lastUsed) >= c.ttl {
			c.lru.Remove(ca.elem)
			delete(c.agents, key)
			toClose = append(toClose, ca.agent)
		}
	}
	c.mu.Unlock()

	for _, a := range toClose {
		a.Close()
	}
}

func (c *AgentCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.agents)
}

func (c *AgentCache) Close() {
	c.mu.Lock()

	var toClose []Agent
	for key, ca := range c.agents {
		c.lru.Remove(ca.elem)
		delete(c.agents, key)
		toClose = append(toClose, ca.agent)
	}
	c.mu.Unlock()

	for _, a := range toClose {
		a.Close()
	}
}
