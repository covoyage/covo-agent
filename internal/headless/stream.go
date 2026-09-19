package headless

import (
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

// EventProjector maps agent lifecycle events onto headless NDJSON records.
type EventProjector struct {
	turn int
}

// Project converts a single agent event into a stream record.
// ok is false when the event should be omitted from the stream.
func (p *EventProjector) Project(ev agentcore.Event) (OutputEvent, bool) {
	switch e := ev.(type) {
	case *agentcore.TurnStartEvent:
		p.turn = int(e.Turn)
		return OutputEvent{}, false
	case *agentcore.MessageDeltaEvent:
		if e.Delta == "" || e.Kind == agentcore.BlockKindThinking {
			return OutputEvent{}, false
		}
		return OutputEvent{Type: "text", Content: e.Delta, Turn: p.turn}, true
	case *agentcore.ToolCallStartEvent:
		return OutputEvent{
			Type:      "tool_call",
			Content:   e.ToolCall.Name,
			ToolInput: e.ToolCall.Arguments,
			Turn:      p.turn,
		}, true
	case *agentcore.ToolCallEndEvent:
		result := e.Result
		if e.Err != nil {
			result = e.Err.Error()
		}
		return OutputEvent{
			Type:       "tool_result",
			Content:    e.ToolName,
			ToolResult: result,
			Turn:       p.turn,
		}, true
	case *agentcore.MessageResetEvent:
		return OutputEvent{Type: "reset", Content: e.AttemptID}, true
	default:
		return OutputEvent{}, false
	}
}

// BindStreamingWriter subscribes to agent events and writes NDJSON records.
// The returned function unregisters the handler.
func BindStreamingWriter(core *agentcore.Agent, sw *StreamingWriter) func() {
	if core == nil || sw == nil {
		return func() {}
	}
	var (
		mu        sync.Mutex
		projector EventProjector
	)
	return core.OnAll(func(ev agentcore.Event) {
		mu.Lock()
		event, ok := projector.Project(ev)
		mu.Unlock()
		if !ok {
			return
		}
		_ = sw.WriteEvent(event)
	})
}
