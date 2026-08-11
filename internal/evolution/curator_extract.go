package evolution

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ExtractionConfig controls skill extraction behavior.
type ExtractionConfig struct {
	// MinTurnsToExtract is the minimum number of agent turns before extraction is considered.
	MinTurnsToExtract int

	// MinTTLBetweenExtractions prevents extraction from firing too frequently.
	MinTTLBetweenExtractions time.Duration

	// MaxExtractionsPerDay limits daily extractions to avoid noise.
	MaxExtractionsPerDay int
}

// DefaultExtractionConfig returns sensible defaults.
func DefaultExtractionConfig() ExtractionConfig {
	return ExtractionConfig{
		MinTurnsToExtract:        10,
		MinTTLBetweenExtractions: 30 * time.Minute,
		MaxExtractionsPerDay:     5,
	}
}

// ExtractionCandidate represents a potential skill extracted from a trajectory.
type ExtractionCandidate struct {
	// Name is the suggested skill name (e.g., "github-code-review").
	Name string `json:"name"`

	// Description explains what the skill does.
	Description string `json:"description"`

	// Body is the full SKILL.md content.
	Body string `json:"body"`

	// Category is the suggested skill category.
	Category string `json:"category"`

	// Confidence is 0.0-1.0 indicating extraction quality.
	Confidence float64 `json:"confidence"`

	// SourceSession is the session ID that produced this candidate.
	SourceSession string `json:"source_session"`

	// CreatedAt is the extraction timestamp.
	CreatedAt time.Time `json:"created_at"`
}

// ExtractedSkill represents a successfully extracted and saved skill.
type ExtractedSkill struct {
	Candidate ExtractionCandidate `json:"candidate"`
	SkillPath string              `json:"skill_path"`
	CreatedAt time.Time           `json:"created_at"`
	SessionID string              `json:"session_id"`
}

// ExtractorStorage persists extraction-related data.
type ExtractorStorage struct {
	mu         sync.RWMutex
	storePath  string
	candidates []ExtractionCandidate
	created    []ExtractedSkill
}

// NewExtractorStorage creates a new extractor storage.
func NewExtractorStorage(homeDir string) *ExtractorStorage {
	return &ExtractorStorage{
		storePath: filepath.Join(homeDir, ".extractor_state.json"),
	}
}

