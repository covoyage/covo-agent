package headless

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestEventProjectorMapsLifecycle(t *testing.T) {
	var p EventProjector
	p.Project(&agentcore.TurnStartEvent{Turn: 2})

	text, ok := p.Project(&agentcore.MessageDeltaEvent{Delta: "hi", Kind: agentcore.BlockKindText})
	if !ok || text.Type != "text" || text.Content != "hi" || text.Turn != 2 {
		t.Fatalf("text event = %+v ok=%v", text, ok)
	}
	if _, ok := p.Project(&agentcore.MessageDeltaEvent{Delta: "think", Kind: agentcore.BlockKindThinking}); ok {
		t.Fatal("thinking deltas should be omitted")
	}

	call, ok := p.Project(&agentcore.ToolCallStartEvent{ToolCall: agentcore.ToolCall{Name: "read", Arguments: `{"path":"a.go"}`}})
	if !ok || call.Type != "tool_call" || call.Content != "read" || call.ToolInput != `{"path":"a.go"}` {
		t.Fatalf("tool call = %+v", call)
	}
	result, ok := p.Project(&agentcore.ToolCallEndEvent{ToolName: "read", Result: "ok"})
	if !ok || result.Type != "tool_result" || result.ToolResult != "ok" {
		t.Fatalf("tool result = %+v", result)
	}
}

func TestStreamingWriterIsLineDelimited(t *testing.T) {
	var buf bytes.Buffer
	sw := NewStreamingWriter(&buf)
	if err := sw.WriteText("hello", 1); err != nil {
		t.Fatal(err)
	}
	if err := sw.WriteDone(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	var first OutputEvent
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.Type != "text" || first.Content != "hello" {
		t.Fatalf("first = %+v", first)
	}
}
