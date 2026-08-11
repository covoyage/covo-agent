package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

var IdempotentToolNames = map[string]bool{
	"read":           true,
	"grep":           true,
	"glob":           true,
	"ls":             true,
	"web_search":     true,
	"web_fetch":      true,
	"session_search": true,
	"pdf":            true,
	"diffs":          true,
	"git_status":     true,
	"git_log":        true,
	"git_diff":       true,
}

var MutatingToolNames = map[string]bool{
	"write_file":     true,
	"edit":           true,
	"edit_block":     true,
	"patch":          true,
	"bash":           true,
	"git_commit":     true,
	"git_push":       true,
	"process":        true,
	"execute_code":   true,
	"browser":        true,
	"todo":           true,
	"clarify":        true,
	"cronjob":        true,
	"update_plan":    true,
	"send_message":   true,
	"human_handoff":  true,
	"llm_task":       true,
	"skill_manage":   true,
	"tts":            true,
	"image_generate": true,
	"video_generate": true,
	"music_generate": true,
	"sessions_spawn": true,
	"computer_use":   true,
}

type GuardrailDecision string

const (
	GuardrailAllow GuardrailDecision = "allow"
	GuardrailWarn  GuardrailDecision = "warn"
	GuardrailBlock GuardrailDecision = "block"
	GuardrailHalt  GuardrailDecision = "halt"
)

// GuardrailConfig controls detection thresholds.
type GuardrailConfig struct {
	ExactDuplicateWarnAfter  int // consecutive identical (name+canonical args) calls before warning (default 3)
	ExactDuplicateBlockAfter int // consecutive identical calls before blocking (default 15)
	NoProgressWarnAfter      int // idempotent tool returning same result before warning (default 5)
	NoProgressBlockAfter     int // idempotent tool returning same result before blocking (default 15)

	// GlobalDuplicateWarnAfter / GlobalDuplicateHaltAfter catch the same
	// exact call (name+canonical args) recurring non-consecutively within
	// the bounded history window (other calls interleaved in between) --
	// a pattern the consecutive-only ExactDuplicate check above cannot see.
	// Subject to the read-only cold-start exemption (see hasSeenMutating):
	// before any mutating tool has run this session, a window full of
	// read-only calls is treated as normal exploration, not a loop.
	GlobalDuplicateWarnAfter int // default 6
	GlobalDuplicateHaltAfter int // default 10
}

func DefaultGuardrailConfig() GuardrailConfig {
	return GuardrailConfig{
		ExactDuplicateWarnAfter:  3,
		ExactDuplicateBlockAfter: 15,
		NoProgressWarnAfter:      5,
		NoProgressBlockAfter:     15,
		GlobalDuplicateWarnAfter: 6,
		GlobalDuplicateHaltAfter: 10,
	}
}

type ToolCallRecord struct {
	Name   string
	Args   string
	Result string
	Hash   string
	Time   time.Time
}

// turnState tracks counts that reset each turn.
type turnState struct {
	exactFailureCounts map[string]int    // hash -> count
	sameToolFailures   map[string]int    // toolName -> consecutive count
	noProgressCounts   map[string]int    // hash -> count of identical results
	lastResultHash     map[string]string // hash -> last result hash (for no-progress)
}

func newTurnState() *turnState {
	return &turnState{
		exactFailureCounts: make(map[string]int),
		sameToolFailures:   make(map[string]int),
		noProgressCounts:   make(map[string]int),
		lastResultHash:     make(map[string]string),
	}
}

type ToolGuardrail struct {
	mu              sync.Mutex
	history         []ToolCallRecord
	maxHistory      int
	config          GuardrailConfig
	turn            *turnState
	halted          bool     // set when halt is triggered; must reset manually
	pendingWarnings []string // warnings to append to current tool result

	// hasSeenMutating gates the global-duplicate cold-start exemption: until
	// at least one mutating tool call has been recorded, read-only/idempotent
	// tool calls are exempt from global-duplicate counting (a window full of
	// reads at the start of a task -- e.g. list_directory + several
	// read_file calls -- is normal exploration, not a loop). Never reset by
	// NewTurn(); persists for the life of the session (per-prompt, not
	// per-turn scoping).
	hasSeenMutating bool
}

