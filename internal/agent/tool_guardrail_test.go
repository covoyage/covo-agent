package agent

import (
	"encoding/json"
	"testing"
)

func TestStepSignature_KeyOrderInvariant(t *testing.T) {
	// Same content, different key order → same signature.
	sigA := computeStepSignature("read", json.RawMessage(`{"path":"foo.go","line":10}`))
	sigB := computeStepSignature("read", json.RawMessage(`{"line":10,"path":"foo.go"}`))
	if sigA != sigB {
		t.Errorf("signatures should match regardless of JSON key order:\n  %s\n  %s", sigA, sigB)
	}
}

func TestStepSignature_DifferentArgsDiffer(t *testing.T) {
	sigA := computeStepSignature("read", json.RawMessage(`{"path":"a.go"}`))
	sigB := computeStepSignature("read", json.RawMessage(`{"path":"b.go"}`))
	if sigA == sigB {
		t.Error("different args must produce different signatures")
	}
}

func TestStepSignature_DifferentToolDiffer(t *testing.T) {
	sigA := computeStepSignature("read", json.RawMessage(`{"path":"a.go"}`))
	sigB := computeStepSignature("grep", json.RawMessage(`{"path":"a.go"}`))
	if sigA == sigB {
		t.Error("different tool names must produce different signatures")
	}
}

func TestStepSignature_NestedKeyOrderInvariant(t *testing.T) {
	// Nested object key order should also normalize.
	sigA := computeStepSignature("bash", json.RawMessage(`{"cmd":"x","opts":{"v":true,"n":3}}`))
	sigB := computeStepSignature("bash", json.RawMessage(`{"opts":{"n":3,"v":true},"cmd":"x"}`))
	if sigA != sigB {
		t.Errorf("nested key order should normalize:\n  %s\n  %s", sigA, sigB)
	}
}

func TestStepSignature_InvalidJSONFallback(t *testing.T) {
	// Invalid JSON falls back to raw bytes — two identical raw blobs still match.
	sigA := computeStepSignature("bash", json.RawMessage(`not json`))
	sigB := computeStepSignature("bash", json.RawMessage(`not json`))
	if sigA != sigB {
		t.Error("identical invalid JSON should still match via raw-bytes fallback")
	}
}

func TestGuardrail_DetectsDuplicateAcrossKeyOrder(t *testing.T) {
	// The model may emit the same call with keys in different order across
	// turns; the guardrail must still flag the repetition.
	g := NewToolGuardrail()
	argsA := json.RawMessage(`{"path":"foo.go","line":10}`)
	argsB := json.RawMessage(`{"line":10,"path":"foo.go"}`) // same content, reordered

	// Calls 1-3: allow (WarnAfter=3 means warn on the 4th).
	for i, args := range []json.RawMessage{argsA, argsB, argsA} {
		if d := g.Check("read", args); d != GuardrailAllow {
			t.Fatalf("call %d: expected allow, got %s", i+1, d)
		}
		g.Record("read", args)
	}
	// Call 4: consecutive count is now 3 → warn.
	if d := g.Check("read", argsB); d != GuardrailWarn {
		t.Fatalf("expected warn on 4th consecutive identical call across key order, got %s", d)
	}
}

func TestGuardrail_DifferentArgsResetsConsecutive(t *testing.T) {
	g := NewToolGuardrail()
	foo := json.RawMessage(`{"path":"foo.go"}`)
	bar := json.RawMessage(`{"path":"bar.go"}`)

	for i := 0; i < 3; i++ {
		g.Check("read", foo)
		g.Record("read", foo)
	}
	// A different call breaks the run.
	g.Check("read", bar)
	g.Record("read", bar)
	// Same args as before, but consecutive count reset to 0.
	if d := g.Check("read", foo); d != GuardrailAllow {
		t.Fatalf("different args should reset consecutive count, got %s", d)
	}
}

