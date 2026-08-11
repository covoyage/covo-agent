package evolution

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/safego"
)

// Curator is the self-improvement engine that manages the full lifecycle:
//   - Skill extraction from conversation trajectories
//   - Periodic nudges for memory/skill/cleanup maintenance
//   - Skill staleness tracking and archiving
//   - Skill improvement suggestions
//   - Session review integration (via SessionReviewer interface)
type Curator struct {
	skillsDir    string
	usage        *SkillUsageTracker
	memory       *MemorySystem
	skillMgr     *SkillManager
	extractor    *TrajectorySkillExtractor
	nudgeSystem  *NudgeSystem
	reviewer     SessionReviewer // BackgroundReviewer for post-session analysis
	interval     time.Duration
	staleAfter   time.Duration
	archiveAfter time.Duration
	logger       *slog.Logger

	// Enhanced curator features
	enableExtraction bool
	enableNudge      bool
	sessionCount     int
}

type CuratorConfig struct {
	Enabled          bool
	IntervalHours    int
	StaleAfterDays   int
	ArchiveAfterDays int

	// Enhanced curator features
	EnableSkillExtraction bool
	EnableNudge           bool
	NudgeConfig           NudgeConfig
	ExtractionConfig      ExtractionConfig
}

func NewCurator(skillsDir string, usage *SkillUsageTracker, cfg CuratorConfig, logger *slog.Logger) *Curator {
	if logger == nil {
		logger = slog.Default()
	}

	c := &Curator{
		skillsDir:        skillsDir,
		usage:            usage,
		interval:         time.Duration(cfg.IntervalHours) * time.Hour,
		staleAfter:       time.Duration(cfg.StaleAfterDays) * 24 * time.Hour,
		archiveAfter:     time.Duration(cfg.ArchiveAfterDays) * 24 * time.Hour,
		logger:           logger,
		enableExtraction: cfg.EnableSkillExtraction,
		enableNudge:      cfg.EnableNudge,
	}

	return c
}

// SetMemory links the curator to a memory system for nudge operations.
func (c *Curator) SetMemory(m *MemorySystem) {
	c.logger.Debug("curator: memory system linked")
	c.memory = m
	if c.nudgeSystem != nil {
		c.nudgeSystem.SetMemory(m)
	}
}

// SetSkillManager links the curator to a skill manager for extraction operations.
func (c *Curator) SetSkillManager(sm *SkillManager) {
	c.skillMgr = sm
	if c.nudgeSystem != nil {
		c.nudgeSystem.SetSkillManager(sm)
	}
	if c.extractor == nil && c.skillsDir != "" && sm != nil && c.usage != nil {
		homeDir := filepath.Dir(c.skillsDir)
		c.extractor = NewTrajectorySkillExtractor(homeDir, sm, c.usage,
			DefaultExtractionConfig(), c.logger)
	}
}

// SetNudgeSystem assigns or creates a nudge system.
func (c *Curator) SetNudgeSystem(cfg NudgeConfig, homeDir string) {
	c.nudgeSystem = NewNudgeSystem(homeDir, c.memory, c.skillMgr, c.usage, cfg, c.logger)
	_ = c.nudgeSystem.Load()
}

// SetReviewer links a SessionReviewer (BackgroundReviewer) to the curator.
// This enables post-session memory/skill/combined reviews via the mature
// review prompts in background_review.go.
func (c *Curator) SetReviewer(r SessionReviewer) {
	c.reviewer = r
}

// TrajectoryExtractor returns the skill extractor or nil if extraction is disabled.
func (c *Curator) TrajectoryExtractor() *TrajectorySkillExtractor {
	return c.extractor
}

// NudgeSystem returns the nudge system or nil if nudging is disabled.
func (c *Curator) NudgeSystemRef() *NudgeSystem {
	return c.nudgeSystem
}

// OnSessionEnd should be called after each session completes to trigger
// extraction analysis and nudge evaluation.
func (c *Curator) OnSessionEnd(trajectory []map[string]any, llmCall func(ctx context.Context, systemPrompt, userPrompt string) (string, error)) {
	c.sessionCount++

	// Trigger the BackgroundReviewer if wired (uses mature review prompts)
	if c.reviewer != nil && len(trajectory) > 0 {
		c.reviewer.SpawnReview(trajectory)
	}

	// Attempt skill extraction if enabled and trajectory is meaningful
	if c.enableExtraction && c.extractor != nil && len(trajectory) > 0 && llmCall != nil {
		safego.SafeGo(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			candidate, err := c.extractor.AnalyzeTrajectory(ctx, trajectory, llmCall)
			if err != nil {
				c.logger.Warn("curator: skill extraction failed", "error", err)
				return
			}
			if candidate != nil {
				c.logger.Info("curator: skill candidate extracted",
					"name", candidate.Name,
					"confidence", candidate.Confidence,
				)
				// Auto-create high-confidence candidates
				if candidate.Confidence >= 0.8 {
					if err := c.createSkillFromCandidate(*candidate); err != nil {
						c.logger.Warn("curator: auto-create skill failed", "name", candidate.Name, "error", err)
					} else {
						c.logger.Info("curator: skill auto-created", "name", candidate.Name)
					}
				}
			}
		}, c.logger)
	}

	// Evaluate nudges if enabled
	if c.enableNudge && c.nudgeSystem != nil {
		safego.SafeGo(func() {
			nudges := c.nudgeSystem.Tick(c.sessionCount)
			for _, n := range nudges {
				c.logger.Info("curator: nudge generated",
					"type", n.Type,
					"priority", n.Priority,
					"id", n.ID,
				)
			}
		}, c.logger)
	}
}

