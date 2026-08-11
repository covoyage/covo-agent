package agent

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covonaut/agentcore"
)

const (
	defaultStaleTimeout = 90 * time.Second
	defaultReadTimeout  = 60 * time.Second
	healthCheckInterval = 5 * time.Second

	// Repetition-loop detection. A degenerating model can stream the same
	// short phrase over and over ("madly repeating the same content"). We
	// scan a bounded tail of the accumulated text and, if it is periodic with
	// a small period repeated many times, cancel the stream to break the loop.
	repetitionMinUnit    = 8    // min rune length of a repeating unit to consider
	repetitionMaxUnit    = 512  // max rune length of a repeating unit to scan
	repetitionMinCycles  = 4    // min consecutive cycles before flagging (short units)
	repetitionMinSpan    = 120  // min total runes the repetition must span
	repetitionScanTail   = 8192 // max runes of trailing text kept for scanning
	repetitionCheckEvery = 128  // run detection after this many new runes

	// Long repeating units (whole sentences/paragraphs) get a lower cycle
	// requirement: an exact verbatim block of this size repeating even twice
	// back-to-back is already a very strong degeneration signal, and waiting
	// for repetitionMinCycles (4) full repeats — as required for short units —
	// would let a lot of visibly duplicated text reach the user first.
	repetitionLongUnitLen    = 64 // rune length at/above which a unit is "long"
	repetitionLongUnitCycles = 2  // cycles required to flag a long unit

	// repetitionMinDistinct guards against flagging low-entropy repeats (long
	// runs of "====", markdown table dividers, indentation dots, etc.) as
	// degeneration loops: a periodic match only counts if the repeating unit
	// itself contains at least this many distinct runes. Legitimate repeated
	// sentences/paragraphs have plenty of distinct characters and are
	// unaffected; a run of a single repeated character or a short
	// low-variety pattern no longer trips the detector.
	repetitionMinDistinct = 3
)

// markdownStructuralLinePattern matches lines that look like structured
// Markdown (table rows, list items, headings, block quotes) rather than
// genuinely looping prose. This content has legitimate syntactic repetition
// (table pipes, list bullets, heading markers) that could otherwise trip the
// periodicity/distinct-rune checks below.
var markdownStructuralLinePattern = regexp.MustCompile(
	`(?m)^\s*(\|.*\||[-*+]\s+\S|\d+\.\s+\S|#{1,6}\s+\S|>\s+\S)`,
)

// looksLikeStructuredMarkdown reports whether s is currently inside an open
// code fence, or contains table/list/heading/blockquote-style lines. Kept
// deliberately coarse (whole-tail, not per-match) to match this detector's
// stateless "rescan the whole tail" architecture cheaply.
func looksLikeStructuredMarkdown(s string) bool {
	if strings.Count(s, "```")%2 == 1 {
		return true // inside an open code fence
	}
	return markdownStructuralLinePattern.MatchString(s)
}

type streamHealthMiddleware struct {
	inner        agentcore.Provider
	logger       *slog.Logger
	staleTimeout time.Duration
	readTimeout  time.Duration
}

// NewStreamHealthMiddleware creates a middleware that monitors streaming responses
// for staleness and read timeouts.
//
//   - staleTimeout: if no data is received within this duration, the stream is
//     considered stale and an error is injected to break the agent loop.
//   - readTimeout: if a single delta read takes longer than this, a warning is
//     logged but the stream continues.
func NewStreamHealthMiddleware(logger *slog.Logger, staleTimeout, readTimeout time.Duration) ProviderMiddleware {
	if staleTimeout <= 0 {
		staleTimeout = defaultStaleTimeout
	}
	if readTimeout <= 0 {
		readTimeout = defaultReadTimeout
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return func(inner agentcore.Provider) agentcore.Provider {
		return &streamHealthMiddleware{
			inner:        inner,
			logger:       logger,
			staleTimeout: staleTimeout,
			readTimeout:  readTimeout,
		}
	}
}

func (m *streamHealthMiddleware) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	return m.inner.Complete(ctx, req)
}

