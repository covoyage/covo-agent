// Package doomloop provides cross-turn doom loop detection.
//
// Beyond the existing single-turn repetition detection (stream_health.go),
// this module tracks tool call patterns across turns to detect:
//   - Repeated identical tool calls (same name + same args)
//   - Cyclical tool call sequences (A → B → A → B → ...)
//   - Repeatedly failing tool calls
//   - Budget exhaustion for recovery attempts
package doomloop

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ToolCall represents a single tool invocation record.
type ToolCall struct {
	Name       string    `json:"name"`
	ArgsHash   string    `json:"args_hash"`
	ResultHash string    `json:"result_hash,omitempty"`
	Turn       int       `json:"turn"`
	Timestamp  time.Time `json:"timestamp"`
	Success    bool      `json:"success"`
	Error      string    `json:"error,omitempty"`
}

// LoopType classifies the detected doom loop pattern.
type LoopType int

const (
	LoopNone            LoopType = iota
	LoopIdenticalTool            // same tool + same args repeatedly
	LoopCyclical                 // A → B → A → B pattern
	LoopFailingTool              // same tool keeps failing
	LoopBudgetExhausted          // recovery budget used up
)

func (lt LoopType) String() string {
	switch lt {
	case LoopNone:
		return "none"
	case LoopIdenticalTool:
		return "identical_tool"
	case LoopCyclical:
		return "cyclical"
	case LoopFailingTool:
		return "failing_tool"
	case LoopBudgetExhausted:
		return "budget_exhausted"
	default:
		return "unknown"
	}
}

// Detection represents a detected doom loop.
type Detection struct {
	Type        LoopType   `json:"type"`
	Description string     `json:"description"`
	Calls       []ToolCall `json:"calls"`
	Turn        int        `json:"turn"`
}

// Config controls doom loop detection sensitivity.
type Config struct {
	// MaxIdenticalCalls: flag after this many identical tool calls.
	MaxIdenticalCalls int
	// MaxCycleLength: max pattern length for cyclical detection.
	MaxCycleLength int
	// MaxCycles: flag after this many cycles of a pattern.
	MaxCycles int
	// MaxConsecutiveFailures: flag after this many failures of the same tool.
	MaxConsecutiveFailures int
	// MaxRecoveryBudget: total recovery attempts allowed before giving up.
	MaxRecoveryBudget int
	// HistorySize: max tool calls to retain.
	HistorySize int
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		MaxIdenticalCalls:      3,
		MaxCycleLength:         5,
		MaxCycles:              2,
		MaxConsecutiveFailures: 3,
		MaxRecoveryBudget:      5,
		HistorySize:            100,
	}
}

// Detector tracks tool calls across turns and detects doom loops.
type Detector struct {
	mu             sync.Mutex
	config         Config
	calls          []ToolCall
	recoveryBudget int
	recoveryUsed   int
	lastDetection  *Detection
}

// New creates a doom loop detector with the given config.
func New(cfg Config) *Detector {
	if cfg.HistorySize <= 0 {
		cfg.HistorySize = 100
	}
	return &Detector{
		config:         cfg,
		calls:          make([]ToolCall, 0, cfg.HistorySize),
		recoveryBudget: cfg.MaxRecoveryBudget,
	}
}

// RecordToolCall records a tool call and returns any detected doom loop.
func (d *Detector) RecordToolCall(call ToolCall) *Detection {
	d.mu.Lock()
	defer d.mu.Unlock()

	if call.Timestamp.IsZero() {
		call.Timestamp = time.Now()
	}

	d.calls = append(d.calls, call)
	if len(d.calls) > d.config.HistorySize {
		d.calls = d.calls[len(d.calls)-d.config.HistorySize:]
	}

	// Check each detection type
	if det := d.checkIdenticalTool(call.Turn); det != nil {
		return d.recordDetection(det)
	}
	if det := d.checkCyclical(call.Turn); det != nil {
		return d.recordDetection(det)
	}
	if det := d.checkFailingTool(call.Turn); det != nil {
		return d.recordDetection(det)
	}

	d.lastDetection = nil
	return nil
}

func (d *Detector) recordDetection(det *Detection) *Detection {
	if d.lastDetection != nil && d.lastDetection.Type == det.Type {
		return nil
	}
	d.lastDetection = det
	return det
}

// checkIdenticalTool detects repeated identical tool calls.
func (d *Detector) checkIdenticalTool(turn int) *Detection {
	if len(d.calls) < d.config.MaxIdenticalCalls {
		return nil
	}

	// Check the last N calls for identical name+args
	n := d.config.MaxIdenticalCalls
	recent := d.calls[len(d.calls)-n:]
	first := recent[0]
	allSame := true
	for _, c := range recent[1:] {
		if c.Name != first.Name || c.ArgsHash != first.ArgsHash || c.Success != first.Success {
			allSame = false
			break
		}
		if c.Success && c.ResultHash != first.ResultHash {
			allSame = false
			break
		}
	}
	if allSame {
		return &Detection{
			Type:        LoopIdenticalTool,
			Description: fmt.Sprintf("tool %q called %d times with identical arguments", first.Name, n),
			Calls:       append([]ToolCall{}, recent...),
			Turn:        turn,
		}
	}
	return nil
}

