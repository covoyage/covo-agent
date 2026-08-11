// Package evolution: user persona and dialect modeling.
//
// Captures the user's identity, preferences, communication style,
// and mental model — "who the user is and how they think" —
// enabling personalized agent behavior across sessions.
package evolution

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// UserPersona captures the user's enduring traits — the "dialect" of how
// they communicate and what they expect from the agent.
//
// This is the Honcho-inspired user modeling layer. Honcho builds user-state
// from conversation signals; we do the same by observing patterns across sessions.
type UserPersona struct {
	// Identity
	Name     string `json:"name,omitempty"`
	Role     string `json:"role,omitempty"`
	Timezone string `json:"timezone,omitempty"`
	Language string `json:"language,omitempty"`

	// Expertise — what the user knows well
	Expertise  []string `json:"expertise,omitempty"`
	Weaknesses []string `json:"weaknesses,omitempty"` // areas where user wants help

	// Communication dialect — how the user writes and wants to be addressed
	Dialect UserDialect `json:"dialect"`

	// Preferences — behavioral expectations
	Preferences UserPreferences `json:"preferences"`

	// Context — what project/life context matters
	Projects    []string `json:"projects,omitempty"`
	Goals       []string `json:"goals,omitempty"`
	Constraints []string `json:"constraints,omitempty"`

	// Metadata
	LastUpdated  time.Time `json:"last_updated"`
	SessionCount int       `json:"session_count"`
}

// UserDialect captures the user's communication fingerprint.
type UserDialect struct {
	// Tone describes expected response tone.
	// e.g. "professional", "casual", "friendly", "technical", "terse"
	Tone string `json:"tone,omitempty"`

	// Verbosity describes preferred response length.
	// e.g. "concise", "detailed", "balanced"
	Verbosity string `json:"verbosity,omitempty"`

	// Formality describes level of formal language.
	// e.g. "formal", "neutral", "informal"
	Formality string `json:"formality,omitempty"`

	// StyleTags are keywords describing the user's preferred writing style.
	// e.g. ["no-fluff", "data-driven", "visual", "bullet-points"]
	StyleTags []string `json:"style_tags,omitempty"`

	// VocabularyPreferences describes terminology choices.
	// e.g. "use 'deploy' not 'ship'", "say 'component' not 'module'"
	VocabularyPreferences []string `json:"vocabulary_preferences,omitempty"`

	// Format describes preferred response format.
	// e.g. "markdown", "plain-text", "code-first"
	Format string `json:"format,omitempty"`

	// AvoidPatterns are things the user explicitly dislikes.
	// e.g. ["excessive hedging", "walls of text", "unnecessary headers"]
	AvoidPatterns []string `json:"avoid_patterns,omitempty"`

	// LikedPatterns are things the user responds positively to.
	// e.g. ["numbered lists", "examples before theory", "direct answers first"]
	LikedPatterns []string `json:"liked_patterns,omitempty"`
}

// UserPreferences captures behavioral expectations.
type UserPreferences struct {
	// ConfirmBefore destructive actions
	ConfirmBeforeDestructive bool `json:"confirm_before_destructive"`

	// AutoCommit after code changes
	AutoCommit bool `json:"auto_commit"`

	// ShowThinking when reasoning
	ShowThinking bool `json:"show_thinking"`

	// PreferredTools lists tools the user specifically likes
	PreferredTools []string `json:"preferred_tools,omitempty"`

	// DislikedTools lists tools the user avoids
	DislikedTools []string `json:"disliked_tools,omitempty"`

	// BrowserEngine preferred browser engine
	BrowserEngine string `json:"browser_engine,omitempty"`

	// NotificationPreferences for alerts and updates
	NotifyOnCompletion bool   `json:"notify_on_completion"`
	QuietHours         string `json:"quiet_hours,omitempty"` // e.g. "23:00-08:00"
}

