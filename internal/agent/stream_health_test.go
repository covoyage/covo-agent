package agent

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

func TestDetectRepetitionLoop(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "empty",
			text: "",
			want: false,
		},
		{
			name: "short non-repeating prose",
			text: "Let me extend the errcode test with more coverage and verify it.",
			want: false,
		},
		{
			name: "repeated sentence loop",
			text: strings.Repeat("Now let me also extend the errcode test with more coverage: ", 12),
			want: true,
		},
		{
			name: "loop after a legit prefix",
			text: "Here is the plan and rationale for the change. " +
				strings.Repeat("retrying the same step again. ", 15),
			want: true,
		},
		{
			name: "long single-char run is not flagged (low entropy divider)",
			// A run of a single repeated character (e.g. a markdown/ASCII
			// divider like "====") is periodic but has only 1 distinct rune in
			// the repeating unit, well below repetitionMinDistinct (3) — legit
			// structured content like this should never trip the detector.
			text: strings.Repeat("=", 300),
			want: false,
		},
		{
			name: "low-entropy two-char alternation is not flagged",
			// "ab" repeated many times: periodic, but only 2 distinct runes in
			// the repeating unit — still below repetitionMinDistinct (3).
			text: strings.Repeat("ab", 200),
			want: false,
		},
		{
			name: "markdown table divider is not flagged",
			text: strings.Repeat("|---", 100),
			want: false,
		},
		{
			name: "varied long output is not flagged",
			text: "func add(a, b int) int { return a + b }\n" +
				"func sub(a, b int) int { return a - b }\n" +
				"func mul(a, b int) int { return a * b }\n" +
				"func div(a, b int) int { return a / b }\n" +
				"func mod(a, b int) int { return a % b }\n",
			want: false,
		},
		{
			name: "few repeats below threshold",
			text: strings.Repeat("extend the errcode test. ", 3),
			want: false,
		},
		{
			name: "long paragraph repeated only twice trips fast",
			// A ~140-rune sentence repeated just twice back-to-back: below
			// repetitionMinCycles (4) but should still be flagged because a
			// long unit only needs repetitionLongUnitCycles (2) repeats.
			text: strings.Repeat("covo-agent is a Go project (108k LOC, 450 files) and clearly shares lineage/inspiration with hermes-agent. Let me dig into its internal architecture. ", 2),
			want: true,
		},
		{
			name: "long paragraph repeated once is not flagged",
			text: "covo-agent is a Go project (108k LOC, 450 files) and clearly shares lineage/inspiration with hermes-agent. Let me dig into its internal architecture. ",
			want: false,
		},
		{
			name: "repeated identical list items are not flagged",
			// Each line has plenty of distinct runes (would trip
			// repetitionMinDistinct on its own), but is legitimate Markdown
			// list syntax, not a degeneration loop.
			text: strings.Repeat("- Buy milk and eggs\n", 20),
			want: false,
		},
		{
			name: "repeated identical table rows are not flagged",
			text: strings.Repeat("| Name | Value | Notes |\n", 20),
			want: false,
		},
		{
			name: "repeated content inside an open code fence is not flagged",
			text: "```go\n" + strings.Repeat("fmt.Println(counter)\n", 20),
			want: false,
		},
		{
			name: "closed code fence does not suppress detection afterward",
			// The fence is closed (even ``` count), so content streamed
			// after it should still be checked normally.
			text: "```go\nfmt.Println(1)\n```\n" +
				strings.Repeat("Now let me also extend the errcode test with more coverage: ", 12),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectRepetitionLoop([]rune(tt.text))
			if got != tt.want {
				t.Errorf("detectRepetitionLoop() = %v, want %v", got, tt.want)
			}
		})
	}
}

// fakeStreamProvider streams `chunk` `count` times, then a Done delta. It
// respects context cancellation so the middleware can abort it.
type fakeStreamProvider struct {
	chunk string
	count int
	sent  atomic.Int64 // number of chunks actually sent before the stream ended
}

func (f *fakeStreamProvider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	return &agentcore.ProviderResponse{}, nil
}

func (f *fakeStreamProvider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	ch := make(chan agentcore.StreamDelta)
	go func() {
		defer close(ch)
		for i := 0; i < f.count; i++ {
			select {
			case ch <- agentcore.StreamDelta{Content: f.chunk}:
				f.sent.Add(1)
			case <-ctx.Done():
				return
			}
		}
		select {
		case ch <- agentcore.StreamDelta{Done: true}:
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

func TestReadStreamNoDuplicateOnBackpressure(t *testing.T) {
	// Verify that readTimeoutStream does NOT duplicate deltas when the
	// output channel is blocked (backpressure). The old code retried the
	// send on timeout, causing duplicates.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := make(chan agentcore.StreamDelta, 1)
	// Use a very short timeout so the timeout case fires quickly.
	discard := slog.New(slog.DiscardHandler)
	out := readTimeoutStream(ctx, in, 1*time.Millisecond, discard)

	// Send 5 deltas via goroutine (output channel blocks, so sends may block).
	go func() {
		for i := 0; i < 5; i++ {
			select {
			case in <- agentcore.StreamDelta{Content: "x"}:
			case <-ctx.Done():
				return
			}
		}
		close(in)
	}()

	// Wait for all goroutine activity to settle, then drain.
	time.Sleep(50 * time.Millisecond)
	cancel()
	var count int
	for range out {
		count++
	}

	// With the fix, timeout drops unforwarded deltas — at most 1 should arrive.
	// The key assertion is: definitely NOT 5 (the old duplicate-everything bug).
	if count > 2 {
		t.Fatalf("expected at most 2 deltas (timeout drops unforwarded), got %d — duplication bug present", count)
	}
}

func TestReadStreamForwardedNormally(t *testing.T) {
	// When the output channel is NOT blocked, every delta is forwarded once.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := make(chan agentcore.StreamDelta, 5)
	out := readTimeoutStream(ctx, in, time.Second, nil)

	for i := 0; i < 5; i++ {
		in <- agentcore.StreamDelta{Content: "ok"}
	}
	close(in)

	var got []string
	for d := range out {
		got = append(got, d.Content)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 deltas, got %d", len(got))
	}
	for i, s := range got {
		if s != "ok" {
			t.Fatalf("delta %d: expected %q, got %q", i, "ok", s)
		}
	}
}

func TestStreamHealthRepetitionSignalsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake := &fakeStreamProvider{
		chunk: "Now let me also extend the errcode test with more coverage: ",
		count: 200,
	}
	mw := NewStreamHealthMiddleware(nil, time.Minute, time.Minute)
	p := mw(fake)

	out, err := p.Stream(ctx, &agentcore.ProviderRequest{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var sb strings.Builder
	sawErr := false
	for d := range out {
		sb.WriteString(d.Content)
		if agentcore.IsRepetitionLoopError(d.Err) {
			sawErr = true
		}
	}

	if !sawErr {
		t.Fatalf("expected a delta with Err = ErrRepetitionLoop, output so far: %q", sb.String())
	}
	// The stream must be cut early — far fewer than all 200 chunks forwarded.
	if int(fake.sent.Load()) >= fake.count {
		t.Fatalf("expected upstream to be aborted early, but all %d chunks were sent", fake.count)
	}
}
