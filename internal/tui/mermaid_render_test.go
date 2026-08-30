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
		input     string
		wantID    string
		wantText  string
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

func TestRenderMermaid_ChainedEdgesAndLabels(t *testing.T) {
	input := `graph TD
    A[Start] --> B{Gate} -->|Yes| C[OK]
    B -->|No| D[End]`
	result := RenderMermaid(input)
	for _, want := range []string{"Start", "Gate", "OK", "End", "Yes", "No"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in output:\n%s", want, result)
		}
	}
}

func TestRenderMermaid_PieChart(t *testing.T) {
	input := `pie title Pets
    "Dogs": 30
    "Cats": 20
    "Birds": 10`
	result := RenderMermaid(input)
	for _, want := range []string{"Pets", "Dogs", "Cats", "Birds", "%"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in pie chart:\n%s", want, result)
		}
	}
}

func TestRenderMermaid_SequenceDiagram(t *testing.T) {
	input := `sequenceDiagram
    participant A as Alice
    participant B as Bob
    A->>B: hello
    B-->>A: hi
    note over A,B: handshake`
	result := RenderMermaid(input)
	for _, want := range []string{"Alice", "Bob", "hello", "hi", "handshake"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in sequence diagram:\n%s", want, result)
		}
	}
	if !strings.Contains(result, "▶") && !strings.Contains(result, "◀") {
		t.Errorf("expected arrows in sequence diagram:\n%s", result)
	}
}

func TestRenderMermaid_ClassDiagram(t *testing.T) {
	input := `classDiagram
    class Animal {
        +String name
        +eat()
    }
    class Dog
    Animal <|-- Dog`
	result := RenderMermaid(input)
	for _, want := range []string{"Animal", "Dog", "name", "eat", "◁──"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in class diagram:\n%s", want, result)
		}
	}
}

func TestRenderMermaid_StateDiagram(t *testing.T) {
	input := `stateDiagram-v2
    [*] --> Still
    Still --> Moving : start
    Moving --> Still : stop`
	result := RenderMermaid(input)
	for _, want := range []string{"Still", "Moving", "start", "stop"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in state diagram:\n%s", want, result)
		}
	}
}

func TestRenderMermaid_ERDiagram(t *testing.T) {
	input := `erDiagram
    CUSTOMER ||--o{ ORDER : places
    CUSTOMER {
        string name
    }
    ORDER {
        int id
    }`
	result := RenderMermaid(input)
	for _, want := range []string{"CUSTOMER", "ORDER", "name", "id", "places"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in er diagram:\n%s", want, result)
		}
	}
}

func TestRenderMermaid_Subgraph(t *testing.T) {
	input := `graph TD
    subgraph cluster [Auth]
        A[Login] --> B[Token]
    end
    B --> C[OK]`
	result := RenderMermaid(input)
	for _, want := range []string{"Auth", "Login", "Token", "OK"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in subgraph diagram:\n%s", want, result)
		}
	}
}

func TestRenderMermaid_LongNodeLabel(t *testing.T) {
	input := `graph TD
    A[ThisIsAQuiteLongNodeLabel] --> B[OK]`
	result := RenderMermaid(input)
	if !strings.Contains(result, "ThisIsAQuiteLong") {
		t.Errorf("long node label truncated too aggressively:\n%s", result)
	}
}

func TestRenderMermaid_OneLinerChain(t *testing.T) {
	result := RenderMermaid("graph LR; A[Start] --> B[Mid] --> C[End]")
	for _, want := range []string{"Start", "Mid", "End"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in one-liner:\n%s", want, result)
		}
	}
}