func (m *streamHealthMiddleware) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	// Create a cancelable context BEFORE opening the inner stream and pass it
	// down, so cancelling streamCtx (on stale or repetition detection) actually
	// aborts the upstream provider promptly instead of letting it keep
	// generating until the stale timeout.
	streamCtx, streamCancel := context.WithCancel(ctx)
	innerStream, err := m.inner.Stream(streamCtx, req)
	if err != nil {
		streamCancel()
		return nil, err
	}
	// Wrap the inner stream so reads respect readTimeout.
	timeoutStream := readTimeoutStream(streamCtx, innerStream, m.readTimeout, m.logger)

	out := make(chan agentcore.StreamDelta)
	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixNano())

	// Health monitor goroutine: watches for stale stream.
	safego.SafeGo(func() {
		defer streamCancel()
		ticker := time.NewTicker(healthCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-streamCtx.Done():
				return
			case <-ticker.C:
				elapsed := time.Duration(time.Now().UnixNano() - lastActivity.Load())
				if elapsed > m.staleTimeout {
					m.logger.Warn("stream stalled, cancelling",
						"stale_for", elapsed.Round(time.Second),
						"timeout", m.staleTimeout)
					return
				}
			}
		}
	}, m.logger)

	// Forward goroutine: reads from timeout-wrapped stream and forwards to output.
	safego.SafeGo(func() {
		defer close(out)
		var tail []rune    // bounded trailing text for repetition scanning
		var sinceCheck int // runes appended since last detection scan
		for {
			select {
			case <-streamCtx.Done():
				return
			case delta, ok := <-timeoutStream:
				if !ok {
					return
				}
				lastActivity.Store(time.Now().UnixNano())
				if delta.Content != "" {
					tail = append(tail, []rune(delta.Content)...)
					if len(tail) > repetitionScanTail {
						tail = tail[len(tail)-repetitionScanTail:]
					}
					sinceCheck += len([]rune(delta.Content))
					if sinceCheck >= repetitionCheckEvery {
						sinceCheck = 0
						if detectRepetitionLoop(tail) {
							m.logger.Warn("stream repetition loop detected, cancelling",
								"tail_len", len(tail))
							// Signal a terminal error instead of silently
							// truncating or baking a notice into the
							// degenerate output: runLoop's repetition-recovery
							// ladder (agentcore.IsRepetitionLoopError) catches
							// this, discards the bad partial content, injects
							// a corrective steering nudge, and gives the model
							// another chance (escalating, then giving up)
							// rather than ending the turn outright.
							select {
							case out <- agentcore.StreamDelta{Err: agentcore.ErrRepetitionLoop}:
							case <-streamCtx.Done():
							}
							streamCancel() // abort upstream promptly
							return
						}
					}
				}
				select {
				case out <- delta:
				case <-streamCtx.Done():
					return
				}
			}
		}
	}, m.logger)

	return out, nil
}

// detectRepetitionLoop reports whether the tail of streamed text has degenerated
// into a repeating loop. It returns true when some unit of length p (in
// [repetitionMinUnit, repetitionMaxUnit]) repeats consecutively at the end of
// the text for at least repetitionMinCycles cycles spanning repetitionMinSpan
// runes. The check is phase-independent: it tests periodicity of the suffix
// rather than requiring block alignment, so token boundaries don't matter.
func detectRepetitionLoop(tail []rune) bool {
	n := len(tail)
	if n < repetitionMinSpan {
		return false
	}
	if looksLikeStructuredMarkdown(string(tail)) {
		return false
	}
	// Bound the search using the smallest possible cycle requirement
	// (repetitionLongUnitCycles) so long-unit periodicities near
	// repetitionMaxUnit aren't excluded by a tighter, short-unit-only bound.
	maxUnit := repetitionMaxUnit
	if maxUnit > n/repetitionLongUnitCycles {
		maxUnit = n / repetitionLongUnitCycles
	}
	for p := repetitionMinUnit; p <= maxUnit; p++ {
		cycles := repetitionMinCycles
		if p >= repetitionLongUnitLen {
			cycles = repetitionLongUnitCycles
		}
		if p*cycles < repetitionMinSpan {
			cycles = (repetitionMinSpan + p - 1) / p
		}
		span := p * cycles
		if span > n {
			continue
		}
		periodic := true
		for i := 0; i < span-p; i++ {
			if tail[n-1-i] != tail[n-1-i-p] {
				periodic = false
				break
			}
		}
		if periodic && distinctRuneCount(tail[n-p:]) >= repetitionMinDistinct {
			return true
		}
	}
	return false
}

// distinctRuneCount returns the number of distinct runes in s.
func distinctRuneCount(s []rune) int {
	seen := make(map[rune]struct{}, len(s))
	for _, r := range s {
		seen[r] = struct{}{}
	}
	return len(seen)
}

// readTimeoutStream wraps a stream channel so that each read has a timeout.
// On timeout, a warning is logged and a sentinel delta with an error is emitted.
func readTimeoutStream(ctx context.Context, in <-chan agentcore.StreamDelta, timeout time.Duration, logger *slog.Logger) <-chan agentcore.StreamDelta {
	out := make(chan agentcore.StreamDelta)
	safego.SafeGo(func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case delta, ok := <-in:
				if !ok {
					return
				}
				select {
				case out <- delta:
				case <-ctx.Done():
					return
				case <-time.After(timeout):
					logger.Warn("stream read timed out, dropping delta",
						"timeout", timeout)
				}
			}
		}
	}, logger)
	return out
}