// Load reads persisted extraction state.
func (es *ExtractorStorage) Load() error {
	es.mu.Lock()
	defer es.mu.Unlock()

	data, err := os.ReadFile(es.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var state struct {
		Candidates []ExtractionCandidate `json:"candidates"`
		Created    []ExtractedSkill      `json:"created"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parse extractor state: %w", err)
	}

	es.candidates = state.Candidates
	es.created = state.Created
	return nil
}

func (es *ExtractorStorage) save() error {
	data, err := json.MarshalIndent(struct {
		Candidates []ExtractionCandidate `json:"candidates"`
		Created    []ExtractedSkill      `json:"created"`
	}{
		Candidates: es.candidates,
		Created:    es.created,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(es.storePath, data, 0644)
}

// AddCandidate stores a potential extraction candidate.
func (es *ExtractorStorage) AddCandidate(c ExtractionCandidate) {
	es.mu.Lock()
	defer es.mu.Unlock()

	// Cap candidates at 50
	if len(es.candidates) >= 50 {
		es.candidates = es.candidates[len(es.candidates)-49:]
	}
	es.candidates = append(es.candidates, c)
	_ = es.save()
}

// AddCreated records a successfully extracted skill.
func (es *ExtractorStorage) AddCreated(s ExtractedSkill) {
	es.mu.Lock()
	defer es.mu.Unlock()

	if len(es.created) >= 200 {
		es.created = es.created[len(es.created)-199:]
	}
	es.created = append(es.created, s)
	_ = es.save()
}

// DailyCreatedCount returns how many skills were created today.
func (es *ExtractorStorage) DailyCreatedCount() int {
	es.mu.RLock()
	defer es.mu.RUnlock()

	today := time.Now().Truncate(24 * time.Hour)
	count := 0
	for i := len(es.created) - 1; i >= 0; i-- {
		if es.created[i].CreatedAt.Before(today) {
			break
		}
		count++
	}
	return count
}

// LastCreatedAt returns the timestamp of the most recent created skill.
func (es *ExtractorStorage) LastCreatedAt() time.Time {
	es.mu.RLock()
	defer es.mu.RUnlock()

	if len(es.created) == 0 {
		return time.Time{}
	}
	return es.created[len(es.created)-1].CreatedAt
}

// TrajectorySkillExtractor analyzes conversation trajectories to identify
// repeatable patterns worth capturing as skills.
type TrajectorySkillExtractor struct {
	storage    *ExtractorStorage
	skillMgr   *SkillManager
	skillUsage *SkillUsageTracker
	logger     *slog.Logger
	cfg        ExtractionConfig
}

// NewTrajectorySkillExtractor creates an extractor.
func NewTrajectorySkillExtractor(
	homeDir string,
	skillMgr *SkillManager,
	skillUsage *SkillUsageTracker,
	cfg ExtractionConfig,
	logger *slog.Logger,
) *TrajectorySkillExtractor {
	es := NewExtractorStorage(homeDir)
	_ = es.Load()
	if logger == nil {
		logger = slog.Default()
	}
	return &TrajectorySkillExtractor{
		storage:    es,
		skillMgr:   skillMgr,
		skillUsage: skillUsage,
		cfg:        cfg,
		logger:     logger,
	}
}

// authoringStandards defines the house-style rules for SKILL.md content,
// distilled from skills/software-development/skill-authoring/SKILL.md.
// Injecting these standards into the extraction prompt ensures distilled
// skills match the quality and structure of hand-written peers.
const authoringStandards = `
SKILL.md AUTHORING STANDARDS

The "body" field you provide will be appended AFTER auto-generated YAML frontmatter
(name/description/version=1.0.0). Do NOT include frontmatter in the body — start
directly with "# Title".

FRONTMATTER RULES (name and description come from the JSON fields above):
  - name: lowercase with hyphens, ≤64 chars, e.g. "fix-go-mod-tidy-errors"
  - description: ≤1024 chars (HARD LIMIT — longer will be rejected). Must start
    with "Use when ..." and describe the trigger class, not one specific task.
    "Use when debugging merge conflicts" > "Resolve merge conflicts"
  - version: 1.0.0 (auto-generated)

BODY STRUCTURE (peer-matched — follow this order):
  # <Human-Readable Title>

  ## Overview
  1-2 paragraphs: what and why.

  ## When to Use
  - Bulleted trigger phrases
  - "Don't use for:" counter-triggers

  ## <Topic sections>
  - Quick-reference tables, code blocks with exact commands
  - Reference covo-agent tools by canonical name: read_file, write_file, edit_block, search_files, bash, patch, apply_patch
  - Do NOT use raw shell names:
    - say "read_file" not "cat"
    - say "search_files" not "grep"
    - say "edit_block" not "sed"

  ## Common Pitfalls
  Numbered list of mistakes and their fixes.

  ## Verification Checklist
  - [ ] Checkbox list of post-action verifications

SIZE TARGETS:
  - Description: ≤1024 chars (enforced).
  - Body: 8-14k chars for a typical skill. If pushing past 20k, note that
    supporting material should go in references/ (but do not create those
    files here — just reference them).

QUALITY:
  - Prefer exact commands/URLs/function signatures that appeared verbatim in
    the source conversation — never invent.
  - The skill should generalize beyond this specific session.
  - Do not include session-specific paths, credentials, or user names.
`

// ExtractionPrompt is the system prompt used to analyze a conversation
// trajectory and determine if a new skill should be created.
const ExtractionPrompt = `You are a skill extraction engine. Analyze the conversation below and determine if a reusable skill should be created.

A good skill captures a repeatable procedural pattern — it answers "how do I do X?" for a class of tasks.

QUALIFYING SIGNALS (any one warrants extraction):
  • A multi-step workflow was successfully executed that could help future sessions.
  • A tool was used in a non-obvious way that's worth documenting.
  • A debugging/fix pattern emerged that generalizes beyond this session.
  • A configuration or setup sequence was discovered that others would benefit from.
  • The user provided explicit feedback on style/format/approach that should persist.

DO NOT extract:
  • One-off queries or factual lookups.
  • Session-specific errors that resolved trivially.
  • Tasks completed in fewer than 3 tool calls.
  • Environment-specific paths or credentials.
` + authoringStandards + `
If extraction is warranted, respond with JSON:
{
  "should_extract": true,
  "name": "suggested-skill-name",
  "category": "category-name",
  "description": "Use when <trigger>. <one-line behavior>.",
  "body": "Full SKILL.md body (WITHOUT frontmatter, starting with # Title)",
  "confidence": 0.85
}

If no extraction is warranted:
{
  "should_extract": false,
  "reason": "Why not (brief)"
}

Respond ONLY with the JSON object, no other text.`

// AnalyzeTrajectory examines a conversation trajectory and determines if a skill should be created.
// It returns the candidate if extraction is warranted, or nil otherwise.
func (e *TrajectorySkillExtractor) AnalyzeTrajectory(
	ctx context.Context,
	trajectory []map[string]any,
	llmCall func(ctx context.Context, systemPrompt, userPrompt string) (string, error),
) (*ExtractionCandidate, error) {
	// Quick guard: don't attempt if trajectory is too short
	if len(trajectory) < e.cfg.MinTurnsToExtract {
		return nil, nil
	}

	// Rate limit: check daily cap
	if e.storage.DailyCreatedCount() >= e.cfg.MaxExtractionsPerDay {
		e.logger.Debug("extraction skipped: daily cap reached", "max", e.cfg.MaxExtractionsPerDay)
		return nil, nil
	}

	// Rate limit: check TTL since last extraction
	lastAt := e.storage.LastCreatedAt()
	if !lastAt.IsZero() && time.Since(lastAt) < e.cfg.MinTTLBetweenExtractions {
		e.logger.Debug("extraction skipped: TTL not elapsed",
			"last", lastAt, "min_ttl", e.cfg.MinTTLBetweenExtractions)
		return nil, nil
	}

	// Build a compact trajectory representation for the LLM
	compactTrajectory := e.compactTrajectory(trajectory)
	if compactTrajectory == "" {
		return nil, nil
	}

	response, err := llmCall(ctx, ExtractionPrompt, compactTrajectory)
	if err != nil {
		e.logger.Warn("extraction LLM call failed", "error", err)
		return nil, nil
	}

	resp, err := e.parseExtractionResponse(response)
	if err != nil {
		e.logger.Warn("failed to parse extraction response", "error", err, "response", response[:min(len(response), 200)])
		return nil, nil
	}

	if resp == nil || !resp.ShouldExtract {
		return nil, nil
	}

	candidate := &ExtractionCandidate{
		Name:        resp.Name,
		Description: resp.Description,
		Body:        resp.Body,
		Category:    resp.Category,
		Confidence:  resp.Confidence,
		CreatedAt:   time.Now(),
	}

	// Store as candidate
	e.storage.AddCandidate(*candidate)

	return candidate, nil
}

type extractionResponse struct {
	ShouldExtract bool    `json:"should_extract"`
	Name          string  `json:"name"`
	Category      string  `json:"category"`
	Description   string  `json:"description"`
	Body          string  `json:"body"`
	Confidence    float64 `json:"confidence"`
	Reason        string  `json:"reason"`
}

func (e *TrajectorySkillExtractor) parseExtractionResponse(response string) (*extractionResponse, error) {
	// Extract JSON from response (handle markdown code blocks)
	jsonStr := strings.TrimSpace(response)
	if strings.HasPrefix(jsonStr, "```") {
		lines := strings.Split(jsonStr, "\n")
		if len(lines) > 1 {
			lines = lines[1:] // skip opening ```
		}
		// Find closing ```
		endIdx := -1
		for i, l := range lines {
			if strings.TrimSpace(l) == "```" {
				endIdx = i
				break
			}
		}
		if endIdx >= 0 {
			lines = lines[:endIdx]
		}
		jsonStr = strings.Join(lines, "\n")
	}

	var resp extractionResponse
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}

	return &resp, nil
}