// NewUserPersona creates an empty persona.
func NewUserPersona() *UserPersona {
	return &UserPersona{
		Dialect: UserDialect{
			Tone:      "friendly",
			Verbosity: "balanced",
			Formality: "neutral",
		},
		Preferences: UserPreferences{
			ConfirmBeforeDestructive: true,
			NotifyOnCompletion:       false,
		},
		LastUpdated: time.Now(),
	}
}

// LoadUserPersona reads a persisted persona from disk.
func LoadUserPersona(homeDir string) (*UserPersona, error) {
	path := filepath.Join(homeDir, "persona.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewUserPersona(), nil
		}
		return nil, fmt.Errorf("read persona: %w", err)
	}

	var p UserPersona
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse persona: %w", err)
	}
	return &p, nil
}

// Save persists the persona to disk.
func (p *UserPersona) Save(homeDir string) error {
	p.LastUpdated = time.Now()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(homeDir, "persona.json"), data, 0644)
}

// UpdateDialectFromFeedback applies user feedback corrections to the dialect.
// Feedback like "stop being so verbose" or "don't format as code blocks"
// is parsed and applied to the appropriate dialect field.
func (p *UserPersona) UpdateDialectFromFeedback(feedback string) {
	lower := strings.ToLower(strings.TrimSpace(feedback))

	// Verbosity signals
	if strings.Contains(lower, "too verbose") || strings.Contains(lower, "too long") ||
		strings.Contains(lower, "stop explaining") || strings.Contains(lower, "shorter") ||
		strings.Contains(lower, "get to the point") || strings.Contains(lower, "less detail") {
		p.Dialect.Verbosity = "concise"
	}
	if strings.Contains(lower, "too short") || strings.Contains(lower, "more detail") ||
		strings.Contains(lower, "be thorough") || strings.Contains(lower, "explain more") {
		p.Dialect.Verbosity = "detailed"
	}

	// Tone signals
	if strings.Contains(lower, "too formal") || strings.Contains(lower, "relax") {
		p.Dialect.Formality = "informal"
	}
	if strings.Contains(lower, "too casual") || strings.Contains(lower, "be professional") {
		p.Dialect.Formality = "formal"
	}

	// Format signals
	if strings.Contains(lower, "no markdown") || strings.Contains(lower, "stop using markdown") {
		p.Dialect.Format = "plain-text"
		p.Dialect.AvoidPatterns = appendUnique(p.Dialect.AvoidPatterns, "markdown-formatting")
	}
	if strings.Contains(lower, "no code blocks") || strings.Contains(lower, "stop formatting code") {
		p.Dialect.AvoidPatterns = appendUnique(p.Dialect.AvoidPatterns, "code-blocks")
	}
	if strings.Contains(lower, "use bullet points") || strings.Contains(lower, "bullet format") {
		p.Dialect.LikedPatterns = appendUnique(p.Dialect.LikedPatterns, "bullet-points")
	}

	// Pattern captures
	if strings.Contains(lower, "don't") || strings.Contains(lower, "stop doing") ||
		strings.Contains(lower, "never") {
		p.Dialect.AvoidPatterns = appendUnique(p.Dialect.AvoidPatterns, extractPhrase(feedback))
	}

	// Frustration signals → capture as style guidance
	if strings.Contains(lower, "why are you") ||
		strings.Contains(lower, "i hate") ||
		strings.Contains(lower, "you always") {
		p.Dialect.AvoidPatterns = appendUnique(p.Dialect.AvoidPatterns, extractPhrase(feedback))
	}
}

