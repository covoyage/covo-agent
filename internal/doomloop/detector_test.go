package doomloop

import (
	"strings"
	"testing"
)

func TestDetector_NoLoop(t *testing.T) {
	d := New(DefaultConfig())
	det := d.RecordToolCall(ToolCall{Name: "read_file", ArgsHash: "abc", Turn: 1, Success: true})
	if det != nil {
		t.Fatalf("expected no detection, got %v", det)
	}
}

func TestDetector_IdenticalTool(t *testing.T) {
	cfg := Config{
		MaxIdenticalCalls:      3,
		MaxCycleLength:         5,
		MaxCycles:              2,
		MaxConsecutiveFailures: 3,
		MaxRecoveryBudget:      5,
		HistorySize:            100,
	}
	d := New(cfg)

	d.RecordToolCall(ToolCall{Name: "write_file", ArgsHash: "same", ResultHash: "same-result", Turn: 1, Success: true})
	d.RecordToolCall(ToolCall{Name: "write_file", ArgsHash: "same", ResultHash: "same-result", Turn: 1, Success: true})
	det := d.RecordToolCall(ToolCall{Name: "write_file", ArgsHash: "same", ResultHash: "same-result", Turn: 1, Success: true})

	if det == nil {
		t.Fatal("expected identical tool detection")
	}
	if det.Type != LoopIdenticalTool {
		t.Errorf("expected LoopIdenticalTool, got %s", det.Type)
	}
}

func TestDetector_IdenticalSuccessfulToolChangingResultIsProgress(t *testing.T) {
	d := New(DefaultConfig())
	for _, resultHash := range []string{"result-1", "result-2", "result-3"} {
		if det := d.RecordToolCall(ToolCall{Name: "bash", ArgsHash: "same", ResultHash: resultHash, Turn: 1, Success: true}); det != nil {
			t.Fatalf("changing successful result detected as loop: %+v", det)
		}
	}
}

func TestDetector_ReportsContinuousPatternOnce(t *testing.T) {
	d := New(DefaultConfig())
	call := ToolCall{Name: "bash", ArgsHash: "same", ResultHash: "same-result", Turn: 1, Success: true}
	d.RecordToolCall(call)
	d.RecordToolCall(call)
	if det := d.RecordToolCall(call); det == nil {
		t.Fatal("expected initial identical-tool detection")
	}
	if det := d.RecordToolCall(call); det != nil {
		t.Fatalf("continuous pattern reported twice: %+v", det)
	}
}

func TestDetector_Cyclical(t *testing.T) {
	cfg := Config{
		MaxIdenticalCalls:      5,
		MaxCycleLength:         3,
		MaxCycles:              2,
		MaxConsecutiveFailures: 5,
		MaxRecoveryBudget:      5,
		HistorySize:            100,
	}
	d := New(cfg)

	// Pattern: A → B → A → B (length 2, 2 cycles)
	d.RecordToolCall(ToolCall{Name: "read_file", ArgsHash: "h1", Turn: 1, Success: true})
	d.RecordToolCall(ToolCall{Name: "edit_file", ArgsHash: "h2", Turn: 1, Success: true})
	d.RecordToolCall(ToolCall{Name: "read_file", ArgsHash: "h1", Turn: 1, Success: true})
	det := d.RecordToolCall(ToolCall{Name: "edit_file", ArgsHash: "h2", Turn: 1, Success: true})

	if det == nil {
		t.Fatal("expected cyclical detection")
	}
	if det.Type != LoopCyclical {
		t.Errorf("expected LoopCyclical, got %s", det.Type)
	}
}

func TestDetector_FailingTool(t *testing.T) {
	cfg := Config{
		MaxIdenticalCalls:      10,
		MaxCycleLength:         5,
		MaxCycles:              2,
		MaxConsecutiveFailures: 3,
		MaxRecoveryBudget:      5,
		HistorySize:            100,
	}
	d := New(cfg)

	d.RecordToolCall(ToolCall{Name: "bash", ArgsHash: "cmd1", Turn: 1, Success: false, Error: "exit 1"})
	d.RecordToolCall(ToolCall{Name: "bash", ArgsHash: "cmd2", Turn: 1, Success: false, Error: "exit 1"})
	det := d.RecordToolCall(ToolCall{Name: "bash", ArgsHash: "cmd3", Turn: 1, Success: false, Error: "exit 1"})

	if det == nil {
		t.Fatal("expected failing tool detection")
	}
	if det.Type != LoopFailingTool {
		t.Errorf("expected LoopFailingTool, got %s", det.Type)
	}
}

func TestDetector_RecoveryBudget(t *testing.T) {
	d := New(DefaultConfig())

	if !d.CanRecover() {
		t.Error("expected can recover initially")
	}

	for i := 0; i < d.config.MaxRecoveryBudget; i++ {
		if !d.UseRecovery() {
			t.Fatalf("expected recovery %d to succeed", i)
		}
	}

	if d.CanRecover() {
		t.Error("expected cannot recover after budget exhausted")
	}
	if d.UseRecovery() {
		t.Error("expected UseRecovery to return false when exhausted")
	}

	remaining := d.RemainingBudget()
	if remaining != 0 {
		t.Errorf("expected 0 remaining, got %d", remaining)
	}
}

