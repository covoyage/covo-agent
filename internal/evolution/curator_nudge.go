package evolution

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// NudgeType categorizes the kind of nudge being delivered.
type NudgeType string

const (
	// NudgeMemory prompts the agent to reflect on and persist user knowledge.
	NudgeMemory NudgeType = "memory"

	// NudgeSkill prompts the agent to capture a repeatable pattern as a skill.
	NudgeSkill NudgeType = "skill"

	// NudgeCleanup prompts the agent to audit and prune stale knowledge.
	NudgeCleanup NudgeType = "cleanup"

	// NudgeExploration prompts the agent to suggest unexplored capabilities.
	NudgeExploration NudgeType = "exploration"
)

// NudgePriority indicates urgency.
type NudgePriority string

const (
	NudgeLow    NudgePriority = "low"
	NudgeMedium NudgePriority = "medium"
	NudgeHigh   NudgePriority = "high"
)

// Nudge represents a single nudge event to be delivered to the agent.
type Nudge struct {
	ID          string        `json:"id"`
	Type        NudgeType     `json:"type"`
	Priority    NudgePriority `json:"priority"`
	Message     string        `json:"message"`
	CreatedAt   time.Time     `json:"created_at"`
	Delivered   bool          `json:"delivered"`
	DeliveredAt *time.Time    `json:"delivered_at,omitempty"`
}

// NudgeSystem manages periodic nudges to the agent for self-improvement.
type NudgeSystem struct {
	mu            sync.RWMutex
	storePath     string
	nudges        []Nudge
	logger        *slog.Logger
	memory        *MemorySystem
	skillMgr      *SkillManager
	skillUsage    *SkillUsageTracker
	lastNudgeTime time.Time

	// Configurable intervals
	memoryNudgeInterval  time.Duration // How often to nudge about memory
	skillNudgeInterval   time.Duration // How often to nudge about skill creation
	cleanupNudgeInterval time.Duration // How often to nudge about cleanup
	minSessionsForNudge  int           // Minimum sessions before nudging
}

// NudgeConfig controls nudge behavior.
type NudgeConfig struct {
	MemoryNudgeInterval  time.Duration
	SkillNudgeInterval   time.Duration
	CleanupNudgeInterval time.Duration
	MinSessionsForNudge  int
}

// DefaultNudgeConfig returns sensible defaults.
func DefaultNudgeConfig() NudgeConfig {
	return NudgeConfig{
		MemoryNudgeInterval:  4 * time.Hour,
		SkillNudgeInterval:   8 * time.Hour,
		CleanupNudgeInterval: 24 * time.Hour,
		MinSessionsForNudge:  3,
	}
}

// NewNudgeSystem creates a nudge system.
func NewNudgeSystem(
	homeDir string,
	memory *MemorySystem,
	skillMgr *SkillManager,
	skillUsage *SkillUsageTracker,
	cfg NudgeConfig,
	logger *slog.Logger,
) *NudgeSystem {
	if logger == nil {
		logger = slog.Default()
	}
	return &NudgeSystem{
		storePath:            filepath.Join(homeDir, ".nudge_state.json"),
		memory:               memory,
		skillMgr:             skillMgr,
		skillUsage:           skillUsage,
		logger:               logger,
		memoryNudgeInterval:  cfg.MemoryNudgeInterval,
		skillNudgeInterval:   cfg.SkillNudgeInterval,
		cleanupNudgeInterval: cfg.CleanupNudgeInterval,
		minSessionsForNudge:  cfg.MinSessionsForNudge,
	}
}

