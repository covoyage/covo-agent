package subagent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/covoyage/covonaut/agentcore"
)

// --- PreStop hook tests ---

func TestPreStopHook_NoRerunWhenSatisfied(t *testing.T) {
	var hookCalls atomic.Int32
	runner := NewSubagentRunner(SubagentRunnerConfig{
		PreStopHook: func(ctx context.Context, result SubagentVerificationResult) SubagentPreStopDecision {
			hookCalls.Add(1)
			return SubagentPreStopDecision{ForceRerun: false}
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		return "initial output", nil
	}

	output, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "initial output" {
		t.Errorf("expected original output, got %q", output)
	}
	if hookCalls.Load() != 1 {
		t.Errorf("expected 1 PreStopHook call, got %d", hookCalls.Load())
	}
}

func TestPreStopHook_ForcesRerun(t *testing.T) {
	var spawnCalls atomic.Int32
	runner := NewSubagentRunner(SubagentRunnerConfig{
		MaxReruns: 3,
		PreStopHook: func(ctx context.Context, result SubagentVerificationResult) SubagentPreStopDecision {
			// Force rerun on first call (rerunCount=0), satisfied on second (rerunCount=1).
			if result.RerunCount == 0 && !strings.Contains(result.Output, "rerun output") {
				return SubagentPreStopDecision{
					ForceRerun: true,
					Feedback:   "Output was incomplete, please provide full result.",
					Reason:     "incomplete output",
				}
			}
			return SubagentPreStopDecision{ForceRerun: false}
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		n := spawnCalls.Add(1)
		if n == 1 {
			return "initial output", nil
		}
		// Verify feedback was appended to task
		if !strings.Contains(task, "Feedback from verification") {
			t.Errorf("expected feedback in rerun task, got: %s", task)
		}
		return "rerun output - complete", nil
	}

	output, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "rerun output - complete" {
		t.Errorf("expected rerun output, got %q", output)
	}
	if spawnCalls.Load() != 2 {
		t.Errorf("expected 2 spawn calls, got %d", spawnCalls.Load())
	}
}

func TestPreStopHook_RespectsMaxReruns(t *testing.T) {
	var spawnCalls atomic.Int32
	runner := NewSubagentRunner(SubagentRunnerConfig{
		MaxReruns: 2,
		PreStopHook: func(ctx context.Context, result SubagentVerificationResult) SubagentPreStopDecision {
			// Always force rerun — should hit the cap.
			return SubagentPreStopDecision{
				ForceRerun: true,
				Feedback:   "Still incomplete.",
				Reason:     "always rerun",
			}
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		spawnCalls.Add(1)
		return "output", nil
	}

	output, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have run 1 initial + 2 reruns = 3 total
	if spawnCalls.Load() != 3 {
		t.Errorf("expected 3 spawn calls (1 initial + 2 reruns), got %d", spawnCalls.Load())
	}
	if output != "output" {
		t.Errorf("expected last output, got %q", output)
	}
}

func TestPreStopHook_NotCalledOnError(t *testing.T) {
	var hookCalls atomic.Int32
	runner := NewSubagentRunner(SubagentRunnerConfig{
		PreStopHook: func(ctx context.Context, result SubagentVerificationResult) SubagentPreStopDecision {
			hookCalls.Add(1)
			return SubagentPreStopDecision{ForceRerun: false}
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		return "", context.Canceled
	}

	_, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err == nil {
		t.Fatal("expected error from failed spawn")
	}
	if hookCalls.Load() != 0 {
		t.Errorf("expected 0 PreStopHook calls on error, got %d", hookCalls.Load())
	}
}

func TestPreStopHook_RerunFailsKeepsOriginalOutput(t *testing.T) {
	runner := NewSubagentRunner(SubagentRunnerConfig{
		MaxReruns: 3,
		PreStopHook: func(ctx context.Context, result SubagentVerificationResult) SubagentPreStopDecision {
			if result.RerunCount == 0 {
				return SubagentPreStopDecision{
					ForceRerun: true,
					Feedback:   "Please improve.",
					Reason:     "needs improvement",
				}
			}
			return SubagentPreStopDecision{ForceRerun: false}
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		if strings.Contains(task, "Feedback from verification") {
			// Rerun fails
			return "", context.Canceled
		}
		return "original output", nil
	}

	output, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should keep the original output since rerun failed
	if output != "original output" {
		t.Errorf("expected original output when rerun fails, got %q", output)
	}
}

// --- TruthChecker tests ---

func TestTruthChecker_DowngradeToPartial(t *testing.T) {
	runner := NewSubagentRunner(SubagentRunnerConfig{
		TruthChecker: func(ctx context.Context, output, task string) (string, string) {
			return StatusPartial, "2 of 3 kanban tasks still incomplete"
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		return "All done!", nil
	}

	output, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(output, "[⚠️ PARTIAL") {
		t.Errorf("expected partial annotation, got: %s", output)
	}
	if !strings.Contains(output, "2 of 3 kanban tasks still incomplete") {
		t.Errorf("expected reason in output, got: %s", output)
	}
	if !strings.Contains(output, "All done!") {
		t.Errorf("expected original output preserved, got: %s", output)
	}
}

func TestTruthChecker_DowngradeToFailure(t *testing.T) {
	runner := NewSubagentRunner(SubagentRunnerConfig{
		TruthChecker: func(ctx context.Context, output, task string) (string, string) {
			return StatusFailure, "no tasks completed at all"
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		return "I finished everything", nil
	}

	output, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(output, "[❌ FAILED") {
		t.Errorf("expected failure annotation, got: %s", output)
	}
}

func TestTruthChecker_NoDowngradeWhenSuccess(t *testing.T) {
	runner := NewSubagentRunner(SubagentRunnerConfig{
		TruthChecker: func(ctx context.Context, output, task string) (string, string) {
			return StatusSuccess, ""
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		return "clean output", nil
	}

	output, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "clean output" {
		t.Errorf("expected unmodified output, got: %s", output)
	}
}

func TestTruthChecker_NotCalledOnError(t *testing.T) {
	var checkerCalls atomic.Int32
	runner := NewSubagentRunner(SubagentRunnerConfig{
		TruthChecker: func(ctx context.Context, output, task string) (string, string) {
			checkerCalls.Add(1)
			return StatusSuccess, ""
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		return "", context.Canceled
	}

	_, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if checkerCalls.Load() != 0 {
		t.Errorf("expected 0 TruthChecker calls on error, got %d", checkerCalls.Load())
	}
}

// --- PostStop hook tests ---

func TestPostStopHook_CalledAfterRun(t *testing.T) {
	var postStopCalls atomic.Int32
	var capturedOutput string
	runner := NewSubagentRunner(SubagentRunnerConfig{
		PostStopHook: func(ctx context.Context, result SubagentVerificationResult) {
			postStopCalls.Add(1)
			capturedOutput = result.Output
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		return "final output", nil
	}

	output, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if postStopCalls.Load() != 1 {
		t.Errorf("expected 1 PostStopHook call, got %d", postStopCalls.Load())
	}
	if capturedOutput != "final output" {
		t.Errorf("expected captured output %q, got %q", "final output", capturedOutput)
	}
	if output != "final output" {
		t.Errorf("expected return output %q, got %q", "final output", output)
	}
}

func TestPostStopHook_ReceivesAnnotatedOutput(t *testing.T) {
	var capturedOutput string
	runner := NewSubagentRunner(SubagentRunnerConfig{
		TruthChecker: func(ctx context.Context, output, task string) (string, string) {
			return StatusPartial, "incomplete"
		},
		PostStopHook: func(ctx context.Context, result SubagentVerificationResult) {
			capturedOutput = result.Output
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		return "original", nil
	}

	_, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// PostStop should receive the truth-checker-annotated output
	if !strings.HasPrefix(capturedOutput, "[⚠️ PARTIAL") {
		t.Errorf("expected PostStop to receive annotated output, got: %s", capturedOutput)
	}
}

// --- Combined PreStop + TruthChecker test ---

func TestPreStopAndTruthChecker_Combined(t *testing.T) {
	var spawnCalls atomic.Int32
	runner := NewSubagentRunner(SubagentRunnerConfig{
		MaxReruns: 3,
		PreStopHook: func(ctx context.Context, result SubagentVerificationResult) SubagentPreStopDecision {
			if result.RerunCount == 0 {
				return SubagentPreStopDecision{
					ForceRerun: true,
					Feedback:   "Please add more detail.",
					Reason:     "too brief",
				}
			}
			return SubagentPreStopDecision{ForceRerun: false}
		},
		TruthChecker: func(ctx context.Context, output, task string) (string, string) {
			if strings.Contains(output, "partial marker") {
				return StatusPartial, "found partial marker in output"
			}
			return StatusSuccess, ""
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		n := spawnCalls.Add(1)
		if n == 1 {
			return "brief output", nil
		}
		return "detailed output with partial marker", nil
	}

	output, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have rerun once (PreStopHook forced it), then truth checker downgraded
	if spawnCalls.Load() != 2 {
		t.Errorf("expected 2 spawn calls, got %d", spawnCalls.Load())
	}
	if !strings.HasPrefix(output, "[⚠️ PARTIAL") {
		t.Errorf("expected partial annotation from truth checker, got: %s", output)
	}
}

// --- Bug-fix regression tests ---

// TestPostStopHook_ReceivesCorrectRerunCount verifies Bug 1: PostStopHook must
// receive the actual number of reruns that occurred, not a hardcoded 0.
func TestPostStopHook_ReceivesCorrectRerunCount(t *testing.T) {
	var spawnCalls atomic.Int32
	var hookCalls atomic.Int32
	var capturedRerunCount int
	var capturedOutput string

	runner := NewSubagentRunner(SubagentRunnerConfig{
		MaxReruns: 3,
		PreStopHook: func(ctx context.Context, result SubagentVerificationResult) SubagentPreStopDecision {
			hookCalls.Add(1)
			// Force rerun for the first two runs (rerunCount 0 and 1), then
			// accept on rerunCount 2. Total reruns = 2.
			if result.RerunCount < 2 {
				return SubagentPreStopDecision{
					ForceRerun: true,
					Feedback:   "Needs more detail.",
					Reason:     "too brief",
				}
			}
			return SubagentPreStopDecision{ForceRerun: false}
		},
		PostStopHook: func(ctx context.Context, result SubagentVerificationResult) {
			capturedRerunCount = result.RerunCount
			capturedOutput = result.Output
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		n := spawnCalls.Add(1)
		return fmt.Sprintf("output-v%d", n), nil
	}

	output, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1 initial + 2 reruns = 3 spawn calls.
	if spawnCalls.Load() != 3 {
		t.Errorf("expected 3 spawn calls (1 initial + 2 reruns), got %d", spawnCalls.Load())
	}
	// PreStopHook called once per run: 3 calls.
	if hookCalls.Load() != 3 {
		t.Errorf("expected 3 PreStopHook calls, got %d", hookCalls.Load())
	}
	// PostStopHook must reflect the 2 reruns that actually happened.
	if capturedRerunCount != 2 {
		t.Errorf("PostStopHook RerunCount = %d, want 2", capturedRerunCount)
	}
	// PostStopHook should receive the final (rerun) output.
	if capturedOutput != "output-v3" {
		t.Errorf("PostStopHook output = %q, want %q", capturedOutput, "output-v3")
	}
	if output != "output-v3" {
		t.Errorf("returned output = %q, want %q", output, "output-v3")
	}
}

// TestPostStopHook_RerunCountZeroWhenNoRerun verifies that when no rerun occurs
// (PreStopHook satisfied immediately), PostStopHook receives RerunCount=0.
func TestPostStopHook_RerunCountZeroWhenNoRerun(t *testing.T) {
	var capturedRerunCount = -1 // sentinel to detect "not called"

	runner := NewSubagentRunner(SubagentRunnerConfig{
		MaxReruns: 3,
		PreStopHook: func(ctx context.Context, result SubagentVerificationResult) SubagentPreStopDecision {
			return SubagentPreStopDecision{ForceRerun: false}
		},
		PostStopHook: func(ctx context.Context, result SubagentVerificationResult) {
			capturedRerunCount = result.RerunCount
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		return "done", nil
	}

	_, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedRerunCount != 0 {
		t.Errorf("PostStopHook RerunCount = %d, want 0", capturedRerunCount)
	}
}

// TestPreStopHook_MaxRerunsZeroDisablesRerun verifies Bug 2: when MaxReruns=0,
// the PreStopHook is still called once but cannot force a rerun, even if it
// returns ForceRerun=true.
func TestPreStopHook_MaxRerunsZeroDisablesRerun(t *testing.T) {
	var spawnCalls atomic.Int32
	var hookCalls atomic.Int32
	var postStopRerunCount = -1

	runner := NewSubagentRunner(SubagentRunnerConfig{
		MaxReruns: 0, // explicitly disabled
		PreStopHook: func(ctx context.Context, result SubagentVerificationResult) SubagentPreStopDecision {
			hookCalls.Add(1)
			// Try to force a rerun — must be ignored because MaxReruns=0.
			return SubagentPreStopDecision{
				ForceRerun: true,
				Feedback:   "ignored",
				Reason:     "should not rerun",
			}
		},
		PostStopHook: func(ctx context.Context, result SubagentVerificationResult) {
			postStopRerunCount = result.RerunCount
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		spawnCalls.Add(1)
		return "only output", nil
	}

	output, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the initial run — no reruns despite ForceRerun=true.
	if spawnCalls.Load() != 1 {
		t.Errorf("expected 1 spawn call (no rerun), got %d", spawnCalls.Load())
	}
	// PreStopHook still called exactly once.
	if hookCalls.Load() != 1 {
		t.Errorf("expected 1 PreStopHook call, got %d", hookCalls.Load())
	}
	if output != "only output" {
		t.Errorf("expected original output, got %q", output)
	}
	// PostStopHook should see 0 reruns.
	if postStopRerunCount != 0 {
		t.Errorf("PostStopHook RerunCount = %d, want 0", postStopRerunCount)
	}
}

// TestTruncateRunes_PreservesValidUTF8ForCJK verifies Bug 3: rune-aware
// truncation never produces invalid UTF-8, even for multi-byte CJK content.
func TestTruncateRunes_PreservesValidUTF8ForCJK(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxRunes int
	}{
		{"short cjk no truncation", "你好世界", 10},
		{"exact cjk length", "你好世界", 4},
		{"truncate cjk mid-string", "你好世界你好世界", 3}, // 8 runes -> 3
		{"truncate cjk to one", "你好世界", 1},
		{"mixed ascii and cjk", "abc你好世界def", 5},
		{"emoji (4-byte runes)", "👋🌍🌙✨🌟", 2},
		{"empty string", "", 5},
		{"maxRunes zero", "你好", 0},
		{"maxRunes negative", "你好", -1},
		{"long cjk content", strings.Repeat("你好", 200), 200}, // 400 runes -> 200
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateRunes(tt.input, tt.maxRunes)

			// The result must always be valid UTF-8 — this is the core guarantee.
			if !utf8.ValidString(result) {
				t.Errorf("truncateRunes produced invalid UTF-8: %q", result)
			}

			// Verify the rune count does not exceed maxRunes (plus the optional
			// ellipsis, which is a single rune).
			resultRunes := []rune(result)
			inputRunes := []rune(tt.input)
			if tt.maxRunes <= 0 {
				if result != "" {
					t.Errorf("maxRunes=%d: expected empty result, got %q", tt.maxRunes, result)
				}
				return
			}
			if len(inputRunes) <= tt.maxRunes {
				// No truncation — result equals input.
				if result != tt.input {
					t.Errorf("input shorter than maxRunes: expected %q, got %q", tt.input, result)
				}
				return
			}
			// Truncation occurred: maxRunes runes + "…" (1 rune).
			if len(resultRunes) != tt.maxRunes+1 {
				t.Errorf("expected %d runes (truncated + ellipsis), got %d", tt.maxRunes+1, len(resultRunes))
			}
			// The truncated prefix must be a prefix of the input runes.
			for i := 0; i < tt.maxRunes; i++ {
				if resultRunes[i] != inputRunes[i] {
					t.Errorf("rune mismatch at %d: got %q want %q", i, resultRunes[i], inputRunes[i])
					break
				}
			}
			// Last rune should be the ellipsis.
			if resultRunes[len(resultRunes)-1] != '…' {
				t.Errorf("expected trailing ellipsis, got %q", resultRunes[len(resultRunes)-1])
			}
		})
	}

	// Sanity check: naive byte-slicing WOULD have produced invalid UTF-8 here,
	// demonstrating why the rune-aware version is needed.
	bad := "你好世界"[:5] // splits the second CJK char (3-byte) mid-sequence
	if utf8.ValidString(bad) {
		t.Errorf("expected byte-sliced CJK to be invalid UTF-8, but it was valid: %q", bad)
	}
}

// TestSummarizeParentState_CJKValidUTF8 verifies Bug 3 in the context of
// SummarizeParentState, which truncates user/assistant content. Long CJK
// content must not produce invalid UTF-8 in the summary.
func TestSummarizeParentState_CJKValidUTF8(t *testing.T) {
	// 800 runes (2400 bytes) — exceeds both the 200-rune (user) and 500-rune
	// (assistant) truncation caps, so both fields get truncated.
	longCJK := strings.Repeat("你好世界", 200)

	msgs := []agentcore.Message{
		{
			Role:    agentcore.RoleUser,
			Type:    agentcore.MessageTypeStandard,
			Content: longCJK,
		},
		{
			Role:    agentcore.RoleAssistant,
			Content: longCJK,
		},
	}

	summary := SummarizeParentState(msgs)

	// Core guarantee: the summary must be valid UTF-8 even after truncating
	// multi-byte CJK sequences.
	if !utf8.ValidString(summary) {
		t.Errorf("SummarizeParentState produced invalid UTF-8 for CJK content:\n%q", summary)
	}
	// Truncation must have occurred (content exceeds both caps).
	if !strings.Contains(summary, "…") {
		t.Error("expected summary to contain an ellipsis indicating truncation occurred")
	}
	// The CJK prefix should be preserved in the truncated output.
	if !strings.Contains(summary, "你好世界") {
		t.Error("summary should contain CJK prefix")
	}
}

// TestTruthChecker_UnknownStatusTreatedAsFailure verifies Bug 4: a TruthChecker
// returning an unrecognized status is not silently ignored — it is logged and
// treated as a failure.
func TestTruthChecker_UnknownStatusTreatedAsFailure(t *testing.T) {
	runner := NewSubagentRunner(SubagentRunnerConfig{
		TruthChecker: func(ctx context.Context, output, task string) (string, string) {
			return "bogus-status", "" // unknown status, empty reason
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		return "self-reported success", nil
	}

	output, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be downgraded to failure with a descriptive reason.
	if !strings.HasPrefix(output, "[❌ FAILED") {
		t.Errorf("expected failure annotation for unknown status, got: %s", output)
	}
	if !strings.Contains(output, "bogus-status") {
		t.Errorf("expected unknown status to appear in reason, got: %s", output)
	}
	if !strings.Contains(output, "self-reported success") {
		t.Errorf("expected original output preserved, got: %s", output)
	}
}

// TestTruthChecker_UnknownStatusWithReasonUsesReason verifies that when a
// TruthChecker returns an unknown status with a non-empty reason, that reason
// is used in the failure annotation.
func TestTruthChecker_UnknownStatusWithReasonUsesReason(t *testing.T) {
	runner := NewSubagentRunner(SubagentRunnerConfig{
		TruthChecker: func(ctx context.Context, output, task string) (string, string) {
			return "weird", "custom diagnostic message"
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		return "output", nil
	}

	output, err := runner.Run(context.Background(), spawn, "test", nil, SubagentRunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(output, "[❌ FAILED") {
		t.Errorf("expected failure annotation, got: %s", output)
	}
	if !strings.Contains(output, "custom diagnostic message") {
		t.Errorf("expected custom reason in annotation, got: %s", output)
	}
}