// checkCyclical detects A → B → A → B patterns.
func (d *Detector) checkCyclical(turn int) *Detection {
	for patternLen := 2; patternLen <= d.config.MaxCycleLength; patternLen++ {
		cyclesNeeded := d.config.MaxCycles
		totalNeeded := patternLen * cyclesNeeded
		if len(d.calls) < totalNeeded {
			continue
		}

		recent := d.calls[len(d.calls)-totalNeeded:]
		pattern := recent[:patternLen]

		allMatch := true
		for cycle := 1; cycle < cyclesNeeded; cycle++ {
			for i := 0; i < patternLen; i++ {
				idx := cycle*patternLen + i
				if recent[idx].Name != pattern[i].Name || recent[idx].ArgsHash != pattern[i].ArgsHash {
					allMatch = false
					break
				}
			}
			if !allMatch {
				break
			}
		}

		if allMatch {
			return &Detection{
				Type:        LoopCyclical,
				Description: fmt.Sprintf("cyclical tool pattern of length %d repeated %d times", patternLen, cyclesNeeded),
				Calls:       append([]ToolCall{}, recent...),
				Turn:        turn,
			}
		}
	}
	return nil
}

// checkFailingTool detects repeatedly failing tool calls.
func (d *Detector) checkFailingTool(turn int) *Detection {
	if len(d.calls) < d.config.MaxConsecutiveFailures {
		return nil
	}

	n := d.config.MaxConsecutiveFailures
	recent := d.calls[len(d.calls)-n:]

	// All must be failures of the same tool
	first := recent[0]
	if first.Success {
		return nil
	}
	allSameFailing := true
	for _, c := range recent[1:] {
		if c.Name != first.Name || c.Success {
			allSameFailing = false
			break
		}
	}
	if allSameFailing {
		return &Detection{
			Type:        LoopFailingTool,
			Description: fmt.Sprintf("tool %q failed %d consecutive times", first.Name, n),
			Calls:       append([]ToolCall{}, recent...),
			Turn:        turn,
		}
	}
	return nil
}

// CanRecover checks if there's recovery budget remaining.
func (d *Detector) CanRecover() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.recoveryUsed < d.recoveryBudget
}

// UseRecovery increments the recovery counter.
// Returns false if the budget is exhausted.
func (d *Detector) UseRecovery() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.recoveryUsed >= d.recoveryBudget {
		return false
	}
	d.recoveryUsed++
	return true
}

// RemainingBudget returns the number of recovery attempts left.
func (d *Detector) RemainingBudget() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.recoveryBudget - d.recoveryUsed
}

// Reset clears the detector state (e.g., after a successful turn).
func (d *Detector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = d.calls[:0]
	d.recoveryUsed = 0
	d.lastDetection = nil
}

// ResetForNewTurn clears the call history but preserves recovery budget.
func (d *Detector) ResetForNewTurn() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = d.calls[:0]
	d.lastDetection = nil
}

// LastDetection returns the most recent doom loop detection, or nil.
func (d *Detector) LastDetection() *Detection {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastDetection
}

// GetRecoveryNudge generates a steering nudge for the agent when a doom
// loop is detected. This is injected as a system message to guide the
// agent away from the repeating pattern.
func (det *Detection) GetRecoveryNudge() string {
	switch det.Type {
	case LoopIdenticalTool:
		toolName := ""
		if len(det.Calls) > 0 {
			toolName = det.Calls[0].Name
		}
		if len(det.Calls) > 0 && det.Calls[0].Success {
			return fmt.Sprintf(
				"You've called %s %d times with identical arguments and received the same result. "+
					"Stop repeating the same action and use the existing result or try a different approach.",
				toolName, len(det.Calls),
			)
		}
		return fmt.Sprintf(
			"You've called %s %d times with identical arguments without success. "+
				"Stop repeating the same action. Read the error carefully and try a different approach.",
			toolName, len(det.Calls),
		)
	case LoopCyclical:
		tools := make([]string, 0)
		seen := make(map[string]bool)
		for _, c := range det.Calls {
			if !seen[c.Name] {
				tools = append(tools, c.Name)
				seen[c.Name] = true
			}
		}
		return fmt.Sprintf(
			"⚠️ You're stuck in a cycle: %s. "+
				"BREAK THE CYCLE. Step back, analyze why the previous attempts failed, "+
				"and try a fundamentally different approach.",
			strings.Join(tools, " → "),
		)
	case LoopFailingTool:
		toolName := ""
		if len(det.Calls) > 0 {
			toolName = det.Calls[0].Name
		}
		errors := make([]string, 0)
		for _, c := range det.Calls {
			if c.Error != "" {
				errors = append(errors, c.Error)
			}
		}
		return fmt.Sprintf(
			"⚠️ Tool %s has failed %d consecutive times. "+
				"Last error: %s. "+
				"DO NOT retry the same action. Analyze the root cause and fix it before trying again.",
			toolName, len(det.Calls),
			strings.Join(errors, "; "),
		)
	case LoopBudgetExhausted:
		return "⚠️ Recovery budget exhausted. The task may not be achievable with the current approach. " +
			"Summarize what you've tried and ask the user for guidance."
	default:
		return ""
	}
}

// HashArgs generates a stable full-content digest for deduplication. JSON is
// canonicalized without changing whitespace inside string values.
func HashArgs(args string) string {
	normalized := []byte(strings.TrimSpace(args))
	var value any
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.UseNumber()
	if json.Valid(normalized) && decoder.Decode(&value) == nil {
		if canonical, err := json.Marshal(value); err == nil {
			normalized = canonical
		}
	} else {
		normalized = []byte(strings.Join(strings.Fields(args), " "))
	}
	return hashBytes(normalized)
}

// HashResult generates an exact full-content digest for tool output. A
// changing result is treated as progress even when tool arguments are stable.
func HashResult(result string) string {
	return hashBytes([]byte(result))
}

func hashBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