// createSkillFromCandidate persists a high-confidence extraction candidate as a real skill.
func (c *Curator) createSkillFromCandidate(candidate ExtractionCandidate) error {
	if c.skillMgr == nil {
		return fmt.Errorf("skill manager not initialized")
	}

	// Validate the candidate before creating
	if candidate.Name == "" || candidate.Body == "" {
		return fmt.Errorf("candidate missing name or body")
	}

	_, err := c.skillMgr.Create(candidate.Name, candidate.Description, candidate.Body)
	return err
}

// PendingNudgesForPrompt returns system prompt content for pending nudges.
func (c *Curator) PendingNudgesForPrompt() string {
	if c.nudgeSystem == nil {
		return ""
	}
	return c.nudgeSystem.FormatNudgeForSystemPrompt()
}

func (c *Curator) Start(ctx context.Context) {
	if c.interval <= 0 {
		c.logger.Warn("curator interval is zero or negative, using default 168h")
		c.interval = 168 * time.Hour
	}

	c.logger.Info("curator started",
		"interval", c.interval,
		"stale_after", c.staleAfter,
		"archive_after", c.archiveAfter,
	)

	c.run()

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("curator stopped")
			return
		case <-ticker.C:
			c.run()
		}
	}
}

func (c *Curator) run() {
	c.logger.Debug("curator running maintenance cycle")

	records := c.usage.AllRecords()
	now := time.Now().UTC()
	bundledManifest := readManifest(c.skillsDir)

	staleCount := 0
	archiveCount := 0

	for _, rec := range records {
		id := rec.ID
		if id == "" {
			id = rec.Name
		}
		if rec.Pinned || c.isBundledSkill(id, bundledManifest) {
			continue
		}

		lastActivity, err := time.Parse(time.RFC3339, rec.LastActivityAt)
		if err != nil {
			c.logger.Warn("invalid last_activity_at for skill", "name", rec.Name, "value", rec.LastActivityAt)
			continue
		}

		inactiveDuration := now.Sub(lastActivity)

		switch rec.State {
		case StateActive:
			if inactiveDuration > c.staleAfter {
				if err := c.usage.SetState(id, StateStale); err != nil {
					c.logger.Warn("failed to mark skill stale", "name", rec.Name, "error", err)
				} else {
					c.logger.Info("marked skill stale", "name", rec.Name, "inactive_days", int(inactiveDuration.Hours()/24))
					staleCount++
				}
			}

		case StateStale:
			if inactiveDuration > c.archiveAfter {
				id := rec.ID
				if id == "" {
					id = rec.Name
				}
				if err := c.archiveSkill(id); err != nil {
					c.logger.Warn("failed to archive skill", "name", id, "error", err)
				} else {
					c.logger.Info("archived stale skill", "name", id, "inactive_days", int(inactiveDuration.Hours()/24))
					archiveCount++
				}
			}
		}
	}

	if staleCount > 0 || archiveCount > 0 {
		c.logger.Info("curator cycle complete", "stale", staleCount, "archived", archiveCount)
	}
}

func (c *Curator) isBundledSkill(id string, manifest map[string]string) bool {
	if record, ok := c.usage.GetRecord(id); ok && record.Provenance == "bundled" {
		return true
	}
	_, bundled := manifest[id]
	return bundled
}

func (c *Curator) archiveSkill(id string) error {
	skillDir := filepath.Join(c.skillsDir, filepath.FromSlash(id))
	if c.skillMgr != nil {
		if skill, ok := c.skillMgr.Find(id); ok {
			skillDir = skill.Dir
		}
	}
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		return c.usage.SetState(id, StateArchived)
	}

	archiveDir := filepath.Join(c.skillsDir, ".archive")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return fmt.Errorf("create archive dir: %w", err)
	}

	archiveName := strings.ReplaceAll(filepath.ToSlash(id), "/", "__")
	target := filepath.Join(archiveDir, archiveName)
	if _, err := os.Stat(target); err == nil {
		target = filepath.Join(archiveDir, fmt.Sprintf("%s-%d", archiveName, time.Now().Unix()))
	}

	if err := os.Rename(skillDir, target); err != nil {
		return fmt.Errorf("move to archive: %w", err)
	}

	return c.usage.SetState(id, StateArchived)
}

func (c *Curator) RunNow() {
	c.logger.Info("curator manual run triggered")
	c.run()
}

// Distill runs skill extraction on the given trajectory on demand (the same
// pipeline used automatically at session end) and, if a candidate is produced,
// persists it as a skill. Returns the candidate (may be nil) and whether a skill
// was created.
func (c *Curator) Distill(
	ctx context.Context,
	trajectory []map[string]any,
	llmCall func(ctx context.Context, systemPrompt, userPrompt string) (string, error),
) (*ExtractionCandidate, bool, error) {
	if c.extractor == nil {
		return nil, false, fmt.Errorf("skill extraction is not enabled")
	}
	if llmCall == nil {
		return nil, false, fmt.Errorf("no LLM available for extraction")
	}
	candidate, err := c.extractor.AnalyzeTrajectory(ctx, trajectory, llmCall)
	if err != nil || candidate == nil {
		return candidate, false, err
	}
	created := false
	if c.skillMgr != nil {
		if err := c.createSkillFromCandidate(*candidate); err != nil {
			return candidate, false, err
		}
		created = true
	}
	return candidate, created, nil
}

// Dream audits the memory system for stale entries, contradictions, and bloat,
// returning the findings. It exposes the otherwise-dormant MemoryAuditor on
// demand (advisory: it reports what to consolidate rather than
// destructively deleting).
func (c *Curator) Dream() (*AuditResult, error) {
	if c.memory == nil {
		return nil, fmt.Errorf("memory system is not linked")
	}
	auditor := NewMemoryAuditor(c.memory, c.logger)
	return auditor.Audit(DefaultAuditConfig())
}