func NewToolGuardrail() *ToolGuardrail {
	return &ToolGuardrail{
		history:    make([]ToolCallRecord, 0, 32),
		maxHistory: 20,
		config:     DefaultGuardrailConfig(),
		turn:       newTurnState(),
		halted:     false,
	}
}

func (g *ToolGuardrail) IsIdempotent(toolName string) bool {
	return IdempotentToolNames[toolName]
}

func (g *ToolGuardrail) IsMutating(toolName string) bool {
	return MutatingToolNames[toolName]
}

func (g *ToolGuardrail) IsHalted() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.halted
}

// DrainWarnings returns any pending warnings and clears them.
// Called by the after-hook to append guidance to the tool result.
func (g *ToolGuardrail) DrainWarnings() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.pendingWarnings) == 0 {
		return ""
	}
	result := strings.Join(g.pendingWarnings, "\n")
	g.pendingWarnings = g.pendingWarnings[:0]
	return result
}

func (g *ToolGuardrail) Record(toolName string, args json.RawMessage) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if MutatingToolNames[toolName] {
		g.hasSeenMutating = true
	}

	argsStr := string(args)
	hash := computeStepSignature(toolName, args)

	g.history = append(g.history, ToolCallRecord{
		Name: toolName,
		Args: argsStr,
		Hash: hash,
		Time: time.Now(),
	})

	if len(g.history) > g.maxHistory {
		g.history = g.history[len(g.history)-g.maxHistory:]
	}
}

func (g *ToolGuardrail) RecordResult(hash string, result string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for i := len(g.history) - 1; i >= 0; i-- {
		if g.history[i].Hash == hash {
			g.history[i].Result = result
			break
		}
	}
}

// Check runs before tool execution. Returns allow, warn, block, or halt.
func (g *ToolGuardrail) Check(toolName string, args json.RawMessage) GuardrailDecision {
	g.mu.Lock()
	defer g.mu.Unlock()

	callHash := computeStepSignature(toolName, args)

	// 1. Exact duplicate detection (same name + same args)
	consecutive := 0
	for i := len(g.history) - 1; i >= 0; i-- {
		if g.history[i].Name == toolName && g.history[i].Hash == callHash {
			consecutive++
		} else {
			break
		}
	}

	if consecutive >= g.config.ExactDuplicateBlockAfter {
		return GuardrailBlock
	}
	if consecutive >= g.config.ExactDuplicateWarnAfter {
		g.pendingWarnings = append(g.pendingWarnings,
			fmt.Sprintf("[tool loop warning: called %q %d times in a row with identical arguments — try a different approach]",
				toolName, consecutive+1))
		return GuardrailWarn
	}

	// 2. Global (non-consecutive) duplicate detection: the same exact call
	// can also resurface with other, different calls interleaved in
	// between -- a pattern the consecutive-only check above cannot see.
	// Skip read-only/idempotent tools during the cold-start exploration
	// phase: before any mutating tool has run, a window full of reads is
	// normal exploration (e.g. "summarize this project" legitimately opens
	// with list_directory + several read_file calls), not a loop.
	if g.hasSeenMutating || MutatingToolNames[toolName] {
		total := 1 // this call, about to be recorded
		for i := range g.history {
			if g.history[i].Name == toolName && g.history[i].Hash == callHash {
				total++
			}
		}

		if total >= g.config.GlobalDuplicateHaltAfter {
			g.halted = true
			return GuardrailHalt
		}
		if total >= g.config.GlobalDuplicateWarnAfter {
			g.pendingWarnings = append(g.pendingWarnings,
				fmt.Sprintf("[tool loop warning: called %q with identical arguments %d times this turn (not necessarily in a row) — try a different approach]",
					toolName, total))
			return GuardrailWarn
		}
	}

	// 3. Same-tool consecutiveness (different args): allow silently.
	// Only exact duplicates are restricted — running the same tool with
	// different arguments is legitimate (e.g. multiple bash commands).
	return GuardrailAllow
}

