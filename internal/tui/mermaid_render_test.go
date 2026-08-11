package tui

import (
	"strings"
	"testing"
)

func TestRenderMermaid_SimpleFlowchart(t *testing.T) {
	input := `graph TD
    A[Start] --> B{Is it?}
    B -->|Yes| C[OK]
    B -->|No| D[End]`

	result := RenderMermaid(input)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result, "Start") {
		t.Error("expected 'Start' in output")
	}
	if !strings.Contains(result, "OK") {
		t.Error("expected 'OK' in output")
	}
	if !strings.Contains(result, "End") {
		t.Error("expected 'End' in output")
	}
}

func TestRenderMermaid_LR(t *testing.T) {
	input := `graph LR
    A --> B --> C`

	result := RenderMermaid(input)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result, "▶") || !strings.Contains(result, "─") {
		t.Error("expected arrow characters in LR output")
	}
}

func TestRenderMermaid_Empty(t *testing.T) {
	result := RenderMermaid("")
	if result == "" {
		t.Fatal("expected non-empty result even for empty input")
	}
}

func TestRenderMermaid_NodeShapes(t *testing.T) {
	input := `graph TD
    A[Rectangle] --> B{Diamond}
    B --> C(Round)`

	result := RenderMermaid(input)
	if !strings.Contains(result, "Rectangle") {
		t.Error("expected 'Rectangle' in output")
	}
	if !strings.Contains(result, "Diamond") {
		t.Error("expected 'Diamond' in output")
	}
}

func TestParseNodeDef(t *testing.T) {
	tests := []struct {
		input    string
		wantID   string
		wantText string
		wantShape string
	}{
		{"A[Hello]", "A", "Hello", "rect"},
		{"B{Decision}", "B", "Decision", "diamond"},
		{"C(Round)", "C", "Round", "round"},
		{"D", "D", "D", "rect"},
		{"E([Stadium])", "E", "Stadium", "stadium"},
	}

	for _, tt := range tests {
		id, text, shape := parseNodeDef(tt.input)
		if id != tt.wantID {
			t.Errorf("parseNodeDef(%q) id = %q, want %q", tt.input, id, tt.wantID)
		}
		if text != tt.wantText {
			t.Errorf("parseNodeDef(%q) text = %q, want %q", tt.input, text, tt.wantText)
		}
		if shape != tt.wantShape {
			t.Errorf("parseNodeDef(%q) shape = %q, want %q", tt.input, shape, tt.wantShape)
		}
	}
}

func TestRenderBox(t *testing.T) {
	n := mermaidNode{id: "A", text: "Test", shape: "rect"}
	box := renderBox(n)
	if !strings.Contains(box, "Test") {
		t.Error("expected 'Test' in box")
	}
	if !strings.Contains(box, "┌") {
		t.Error("expected top-left corner in rect box")
	}
}
