package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestCompactInventoryOutput_ShortUnchanged(t *testing.T) {
	in := strings.Repeat("file.go\n", 10)
	got, ok := CompactInventoryOutput(in)
	if ok || got != in {
		t.Fatalf("ok=%v got len=%d", ok, len(got))
	}
}

func TestCompactInventoryOutput_Inventory(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString("src/pkg/subdir/file_")
		b.WriteByte(byte('a' + i%26))
		b.WriteString("_extra.go\n")
	}
	in := b.String()
	got, ok := CompactInventoryOutput(in)
	if !ok {
		t.Fatal("expected compact")
	}
	if !strings.Contains(got, "lines omitted") {
		t.Fatalf("got %q", got[:min(200, len(got))])
	}
	if len(got) >= len(in) {
		t.Fatalf("did not shrink: %d vs %d", len(got), len(in))
	}
	if strings.Count(got, "\n") > 200 {
		t.Fatalf("still too many lines: %d", strings.Count(got, "\n"))
	}
}

func TestCompactInventoryOutput_LongLinesStayRaw(t *testing.T) {
	long := strings.Repeat("func veryLongLineOfSourceCode() { return 1 } // "+strings.Repeat("x", 80)+"\n", 80)
	got, ok := CompactInventoryOutput(long)
	if ok {
		t.Fatalf("file-like content should stay raw, got %d bytes", len(got))
	}
}

func TestCompactExecAfterToolCall(t *testing.T) {
	ca := &CovoAgent{}
	hook := ca.compactExecAfterToolCall()
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString("src/pkg/subdir/file_extra.go\n")
	}
	res := &agentcore.ToolResult{Result: b.String()}
	got := hook(context.Background(), agentcore.ToolCall{Name: "process"}, res)
	if got == nil || !strings.Contains(got.Result, "lines omitted") {
		t.Fatalf("got %+v", got)
	}
	if hook(context.Background(), agentcore.ToolCall{Name: "read"}, res) != nil {
		t.Fatal("read should not compact")
	}
}
