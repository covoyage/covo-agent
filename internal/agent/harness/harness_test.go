package harness

import (
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestHarnessBasic(t *testing.T) {
	sc := &Scenario{
		Name: "basic",
		Steps: []ScenarioStep{
			{
				User: "hello",
				Mock: []MockTurn{
					{Content: "Hi there!"},
				},
			},
		},
	}

	h := New(t, sc)
	h.Run()

	calls := h.RecordedCalls()
	if len(calls) == 0 {
		t.Fatal("expected at least one LLM call")
	}
	if calls[0].Response.Content != "Hi there!" {
		t.Errorf("expected content 'Hi there!', got %q", calls[0].Response.Content)
	}
}

func TestHarnessToolCall(t *testing.T) {
	sc := &Scenario{
		Name: "tool-call",
		Steps: []ScenarioStep{
			{
				User: "what is the time?",
				Mock: []MockTurn{
					{
						ToolCalls: []agentcore.ToolCall{
							{ID: "call_1", Name: "current_time", Arguments: `{}`},
						},
					},
					{
						Content: "The current time is now.",
					},
				},
			},
		},
	}

	h := New(t, sc)
	h.Run()

	h.RequireToolCalled("current_time")
}

func TestHarnessMultiStep(t *testing.T) {
	sc := &Scenario{
		Name: "multi-step",
		Steps: []ScenarioStep{
			{
				User: "list files",
				Mock: []MockTurn{
					{Content: "Let me check which files exist."},
				},
			},
			{
				User: "show the config",
				Mock: []MockTurn{
					{Content: "Here is the configuration."},
				},
			},
		},
	}

	h := New(t, sc)
	h.Run()

	calls := h.RecordedCalls()
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 LLM calls, got %d", len(calls))
	}
}

func TestHarnessRequirePromptContains(t *testing.T) {
	sc := &Scenario{
		Name: "prompt-check",
		Steps: []ScenarioStep{
			{
				User: "explain the architecture",
				Mock: []MockTurn{
					{Content: "The system uses a modular design."},
				},
			},
		},
	}

	h := New(t, sc)
	h.Run()

	h.RequirePromptContains("architecture")
}

func TestHarnessMultipleToolCalls(t *testing.T) {
	sc := &Scenario{
		Name: "multi-tool",
		Steps: []ScenarioStep{
			{
				User: "search and summarize",
				Mock: []MockTurn{
					{
						ToolCalls: []agentcore.ToolCall{
							{ID: "s1", Name: "search", Arguments: `{"q":"go"}`},
						},
					},
					{
						ToolCalls: []agentcore.ToolCall{
							{ID: "s2", Name: "read_file", Arguments: `{"path":"doc.go"}`},
						},
					},
					{
						Content: "Here are the results.",
					},
				},
			},
		},
	}

	h := New(t, sc)
	h.Run()

	h.RequireToolCalled("search")
	h.RequireToolCalled("read_file")
}