// CheckAfterCall runs after tool execution. Detects no-progress patterns
// for idempotent tools that keep returning the same result.
func (g *ToolGuardrail) CheckAfterCall(toolName string, args json.RawMessage, result string) GuardrailDecision {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !IdempotentToolNames[toolName] {
		return GuardrailAllow
	}

	callHash := computeStepSignature(toolName, args)
	resultHash := computeResultHash(result)

	// Check if this exact call previously returned the same result
	prevHash, existed := g.turn.lastResultHash[callHash]
	g.turn.lastResultHash[callHash] = resultHash

	if existed && prevHash == resultHash {
		count := g.turn.noProgressCounts[callHash] + 1
		g.turn.noProgressCounts[callHash] = count

		if count >= g.config.NoProgressBlockAfter {
			return GuardrailBlock
		}
		if count >= g.config.NoProgressWarnAfter {
			g.pendingWarnings = append(g.pendingWarnings,
				fmt.Sprintf("[no-progress warning: %s returned the same result %d times — the retrieved data is not changing, move on]",
					toolName, count+1))
			return GuardrailWarn
		}
	}

	return GuardrailAllow
}

// CheckConsecutiveMutating checks for consecutive mutating tool calls.
func (g *ToolGuardrail) CheckConsecutiveMutating() GuardrailDecision {
	g.mu.Lock()
	defer g.mu.Unlock()

	count := 0
	for i := len(g.history) - 1; i >= 0; i-- {
		if MutatingToolNames[g.history[i].Name] {
			count++
		} else {
			break
		}
	}

	if count >= 3 {
		return GuardrailWarn
	}
	return GuardrailAllow
}

func (g *ToolGuardrail) LastCall() *ToolCallRecord {
	g.mu.Lock()
	defer g.mu.Unlock()

	if len(g.history) == 0 {
		return nil
	}
	last := g.history[len(g.history)-1]
	return &last
}

func (g *ToolGuardrail) History() []ToolCallRecord {
	g.mu.Lock()
	defer g.mu.Unlock()

	cp := make([]ToolCallRecord, len(g.history))
	copy(cp, g.history)
	return cp
}

// NewTurn resets per-turn state. Called at the start of each agent turn.
func (g *ToolGuardrail) NewTurn() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.turn = newTurnState()
	g.halted = false
	g.pendingWarnings = nil
}

func (g *ToolGuardrail) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.history = g.history[:0]
	g.turn = newTurnState()
	g.halted = false
	g.pendingWarnings = nil
}

// computeStepSignature produces a canonical hash of (toolName, args) that is
// stable regardless of JSON key ordering, so {"a":1,"b":2} and {"b":2,"a":1}
// collapse to the same signature.
func computeStepSignature(toolName string, args json.RawMessage) string {
	normalized := normalizeArgs(args)
	h := sha256.New()
	h.Write([]byte(toolName))
	h.Write([]byte{0})
	h.Write(normalized)
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// normalizeArgs re-serializes JSON with sorted keys. Go's json.Marshal sorts
// map keys lexicographically, so two semantically-equal objects with different
// key order produce identical output. Falls back to raw bytes on parse error.
func normalizeArgs(args json.RawMessage) []byte {
	if len(args) == 0 {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(args, &v); err != nil {
		return args
	}
	out, err := json.Marshal(v)
	if err != nil {
		return args
	}
	return out
}

func computeResultHash(result string) string {
	h := sha256.New()
	h.Write([]byte(result))
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}