// UpdateFromReviewResult updates persona fields based on background review findings.
func (p *UserPersona) UpdateFromReviewResult(insights []PersonaInsight) {
	for _, insight := range insights {
		switch insight.Field {
		case "name":
			p.Name = insight.Value
		case "role":
			p.Role = insight.Value
		case "timezone":
			p.Timezone = insight.Value
		case "language":
			p.Language = insight.Value
		case "expertise":
			p.Expertise = appendUnique(p.Expertise, insight.Value)
		case "weakness":
			p.Weaknesses = appendUnique(p.Weaknesses, insight.Value)
		case "project":
			p.Projects = appendUnique(p.Projects, insight.Value)
		case "goal":
			p.Goals = appendUnique(p.Goals, insight.Value)
		case "constraint":
			p.Constraints = appendUnique(p.Constraints, insight.Value)
		case "tone":
			p.Dialect.Tone = insight.Value
		case "verbosity":
			p.Dialect.Verbosity = insight.Value
		case "formality":
			p.Dialect.Formality = insight.Value
		case "preferred_tool":
			p.Preferences.PreferredTools = appendUnique(p.Preferences.PreferredTools, insight.Value)
		case "disliked_tool":
			p.Preferences.DislikedTools = appendUnique(p.Preferences.DislikedTools, insight.Value)
		case "avoid_pattern":
			p.Dialect.AvoidPatterns = appendUnique(p.Dialect.AvoidPatterns, insight.Value)
		case "liked_pattern":
			p.Dialect.LikedPatterns = appendUnique(p.Dialect.LikedPatterns, insight.Value)
		case "vocabulary":
			p.Dialect.VocabularyPreferences = appendUnique(p.Dialect.VocabularyPreferences, insight.Value)
		}
	}

	p.SessionCount++
	p.LastUpdated = time.Now()
}

// PersonaInsight represents a single piece of learned information about the user.
type PersonaInsight struct {
	Field      string  `json:"field"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source,omitempty"` // Which session produced this insight
}

