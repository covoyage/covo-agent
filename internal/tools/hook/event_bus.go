package hook

import (
	"fmt"
	"sync"
	"time"
)

type EventType string

const (
	EventSessionStart  EventType = "session:start"
	EventSessionEnd    EventType = "session:end"
	EventTurnStart     EventType = "turn:start"
	EventTurnEnd       EventType = "turn:end"
	EventToolCallStart EventType = "tool:start"
	EventToolCallEnd   EventType = "tool:end"
	EventLLMCallStart  EventType = "llm:start"
	EventLLMCallEnd    EventType = "llm:end"
	EventSubagentSpawn EventType = "subagent:spawn"
	EventSubagentEnd   EventType = "subagent:end"
	EventCompact       EventType = "compact"
	EventError         EventType = "error"
)

type EventPayload struct {
	Type      EventType   `json:"type"`
	SessionID string      `json:"session_id,omitempty"`
	ToolName  string      `json:"tool_name,omitempty"`
	AgentID   string      `json:"agent_id,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

type EventHandler func(payload EventPayload)

type EventBus struct {
	mu          sync.RWMutex
	subscribers map[EventType][]EventHandler
	wildcards   []EventHandler
	bufferSize  int
}

func NewEventBus(bufferSize int) *EventBus {
	if bufferSize <= 0 {
		bufferSize = 64
	}
	return &EventBus{
		subscribers: make(map[EventType][]EventHandler),
		bufferSize:  bufferSize,
	}
}

func (b *EventBus) Subscribe(eventType EventType, handler EventHandler) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscribers[eventType] = append(b.subscribers[eventType], handler)
	return func() { b.unsubscribe(eventType, handler) }
}

func (b *EventBus) SubscribeAll(handler EventHandler) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.wildcards = append(b.wildcards, handler)
	return func() { b.unsubscribeAll(handler) }
}

func (b *EventBus) Publish(eventType EventType, data interface{}) {
	b.mu.RLock()
	handlers := make([]EventHandler, 0, len(b.subscribers[eventType])+len(b.wildcards))
	handlers = append(handlers, b.subscribers[eventType]...)
	handlers = append(handlers, b.wildcards...)
	b.mu.RUnlock()

	payload := EventPayload{Type: eventType, Data: data, Timestamp: time.Now()}
	for _, h := range handlers {
		go h(payload)
	}
}

func (b *EventBus) unsubscribe(eventType EventType, handler EventHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := b.subscribers[eventType]
	for i, h := range subs {
		if fmt.Sprintf("%p", h) == fmt.Sprintf("%p", handler) {
			b.subscribers[eventType] = append(subs[:i], subs[i+1:]...)
			return
		}
	}
}

func (b *EventBus) unsubscribeAll(handler EventHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, h := range b.wildcards {
		if fmt.Sprintf("%p", h) == fmt.Sprintf("%p", handler) {
			b.wildcards = append(b.wildcards[:i], b.wildcards[i+1:]...)
			return
		}
	}
}
