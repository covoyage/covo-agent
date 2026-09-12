package evolution

import (
	"strings"
	"testing"
)

func toolStep(content string) trajMessage {
	return trajMessage{Role: "assistant", Content: content, ToolStep: true}
}

// TestCompressTrajectoryFitsBudget verifies head/tail protection and that
// middle content is dropped.
func TestCompressTrajectoryFitsBudget(t *testing.T) {
	var msgs []trajMessage
	msgs = append(msgs, trajMessage{Role: "user", Content: strings.Repeat("A", 400)}) // head: task
	for i := 0; i < 20; i++ {                                                         // middle: tool steps
		msgs = append(msgs, toolStep("[Assistant invoked tools]"))
		msgs = append(msgs, trajMessage{Role: "tool", Content: strings.Repeat("t", 400)})
	}
	msgs = append(msgs, trajMessage{Role: "assistant", Content: strings.Repeat("Z", 400)}) // tail: resolution

	// Budget fits head + tail + a few middle units, not all of them.
	kept, dropped := CompressTrajectory(msgs, 3000, 2, 4)
	if dropped == 0 {
		t.Fatal("expected middle messages to be dropped")
	}
	out := renderKept(kept)
	total := 0
	for _, m := range kept {
		total += len(m.Content) + 1
	}
	if total > 3000 {
		t.Fatalf("kept size %d exceeds budget 3000", total)
	}
	if !strings.HasPrefix(out, "AAAA") {
		t.Fatalf("expected head (task) preserved first, got %q", out[:min(len(out), 40)])
	}
	if !strings.Contains(out, "AAAA") || !strings.Contains(out, "ZZZZ") {
		t.Fatal("expected head and tail content preserved")
	}
	if !strings.Contains(out, "middle of trajectory omitted") {
		t.Fatal("expected omission marker")
	}
}

// TestCompressTrajectoryKeepsPairs verifies a tool result is never kept
// without the tool-call step that produced it, and vice versa.
func TestCompressTrajectoryKeepsPairs(t *testing.T) {
	var msgs []trajMessage
	msgs = append(msgs, trajMessage{Role: "user", Content: strings.Repeat("A", 400)})
	for i := 0; i < 20; i++ {
		msgs = append(msgs, toolStep("[Assistant invoked tools]"))
		msgs = append(msgs, trajMessage{Role: "tool", Content: strings.Repeat("t", 400)})
	}
	msgs = append(msgs, trajMessage{Role: "assistant", Content: strings.Repeat("Z", 400)})

	kept, _ := CompressTrajectory(msgs, 3000, 2, 4)
	for i, m := range kept {
		if m.Role == "tool" {
			if i == 0 || kept[i-1].Role != "assistant" || !kept[i-1].ToolStep {
				t.Fatalf("tool result at %d kept without its call: %+v after %+v", i, m, kept[i-1])
			}
		}
		if m.ToolStep && i+1 < len(kept) && kept[i+1].Role == "tool" {
			continue
		}
	}
}

// TestCompressTrajectorySmall verifies no compression when everything fits.
func TestCompressTrajectorySmall(t *testing.T) {
	msgs := []trajMessage{
		{Role: "user", Content: "fix the bug"},
		toolStep("[Assistant invoked tools]"),
		{Role: "tool", Content: "ok"},
	}
	kept, dropped := CompressTrajectory(msgs, 8000, 2, 4)
	if dropped != 0 || len(kept) != len(msgs) {
		t.Fatalf("expected untouched output, got dropped=%d kept=%d", dropped, len(kept))
	}
}

func renderKept(kept []trajMessage) string {
	var b strings.Builder
	for _, m := range kept {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}