// compactTrajectory builds a compact text representation of the conversation
// suitable for LLM analysis (max ~8000 chars).
func (e *TrajectorySkillExtractor) compactTrajectory(trajectory []map[string]any) string {
	var b strings.Builder
	toolCallCount := 0

	for _, msg := range trajectory {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)
		if content == "" {
			continue
		}

		switch role {
		case "user":
			// Keep user messages
			b.WriteString(fmt.Sprintf("User: %s\n", truncateStr(content, 500)))
		case "assistant":
			// Count tool calls in assistant messages
			if strings.Contains(content, "tool_calls") || strings.Contains(content, "tool_call") {
				toolCallCount++
				b.WriteString("[Assistant invoked tools]\n")
			} else if len(content) < 200 {
				// Short assistant responses
				b.WriteString(fmt.Sprintf("Assistant: %s\n", content))
			} else {
				b.WriteString(fmt.Sprintf("Assistant: %s\n", truncateStr(content, 300)))
			}
		case "tool":
			// Summarize tool results
			b.WriteString(fmt.Sprintf("[Tool result: %s]\n", truncateStr(content, 200)))
		case "system":
			if len(content) < 100 {
				b.WriteString(fmt.Sprintf("System: %s\n", content))
			}
		}

		// Limit output size
		if b.Len() > 8000 {
			b.WriteString("... [trajectory truncated]\n")
			break
		}
	}

	if toolCallCount < 3 {
		return "" // Don't bother analyzing trivial sessions
	}

	return b.String()
}

// min returns the smaller of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// truncateStr truncates a string to maxLen characters with ellipsis.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