func TestGuardrail_BlockAfterManyDuplicates(t *testing.T) {
	g := NewToolGuardrail()
	args := json.RawMessage(`{"path":"foo.go"}`)
	// 14 calls: warn starts at 4 (consecutive=3), but not yet block.
	for i := 0; i < 14; i++ {
		switch d := g.Check("read", args); d {
		case GuardrailBlock:
			t.Fatalf("call %d: unexpected early block", i+1)
		}
		g.Record("read", args)
	}
	// 15th call: consecutive=14, still under BlockAfter=15 → warn.
	// 16th call: consecutive=15 → block.
	if d := g.Check("read", args); d != GuardrailWarn {
		t.Fatalf("call 15: expected warn, got %s", d)
	}
	g.Record("read", args)
	if d := g.Check("read", args); d != GuardrailBlock {
		t.Fatalf("call 16: expected block, got %s", d)
	}
}

// TestGuardrail_GlobalDuplicate_ColdStartExemptionForReadOnlyTools verifies
// that non-consecutive repeats of a read-only tool call are NOT flagged
// before any mutating tool has run. A prompt like "summarize this project"
// legitimately re-reads files in a scattered (non-consecutive) pattern
// during initial exploration; that should never trip the global-duplicate
// guard.
func TestGuardrail_GlobalDuplicate_ColdStartExemptionForReadOnlyTools(t *testing.T) {
	g := NewToolGuardrail()
	foo := json.RawMessage(`{"path":"foo.go"}`)
	bar := json.RawMessage(`{"path":"bar.go"}`)

	// Interleave "read foo" with "read bar" many times (never consecutive,
	// so the exact-duplicate check can't fire) — well past
	// GlobalDuplicateWarnAfter(6) and GlobalDuplicateHaltAfter(10) for foo.
	for i := 0; i < 15; i++ {
		if d := g.Check("read", foo); d != GuardrailAllow {
			t.Fatalf("iteration %d: expected allow during cold-start read-only exploration, got %s", i, d)
		}
		g.Record("read", foo)
		if d := g.Check("read", bar); d != GuardrailAllow {
			t.Fatalf("iteration %d: expected allow during cold-start read-only exploration, got %s", i, d)
		}
		g.Record("read", bar)
	}
}

// TestGuardrail_GlobalDuplicate_FiresAfterMutatingToolSeen verifies that
// once a mutating tool call has been recorded (the cold-start exploration
// phase is over), non-consecutive repeats of the same read-only call DO
// count toward the global-duplicate warn/halt thresholds. "read foo" is
// interleaved with "read bar" for the *entire* sequence (never consecutive)
// so only the global check can fire — a consecutive run of "read foo" would
// trip the (unrelated, higher-priority) exact-duplicate check instead and
// mask what we're testing here.
func TestGuardrail_GlobalDuplicate_FiresAfterMutatingToolSeen(t *testing.T) {
	g := NewToolGuardrail()
	foo := json.RawMessage(`{"path":"foo.go"}`)
	bar := json.RawMessage(`{"path":"bar.go"}`)
	writeArgs := json.RawMessage(`{"path":"x.go","content":"y"}`)

	// Arm the cold-start exemption: a mutating tool call has happened.
	g.Check("write_file", writeArgs)
	g.Record("write_file", writeArgs)

	// 9 interleaved (foo, bar) pairs: "read foo" reaches
	// GlobalDuplicateWarnAfter(6) on its 6th occurrence and
	// GlobalDuplicateHaltAfter(10) on its 10th (9 recorded here + 1 more
	// below), all while never running consecutively.
	var lastFooDecision GuardrailDecision
	for i := 0; i < 9; i++ {
		lastFooDecision = g.Check("read", foo)
		g.Record("read", foo)
		g.Check("read", bar)
		g.Record("read", bar)

		switch {
		case i < 5:
			if lastFooDecision != GuardrailAllow {
				t.Fatalf("occurrence %d: expected allow below the warn threshold, got %s", i+1, lastFooDecision)
			}
		default:
			if lastFooDecision != GuardrailWarn {
				t.Fatalf("occurrence %d: expected warn once past the global duplicate threshold, got %s", i+1, lastFooDecision)
			}
		}
	}

	// 10th non-consecutive occurrence of "read foo" → reaches
	// GlobalDuplicateHaltAfter(10).
	if d := g.Check("read", foo); d != GuardrailHalt {
		t.Fatalf("expected halt once the global duplicate halt threshold is reached, got %s", d)
	}
	if !g.IsHalted() {
		t.Fatal("expected IsHalted() to be true after a GuardrailHalt decision")
	}
}