func TestDetector_Reset(t *testing.T) {
	d := New(DefaultConfig())
	d.RecordToolCall(ToolCall{Name: "test", ArgsHash: "h", Turn: 1, Success: true})
	d.UseRecovery()

	d.Reset()

	if d.RemainingBudget() != d.config.MaxRecoveryBudget {
		t.Error("expected budget reset")
	}
	if d.LastDetection() != nil {
		t.Error("expected last detection cleared")
	}
}

func TestDetector_ResetForNewTurn(t *testing.T) {
	d := New(DefaultConfig())
	call := ToolCall{Name: "test", ArgsHash: "h", ResultHash: "same", Turn: 1, Success: true}
	d.RecordToolCall(call)
	d.RecordToolCall(call)
	d.RecordToolCall(call)
	d.UseRecovery()

	d.ResetForNewTurn()

	// Recovery budget should be preserved
	if d.RemainingBudget() != d.config.MaxRecoveryBudget-1 {
		t.Error("expected budget preserved on turn reset")
	}
	if d.LastDetection() != nil {
		t.Error("expected duplicate suppression state cleared on turn reset")
	}
}

func TestDetection_GetRecoveryNudge_IdenticalTool(t *testing.T) {
	det := &Detection{
		Type: LoopIdenticalTool,
		Calls: []ToolCall{
			{Name: "write_file", ArgsHash: "same", Turn: 1, Success: true},
			{Name: "write_file", ArgsHash: "same", Turn: 1, Success: true},
			{Name: "write_file", ArgsHash: "same", Turn: 1, Success: true},
		},
	}
	nudge := det.GetRecoveryNudge()
	if nudge == "" {
		t.Error("expected non-empty nudge")
	}
	if strings.Contains(nudge, "without success") {
		t.Fatalf("successful calls described as failures: %q", nudge)
	}
}

func TestDetection_GetRecoveryNudge_Cyclical(t *testing.T) {
	det := &Detection{
		Type: LoopCyclical,
		Calls: []ToolCall{
			{Name: "read_file", ArgsHash: "h1", Turn: 1, Success: true},
			{Name: "edit_file", ArgsHash: "h2", Turn: 1, Success: true},
		},
	}
	nudge := det.GetRecoveryNudge()
	if nudge == "" {
		t.Error("expected non-empty nudge")
	}
}

func TestDetection_GetRecoveryNudge_FailingTool(t *testing.T) {
	det := &Detection{
		Type: LoopFailingTool,
		Calls: []ToolCall{
			{Name: "bash", ArgsHash: "cmd", Turn: 1, Success: false, Error: "exit 1"},
		},
	}
	nudge := det.GetRecoveryNudge()
	if nudge == "" {
		t.Error("expected non-empty nudge")
	}
}

func TestHashArgs(t *testing.T) {
	h1 := HashArgs("read file.go")
	h2 := HashArgs("  read   file.go  ")
	if h1 != h2 {
		t.Error("expected same hash for whitespace-normalized args")
	}

	long := HashArgs("very long argument string that exceeds 32 characters")
	if len(long) != 64 {
		t.Errorf("expected SHA-256 hex digest, got %d chars", len(long))
	}

	prefix := strings.Repeat("x", 40)
	if HashArgs(prefix+"first") == HashArgs(prefix+"second") {
		t.Fatal("different long arguments with common prefix collided")
	}

	if HashArgs(`{"command":"echo  a","timeout":1}`) != HashArgs(`{ "timeout": 1, "command": "echo  a" }`) {
		t.Fatal("equivalent JSON arguments should have the same hash")
	}

	if HashArgs(`{"offset":9007199254740992}`) == HashArgs(`{"offset":9007199254740993}`) {
		t.Fatal("distinct large JSON integers collided")
	}
}

func TestLoopType_String(t *testing.T) {
	if LoopNone.String() != "none" {
		t.Error("bad string")
	}
	if LoopIdenticalTool.String() != "identical_tool" {
		t.Error("bad string")
	}
	if LoopCyclical.String() != "cyclical" {
		t.Error("bad string")
	}
	if LoopFailingTool.String() != "failing_tool" {
		t.Error("bad string")
	}
}

func TestDetector_HistorySize(t *testing.T) {
	d := New(Config{
		MaxIdenticalCalls:      100,
		MaxCycleLength:         5,
		MaxCycles:              2,
		MaxConsecutiveFailures: 100,
		MaxRecoveryBudget:      5,
		HistorySize:            10,
	})

	for i := 0; i < 20; i++ {
		d.RecordToolCall(ToolCall{
			Name:     "test",
			ArgsHash: "unique_" + string(rune('a'+i)),
			Turn:     1,
			Success:  true,
		})
	}
	// Should not have overflowed
}

func TestDetector_LastDetection(t *testing.T) {
	d := New(Config{
		MaxIdenticalCalls:      2,
		MaxCycleLength:         5,
		MaxCycles:              2,
		MaxConsecutiveFailures: 5,
		MaxRecoveryBudget:      5,
		HistorySize:            100,
	})

	d.RecordToolCall(ToolCall{Name: "test", ArgsHash: "h", Turn: 1, Success: true})
	d.RecordToolCall(ToolCall{Name: "test", ArgsHash: "h", Turn: 1, Success: true})

	if d.LastDetection() == nil {
		t.Error("expected last detection set")
	}
	if d.LastDetection().Type != LoopIdenticalTool {
		t.Errorf("expected identical tool, got %s", d.LastDetection().Type)
	}
}