// Load reads persisted nudge state.
func (ns *NudgeSystem) Load() error {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	data, err := os.ReadFile(ns.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var state struct {
		Nudges      []Nudge   `json:"nudges"`
		LastNudgeAt time.Time `json:"last_nudge_at"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parse nudge state: %w", err)
	}

	ns.nudges = state.Nudges
	ns.lastNudgeTime = state.LastNudgeAt
	return nil
}

func (ns *NudgeSystem) save() error {
	data, err := json.MarshalIndent(struct {
		Nudges      []Nudge   `json:"nudges"`
		LastNudgeAt time.Time `json:"last_nudge_at"`
	}{
		Nudges:      ns.nudges,
		LastNudgeAt: ns.lastNudgeTime,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ns.storePath, data, 0644)
}

// Tick evaluates whether any nudges should fire and returns them.
// Called periodically (e.g., every session end or heartbeat).
func (ns *NudgeSystem) Tick(sessionCount int) []Nudge {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	if sessionCount < ns.minSessionsForNudge {
		return nil
	}

	now := time.Now()
	var due []Nudge

	// Memory nudge: has it been long enough since last memory update?
	if time.Since(ns.lastNudgeTime) >= ns.memoryNudgeInterval {
		memoryContent := ns.memory.Snapshot(MemoryAgent)
		userContent := ns.memory.Snapshot(MemoryUser)

		// Only nudge if there's content to reflect on
		if len(memoryContent) > 50 || len(userContent) > 50 {
			nudge := Nudge{
				ID:        fmt.Sprintf("nudge-mem-%d", now.Unix()),
				Type:      NudgeMemory,
				Priority:  NudgeMedium,
				Message:   ns.buildMemoryNudgeMessage(),
				CreatedAt: now,
			}
			due = append(due, nudge)
			ns.logger.Info("memory nudge triggered")
		}
	}

	// Skill nudge: check if there are stale skills or unused patterns
	if time.Since(ns.lastNudgeTime) >= ns.skillNudgeInterval {
		records := ns.skillUsage.AllRecords()
		activeCount := 0
		staleCount := 0
		for _, r := range records {
			if r.Provenance == "bundled" {
				continue
			}
			switch r.State {
			case StateActive:
				activeCount++
			case StateStale:
				staleCount++
			}
		}

		if activeCount == 0 || staleCount > 0 {
			nudge := Nudge{
				ID:        fmt.Sprintf("nudge-skill-%d", now.Unix()),
				Type:      NudgeSkill,
				Priority:  NudgeLow,
				Message:   ns.buildSkillNudgeMessage(activeCount, staleCount),
				CreatedAt: now,
			}
			due = append(due, nudge)
			ns.logger.Info("skill nudge triggered", "active", activeCount, "stale", staleCount)
		}
	}

	// Cleanup nudge: periodic memory/skill audit
	if time.Since(ns.lastNudgeTime) >= ns.cleanupNudgeInterval {
		nudge := Nudge{
			ID:        fmt.Sprintf("nudge-clean-%d", now.Unix()),
			Type:      NudgeCleanup,
			Priority:  NudgeLow,
			Message:   ns.buildCleanupNudgeMessage(),
			CreatedAt: now,
		}
		due = append(due, nudge)
		ns.logger.Info("cleanup nudge triggered")
	}

	if len(due) > 0 {
		ns.lastNudgeTime = now
		ns.nudges = append(ns.nudges, due...)
		// Cap stored nudges at 100
		if len(ns.nudges) > 100 {
			ns.nudges = ns.nudges[len(ns.nudges)-100:]
		}
		_ = ns.save()
	}

	return due
}

// PendingNudges returns undelivered nudges.
func (ns *NudgeSystem) PendingNudges() []Nudge {
	ns.mu.RLock()
	defer ns.mu.RUnlock()

	var pending []Nudge
	for _, n := range ns.nudges {
		if !n.Delivered {
			pending = append(pending, n)
		}
	}
	return pending
}

// MarkDelivered marks a nudge as delivered.
func (ns *NudgeSystem) MarkDelivered(id string) {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	for i := range ns.nudges {
		if ns.nudges[i].ID == id && !ns.nudges[i].Delivered {
			now := time.Now()
			ns.nudges[i].Delivered = true
			ns.nudges[i].DeliveredAt = &now
			_ = ns.save()
			return
		}
	}
}

// FormatNudgeForSystemPrompt builds system prompt content from pending nudges.
func (ns *NudgeSystem) FormatNudgeForSystemPrompt() string {
	var b strings.Builder
	pending := ns.PendingNudges()
	if len(pending) == 0 {
		return ""
	}

	// Only include highest-priority nudges to avoid overwhelming
	highPriority := false
	hasMedium := false
	for _, n := range pending {
		if n.Priority == NudgeHigh {
			highPriority = true
			break
		}
		if n.Priority == NudgeMedium {
			hasMedium = true
		}
	}

	if highPriority {
		b.WriteString("\n--- AGENT NUDGES (PRIORITY) ---\n")
		b.WriteString("You have pending self-improvement nudges. Review and take action:\n\n")
		for _, n := range pending {
			if n.Priority == NudgeHigh {
				b.WriteString(fmt.Sprintf("[%s] %s\n\n", strings.ToUpper(string(n.Type)), n.Message))
			}
		}
	} else if hasMedium {
		b.WriteString("\n--- AGENT NUDGES ---\n")
		b.WriteString("When convenient, consider these self-improvement actions:\n\n")
		for _, n := range pending {
			if n.Priority == NudgeMedium {
				b.WriteString(fmt.Sprintf("[%s] %s\n\n", strings.ToUpper(string(n.Type)), n.Message))
			}
		}
	}

	return b.String()
}

func (ns *NudgeSystem) buildMemoryNudgeMessage() string {
	memorySize := len(ns.memory.Snapshot(MemoryAgent)) + len(ns.memory.Snapshot(MemoryUser))
	var b strings.Builder
	b.WriteString("Review memory and consider these actions:\n\n")

	if memorySize < 200 {
		b.WriteString("1. **Capture recent learnings**: ")
		b.WriteString("Has anything from recent sessions worth remembering? ")
		b.WriteString("User preferences, project context, or work patterns?\n\n")
	}

	b.WriteString("2. **Audit existing memories**: ")
	b.WriteString("Are any entries outdated, contradictory, or no longer relevant? ")
	b.WriteString("Remove stale information.\n\n")

	b.WriteString("3. **Organize for retrieval**: ")
	b.WriteString("Would restructuring entries improve retrieval? Group related memories together.\n")

	return b.String()
}

func (ns *NudgeSystem) buildSkillNudgeMessage(activeCount, staleCount int) string {
	var b strings.Builder
	b.WriteString("Review skill library:\n\n")

	if activeCount == 0 {
		b.WriteString("1. **No active skills**. ")
		b.WriteString("Consider what patterns you've used recently that could be captured as skills. ")
		b.WriteString("Any multi-step workflows worth documenting?\n\n")
	}

	if activeCount > 0 {
		b.WriteString(fmt.Sprintf("1. **%d active skills**. ", activeCount))
		b.WriteString("Have any become outdated or need improvement? ")
		b.WriteString("Review recent usage and update as needed.\n\n")
	}

	if staleCount > 0 {
		b.WriteString(fmt.Sprintf("2. **%d stale skills**. ", staleCount))
		b.WriteString("These haven't been used recently. Should they be archived or updated?\n\n")
	}

	b.WriteString("3. **Skill gaps**: ")
	b.WriteString("Has any task category emerged that has no corresponding skill? ")
	b.WriteString("Creating skills for recurring patterns improves future sessions.\n")

	return b.String()
}

func (ns *NudgeSystem) buildCleanupNudgeMessage() string {
	return strings.Join([]string{
		"Periodic maintenance — consider these actions:",
		"",
		"1. **Memory audit**: Review MEMORY.md for outdated or contradictory entries.",
		"2. **Skill audit**: Check skills for correctness and relevance.",
		"3. **Session review**: Are there recent sessions with insights worth persisting?",
		"4. **Tool configuration**: Is all tooling working correctly? Any broken integrations?",
	}, "\n")
}

// ExplorationNudge checks if there are unexplored capabilities worth suggesting.
// For example, if the user has a Google API key but hasn't used Calendar integration yet.
func (ns *NudgeSystem) ExplorationNudge() *Nudge {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	var suggestions []string

	// Check for configured but unused tools
	if os.Getenv("GOOGLE_API_KEY") != "" || os.Getenv("GEMINI_API_KEY") != "" {
		suggestions = append(suggestions, "- Google Workspace (Calendar, Drive, Gmail) is available")
	}
	if os.Getenv("FAL_KEY") != "" {
		suggestions = append(suggestions, "- Image and video generation is available")
	}
	if os.Getenv("BROWSER_USE_API_KEY") != "" || os.Getenv("BROWSERBASE_API_KEY") != "" {
		suggestions = append(suggestions, "- Cloud browser automation is available")
	}

	if len(suggestions) == 0 {
		return nil
	}

	now := time.Now()
	nudge := Nudge{
		ID:        fmt.Sprintf("nudge-explore-%d", now.Unix()),
		Type:      NudgeExploration,
		Priority:  NudgeLow,
		Message:   "Unexplored capabilities detected:\n" + strings.Join(suggestions, "\n"),
		CreatedAt: now,
	}
	ns.nudges = append(ns.nudges, nudge)
	_ = ns.save()

	return &nudge
}

// LastNudgeTime returns when the last nudge was delivered.
func (ns *NudgeSystem) LastNudgeTime() time.Time {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.lastNudgeTime
}

// Stats returns nudge statistics.
type NudgeStats struct {
	TotalCreated    int `json:"total_created"`
	PendingDelivery int `json:"pending_delivery"`
	Delivered       int `json:"delivered"`
}

func (ns *NudgeSystem) Stats() NudgeStats {
	ns.mu.RLock()
	defer ns.mu.RUnlock()

	stats := NudgeStats{TotalCreated: len(ns.nudges)}
	for _, n := range ns.nudges {
		if n.Delivered {
			stats.Delivered++
		} else {
			stats.PendingDelivery++
		}
	}
	return stats
}

// PatchMemory provides the nudge system access to memory when memory is set after construction.
func (ns *NudgeSystem) SetMemory(m *MemorySystem) {
	ns.mu.Lock()
	ns.memory = m
	ns.mu.Unlock()
}

// PatchSkillManager provides the nudge system access to skill manager.
func (ns *NudgeSystem) SetSkillManager(sm *SkillManager) {
	ns.mu.Lock()
	ns.skillMgr = sm
	ns.mu.Unlock()
}

// PatchSkillUsage provides the nudge system access to skill usage tracker.
func (ns *NudgeSystem) SetSkillUsage(su *SkillUsageTracker) {
	ns.mu.Lock()
	ns.skillUsage = su
	ns.mu.Unlock()
}

// Ensure context import for the system prompt formatting functions.
