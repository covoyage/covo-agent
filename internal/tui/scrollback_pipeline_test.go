package tui

import (
	"testing"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/theme"
)

func TestScrollbackPipeline_AppendAndRender(t *testing.T) {
	p := NewScrollbackPipeline()

	e1 := p.Append(&UserPromptBlock{Text: "Hello"})
	e2 := p.Append(&AgentMessageBlock{Text: "Hi there!"})

	if e1.ID == 0 {
		t.Error("expected non-zero entry ID")
	}
	if e1.ID == e2.ID {
		t.Error("entries should have unique IDs")
	}

	pal := theme.CurrentPalette()
	lines := p.RenderAll(80, pal)
	if len(lines) == 0 {
		t.Fatal("expected non-empty render")
	}
}

func TestScrollbackPipeline_RunningState(t *testing.T) {
	p := NewScrollbackPipeline()

	entry := p.Append(&ToolCallBlock{ToolName: "bash", Args: "ls -la"})
	p.StartRunning(entry)

	running := p.RunningEntries()
	if len(running) != 1 {
		t.Fatalf("expected 1 running entry, got %d", len(running))
	}
	if !running[0].Running {
		t.Error("entry should be running")
	}

	p.FinishRunning(entry)
	running = p.RunningEntries()
	if len(running) != 0 {
		t.Errorf("expected 0 running after finish, got %d", len(running))
	}
	if entry.Finished {
		// good
	} else {
		t.Error("entry should be marked finished")
	}
}

func TestScrollbackPipeline_Clear(t *testing.T) {
	p := NewScrollbackPipeline()
	p.Append(&UserPromptBlock{Text: "hello"})
	p.Append(&UserPromptBlock{Text: "world"})

	if len(p.Entries()) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(p.Entries()))
	}

	p.Clear()
	if len(p.Entries()) != 0 {
		t.Errorf("expected 0 after clear, got %d", len(p.Entries()))
	}
}

func TestUserPromptBlock_Render(t *testing.T) {
	b := &UserPromptBlock{Text: "Hello\nWorld"}
	pal := theme.CurrentPalette()
	lines := b.RenderLines(80, pal)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if b.Kind() != BlockKindUserPrompt {
		t.Error("wrong kind")
	}
}

func TestToolCallBlock_Summary(t *testing.T) {
	tests := []struct {
		name   string
		block  *ToolCallBlock
		prefix string
	}{
		{"running", &ToolCallBlock{ToolName: "bash"}, "⚙"},
		{"done", &ToolCallBlock{ToolName: "bash", Result: "done"}, "✓"},
		{"error", &ToolCallBlock{ToolName: "bash", Error: "failed"}, "✗"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.block.Summary()
			if len(s) == 0 {
				t.Error("expected non-empty summary")
			}
		})
	}
}

func TestFromAgentMessage(t *testing.T) {
	tests := []struct {
		role     string
		content  string
		wantKind BlockKind
	}{
		{"user", "hello", BlockKindUserPrompt},
		{"assistant", "hi", BlockKindAgentMessage},
		{"system", "note", BlockKindSystem},
	}
	for _, tt := range tests {
		msg := agentcore.Message{
			Role:    agentcore.Role(tt.role),
			Content: tt.content,
		}
		block := FromAgentMessage(msg)
		if block.Kind() != tt.wantKind {
			t.Errorf("role %s: expected kind %s, got %s", tt.role, tt.wantKind, block.Kind())
		}
	}
}

func TestScrollbackPipeline_RenderRunning(t *testing.T) {
	p := NewScrollbackPipeline()
	pal := theme.CurrentPalette()

	e := p.Append(&ToolCallBlock{ToolName: "grep", Args: "test"})
	p.StartRunning(e)

	lines := p.RenderRunning(pal)
	if len(lines) != 1 {
		t.Fatalf("expected 1 running line, got %d", len(lines))
	}
}