// FormatForSystemPrompt returns a persona section for the system prompt.
// Designed to be injected into the Context or Volatile tier.
func (p *UserPersona) FormatForSystemPrompt() string {
	if p == nil || (p.Name == "" && p.Role == "" && p.Dialect.Tone == "" && len(p.Dialect.AvoidPatterns) == 0) {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n--- USER PERSONA (learned) ---\n")

	if p.Name != "" {
		sb.WriteString(fmt.Sprintf("Name: %s\n", p.Name))
	}
	if p.Role != "" {
		sb.WriteString(fmt.Sprintf("Role: %s\n", p.Role))
	}
	if p.Timezone != "" {
		sb.WriteString(fmt.Sprintf("Timezone: %s\n", p.Timezone))
	}
	if p.Language != "" {
		sb.WriteString(fmt.Sprintf("Language: %s\n", p.Language))
	}

	if len(p.Expertise) > 0 {
		sb.WriteString(fmt.Sprintf("Expertise: %s\n", strings.Join(p.Expertise, ", ")))
	}
	if len(p.Projects) > 0 {
		sb.WriteString(fmt.Sprintf("Projects: %s\n", strings.Join(p.Projects, ", ")))
	}

	// Communication style — only include non-default values
	styleParts := []string{}
	if p.Dialect.Tone != "" && p.Dialect.Tone != "friendly" {
		styleParts = append(styleParts, fmt.Sprintf("tone: %s", p.Dialect.Tone))
	}
	if p.Dialect.Verbosity != "" && p.Dialect.Verbosity != "balanced" {
		styleParts = append(styleParts, fmt.Sprintf("verbosity: %s", p.Dialect.Verbosity))
	}
	if p.Dialect.Formality != "" && p.Dialect.Formality != "neutral" {
		styleParts = append(styleParts, fmt.Sprintf("formality: %s", p.Dialect.Formality))
	}
	if p.Dialect.Format != "" {
		styleParts = append(styleParts, fmt.Sprintf("format: %s", p.Dialect.Format))
	}
	if len(p.Dialect.StyleTags) > 0 {
		styleParts = append(styleParts, fmt.Sprintf("tags: %s", strings.Join(p.Dialect.StyleTags, ", ")))
	}

	if len(styleParts) > 0 {
		sb.WriteString("Communication style: ")
		sb.WriteString(strings.Join(styleParts, "; "))
		sb.WriteString("\n")
	}

	// Critical: avoid patterns + liked patterns
	if len(p.Dialect.AvoidPatterns) > 0 {
		sb.WriteString(fmt.Sprintf("AVOID: %s\n", strings.Join(p.Dialect.AvoidPatterns, "; ")))
	}
	if len(p.Dialect.LikedPatterns) > 0 {
		sb.WriteString(fmt.Sprintf("PREFER: %s\n", strings.Join(p.Dialect.LikedPatterns, "; ")))
	}
	if len(p.Dialect.VocabularyPreferences) > 0 {
		sb.WriteString(fmt.Sprintf("Vocabulary: %s\n", strings.Join(p.Dialect.VocabularyPreferences, "; ")))
	}

	// Preferences
	if !p.Preferences.ConfirmBeforeDestructive {
		sb.WriteString("Note: user has disabled confirmation prompts for destructive actions.\n")
	}
	if p.Preferences.AutoCommit {
		sb.WriteString("Note: auto-commit after code changes is enabled.\n")
	}

	return sb.String()
}

// SessionReviewer is the interface that the curator uses to trigger
// background memory/skill/persona reviews after each session.
// Implemented by agent.BackgroundReviewer (avoids circular imports).
type SessionReviewer interface {
	// SpawnReview triggers an async review of the conversation.
	// The review examines the full trajectory and extracts:
	//   - Personal details for the persona model
	//   - Repeatable patterns for skill creation
	//   - User preferences for memory
	SpawnReview(conversation []map[string]any)
}

// PersonaManager manages the user persona lifecycle.
type PersonaManager struct {
	mu      sync.RWMutex
	persona *UserPersona
	homeDir string
	dirty   bool
}

// NewPersonaManager creates a persona manager.
func NewPersonaManager(homeDir string) (*PersonaManager, error) {
	p, err := LoadUserPersona(homeDir)
	if err != nil {
		return nil, err
	}
	return &PersonaManager{
		persona: p,
		homeDir: homeDir,
	}, nil
}

// Persona returns the current persona (read-only copy).
func (pm *PersonaManager) Persona() *UserPersona {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	cp := *pm.persona
	return &cp
}

// UpdateDialect applies user feedback and auto-saves.
func (pm *PersonaManager) UpdateDialect(feedback string) {
	pm.mu.Lock()
	pm.persona.UpdateDialectFromFeedback(feedback)
	pm.dirty = true
	pm.mu.Unlock()
	pm.maybeSave()
}

// ApplyInsights updates persona from review results.
func (pm *PersonaManager) ApplyInsights(insights []PersonaInsight) {
	pm.mu.Lock()
	pm.persona.UpdateFromReviewResult(insights)
	pm.dirty = true
	pm.mu.Unlock()
	pm.maybeSave()
}

// FormatForPrompt returns the persona section for system prompt injection.
func (pm *PersonaManager) FormatForPrompt() string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.persona.FormatForSystemPrompt()
}

func (pm *PersonaManager) maybeSave() {
	pm.mu.RLock()
	dirty := pm.dirty
	pm.mu.RUnlock()
	if !dirty {
		return
	}
	pm.mu.Lock()
	if pm.dirty {
		_ = pm.persona.Save(pm.homeDir)
		pm.dirty = false
	}
	pm.mu.Unlock()
}

// ForceSave writes persona regardless of dirty flag.
func (pm *PersonaManager) ForceSave() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.dirty = false
	return pm.persona.Save(pm.homeDir)
}

// appendUnique appends a value if not already present.
func appendUnique(slice []string, val string) []string {
	val = strings.TrimSpace(val)
	if val == "" {
		return slice
	}
	for _, existing := range slice {
		if strings.EqualFold(existing, val) {
			return slice
		}
	}
	return append(slice, val)
}

// extractPhrase attempts to extract a meaningful short phrase from feedback.
// This is a heuristic for capturing "don't do X" style corrections.
func extractPhrase(feedback string) string {
	// Limit to reasonable length
	if len(feedback) > 80 {
		feedback = feedback[:80]
	}
	return strings.TrimSpace(feedback)
}
