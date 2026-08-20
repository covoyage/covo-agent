package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/theme"
	"gopkg.in/yaml.v3"

	"github.com/covoyage/covo-agent/internal/agent/approval"
	"github.com/covoyage/covo-agent/internal/cli"
	agenttheme "github.com/covoyage/covo-agent/internal/theme"
)

// skinConfig mirrors internal/agent/skin.go's SkinColors for YAML loading.
type skinConfig struct {
	Theme  string     `yaml:"theme,omitempty"`
	Colors skinColors `yaml:"colors,omitempty"`
}

type skinColors struct {
	Accent        string `yaml:"accent,omitempty"`
	Border        string `yaml:"border,omitempty"`
	BorderAccent  string `yaml:"border_accent,omitempty"`
	Success       string `yaml:"success,omitempty"`
	Error         string `yaml:"error,omitempty"`
	Warning       string `yaml:"warning,omitempty"`
	Text          string `yaml:"text,omitempty"`
	Muted         string `yaml:"muted,omitempty"`
	System        string `yaml:"system,omitempty"`
	UserMessage   string `yaml:"user_message,omitempty"`
	AssistantText string `yaml:"assistant_text,omitempty"`
}

func applySkinOverrides(homeDir string, configTheme ...string) {
	path := filepath.Join(homeDir, "skin.yaml")
	data, err := os.ReadFile(path)

	var colors skinColors
	var themeName string
	if err == nil {
		var sc skinConfig
		if yErr := yaml.Unmarshal(data, &sc); yErr == nil {
			themeName = sc.Theme
			colors = sc.Colors
		} else {
			log.Printf("skin.yaml: parse error: %v", yErr)
		}
	}

	// Fall back to config.yml display.theme if skin.yaml doesn't specify one.
	if themeName == "" && len(configTheme) > 0 {
		themeName = configTheme[0]
	}

	mode := theme.DetectColorMode()
	var sem *theme.SemanticTheme

	if preset := agenttheme.Get(themeName); preset != nil {
		sem = preset.ToSemantic()
	} else if themeName == "light" {
		sem = theme.DefaultSemanticLight()
	} else {
		sem = theme.DefaultSemanticForTerminal()
	}

	override := func(target *string, val string) {
		if val != "" {
			*target = val
		}
	}
	override(&sem.Accent, colors.Accent)
	override(&sem.Border, colors.Border)
	override(&sem.BorderAccent, colors.BorderAccent)
	override(&sem.Success, colors.Success)
	override(&sem.Error, colors.Error)
	override(&sem.Warning, colors.Warning)
	override(&sem.Text, colors.Text)
	override(&sem.Muted, colors.Muted)
	override(&sem.System, colors.System)
	override(&sem.UserMessage, colors.UserMessage)
	override(&sem.AssistantText, colors.AssistantText)

	theme.SetSemanticTheme(sem, mode)
}

// applyNamedTheme applies a named preset theme immediately in-memory.
func applyNamedTheme(name string) {
	if preset := agenttheme.Get(name); preset != nil {
		sem := preset.ToSemantic()
		mode := theme.DetectColorMode()
		theme.SetSemanticTheme(sem, mode)
	}
}

func execModeFromConfig(cfg *cli.Config) string {
	if cfg.Execution != nil && cfg.Execution.Mode != "" {
		return cfg.Execution.Mode
	}
	return os.Getenv("EXECUTION_MODE")
}

func concurrencyFromConfig(cfg *cli.Config) int {
	if cfg.Execution != nil && cfg.Execution.Concurrency > 0 {
		return cfg.Execution.Concurrency
	}
	return 0
}

func computerUseFromConfig(cfg *cli.Config) *bool {
	if cfg.ComputerUse != nil {
		return cfg.ComputerUse
	}
	return nil
}

func contextEngineFromConfig(cfg *cli.Config) string {
	if cfg.Context != nil && cfg.Context.Engine != "" {
		return cfg.Context.Engine
	}
	return os.Getenv("COVO_CONTEXT_ENGINE")
}

func approvalConfigFromCLI(cfg *cli.Config, cliYolo bool) *approval.Config {
	ac := &approval.Config{Mode: "manual", YoloMode: cliYolo}
	if cfg.Approvals != nil {
		if cfg.Approvals.Mode != "" {
			ac.Mode = cfg.Approvals.Mode
		}
	}
	if env := os.Getenv("COVO_YOLO"); env == "1" || env == "true" {
		ac.YoloMode = true
	}
	return ac
}

// displayConfigFromCLI reads display configuration from config file and env.
func displayConfigFromCLI(cfg *cli.Config) (showReasoning bool, thinkingMode string) {
	thinkingMode = "collapsed"
	if cfg.Display != nil {
		showReasoning = cfg.Display.ShowReasoning
		if cfg.Display.ThinkingMode != "" {
			thinkingMode = cfg.Display.ThinkingMode
		}
	}
	// Env overrides
	if v := os.Getenv("COVO_SHOW_REASONING"); v != "" {
		showReasoning = v == "1" || v == "true"
	}
	if v := os.Getenv("COVO_THINKING_MODE"); v != "" {
		thinkingMode = v
	}
	return showReasoning, thinkingMode
}

// thinkingConfigFromCLI builds the thinking/reasoning config from config file and env.
// Accepts effort-level values (none/minimal/low/medium/high/xhigh)
// and maps them to covonaut's internal representation.
func thinkingConfigFromCLI(cfg *cli.Config) *agentcore.ThinkingConfig {
	tc := &agentcore.ThinkingConfig{}
	if cfg.ModelConfig != nil && cfg.ModelConfig.Thinking != nil {
		t := cfg.ModelConfig.Thinking
		if t.Effort != "" {
			tc.Effort = mapEffortToCovonaut(t.Effort)
		}
		if t.Display != "" {
			tc.Display = mapDisplayToCovonaut(t.Display)
		}
	}
	// Env overrides config file.
	if v := os.Getenv("COVO_THINKING_EFFORT"); v != "" {
		tc.Effort = mapEffortToCovonaut(v)
	}
	if v := os.Getenv("COVO_THINKING_DISPLAY"); v != "" {
		tc.Display = mapDisplayToCovonaut(v)
	}
	if tc.Effort == "" && tc.Display == "" {
		return nil
	}
	return tc
}

func workspaceOnlyFromConfig(cfg *cli.Config) bool {
	if cfg.FileTools != nil && cfg.FileTools.WorkspaceOnly {
		return true
	}
	return os.Getenv("COVO_WORKSPACE_ONLY") == "true"
}

// frequencyPenaltyFromCLI / presencePenaltyFromCLI read the opt-in sampling
// penalties used to reduce degenerate repetition loops (see
// internal/agent/stream_health.go's detect-and-cut mitigation for the
// after-the-fact safety net; these reduce the odds of the loop happening at
// all, on providers that support it — e.g. OpenAI-compatible). 0 = provider
// default/unset. Env vars override the config file.
func frequencyPenaltyFromCLI(cfg *cli.Config) float64 {
	v := 0.0
	if cfg.ModelConfig != nil {
		v = cfg.ModelConfig.FrequencyPenalty
	}
	if env := os.Getenv("COVO_FREQUENCY_PENALTY"); env != "" {
		if parsed, err := strconv.ParseFloat(env, 64); err == nil {
			v = parsed
		}
	}
	return v
}

func presencePenaltyFromCLI(cfg *cli.Config) float64 {
	v := 0.0
	if cfg.ModelConfig != nil {
		v = cfg.ModelConfig.PresencePenalty
	}
	if env := os.Getenv("COVO_PRESENCE_PENALTY"); env != "" {
		if parsed, err := strconv.ParseFloat(env, 64); err == nil {
			v = parsed
		}
	}
	return v
}

// resolveModelContextLength returns the context window size for the given model.
// Priority: custom provider config > OpenRouter API > built-in table > 0 (fallback to 128k).
func resolveModelContextLength(cfg *cli.Config, provider, modelID string) int64 {
	if cfg == nil {
		return 0
	}
	for _, p := range cfg.CustomProviders {
		for _, m := range p.Models {
			if m.ID == modelID && m.Context > 0 {
				return int64(m.Context)
			}
		}
	}
	// Try OpenRouter API (cached after first call).
	if provider == "openrouter" {
		if models, err := cli.FetchOpenRouterModels(); err == nil {
			for _, m := range models {
				if m.ID == modelID && m.ContextLength > 0 {
					return int64(m.ContextLength)
				}
			}
		}
	}
	if cl, ok := builtinContextLengths[modelID]; ok {
		return cl
	}
	// Prefix match: "gpt-4o-2024-11-20" → try "gpt-4o"
	for prefix, cl := range builtinContextLengths {
		if strings.HasPrefix(modelID, prefix) {
			return cl
		}
	}
	return 0
}

// builtinContextLengths maps known model IDs to their context window sizes.
// Kept in sync with provider docs; used when the provider API doesn't return
// context_length and no custom provider config is set.
// Source: provider docs as of July 2026.
var builtinContextLengths = map[string]int64{
	// OpenAI — https://platform.openai.com/docs/models
	"gpt-5.6":       1050000,
	"gpt-5.6-sol":   1050000,
	"gpt-5.6-terra": 1050000,
	"gpt-5.6-luna":  1050000,
	"gpt-5.5":       1000000,
	"gpt-5.4":       1000000,
	"gpt-5.4-mini":  1000000,
	"gpt-5.4-nano":  1000000,
	"gpt-5":         400000,
	"gpt-4.1":       1000000,
	"gpt-4.1-mini":  1000000,
	"gpt-4.1-nano":  1000000,
	"gpt-4o":        128000,
	"o3":            200000,
	"o3-pro":        200000,
	"o3-mini":       200000,
	"o4-mini":       200000,

	// Anthropic — https://platform.claude.com/docs/en/about-claude/models
	"claude-opus-4":     1000000,
	"claude-opus-4.5":   1000000,
	"claude-opus-4.6":   1000000,
	"claude-opus-4.7":   1000000,
	"claude-opus-4.8":   1000000,
	"claude-sonnet-4":   1000000,
	"claude-sonnet-4.5": 200000,
	"claude-sonnet-4.6": 1000000,
	"claude-sonnet-5":   1000000,
	"claude-haiku-4.5":  200000,

	// Google — https://ai.google.dev/gemini-api/docs/models
	"gemini-2.5-pro":   1048576,
	"gemini-2.5-flash": 1048576,
	"gemini-2.0-flash": 1048576,

	// DeepSeek — https://api-docs.deepseek.com
	"deepseek-chat":     131072,
	"deepseek-reasoner": 163840,
	"deepseek-v3":       131072,
	"deepseek-r1":       163840,

	// xAI — https://docs.x.ai/developers/models
	"grok-4.5":    500000,
	"grok-4.3":    1000000,
	"grok-4":      256000,
	"grok-3":      131072,
	"grok-3-mini": 131072,
}

// mapEffortToCovonaut translates effort-level values to covonaut values.
// Accepted: none / minimal / low / medium / high / xhigh
// Covonaut: "" / "low" / "medium" / "high" / "max"
func mapEffortToCovonaut(v string) agentcore.ThinkingEffort {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "none":
		return agentcore.ThinkingEffortDefault
	case "minimal":
		return agentcore.ThinkingEffortLow
	case "low":
		return agentcore.ThinkingEffortLow
	case "medium":
		return agentcore.ThinkingEffortMedium
	case "high":
		return agentcore.ThinkingEffortHigh
	case "xhigh", "max":
		return agentcore.ThinkingEffortMax
	default:
		return agentcore.ThinkingEffort(strings.ToLower(strings.TrimSpace(v)))
	}
}

// mapDisplayToCovonaut translates display-mode values to covonaut values.
// Accepted: auto / concise / detailed / none
// Covonaut: "summarized" / "omitted"
func mapDisplayToCovonaut(v string) agentcore.ThinkingDisplay {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "none", "omitted":
		return agentcore.ThinkingDisplayOmitted
	case "auto", "concise", "detailed", "summarized":
		return agentcore.ThinkingDisplaySummarized
	default:
		return agentcore.ThinkingDisplay(strings.ToLower(strings.TrimSpace(v)))
	}
}

// approvalBridge adapts the approval.System to the PermissionGate's interface.
type approvalBridge struct {
	system *approval.System
}

func (b *approvalBridge) CheckCommand(ctx context.Context, command, sessionKey string) *ApprovalDecision {
	d := b.system.CheckCommand(ctx, command, sessionKey)
	return &ApprovalDecision{
		Approved:      d.Approved,
		Hardline:      d.Hardline,
		SmartApproved: d.SmartApproved,
		Message:       d.Message,
		PatternKey:    d.PatternKey,
		Description:   d.Description,
	}
}

func (b *approvalBridge) ApproveSession(sessionKey, patternKey string) {
	b.system.ApproveSession(sessionKey, patternKey)
}

func (b *approvalBridge) ApprovePermanent(patternKey string) {
	b.system.ApprovePermanent(patternKey)
}

func (b *approvalBridge) IsApproved(sessionKey, patternKey string) bool {
	return b.system.IsApproved(sessionKey, patternKey)
}

func (b *approvalBridge) HandleUserChoice(sessionKey, patternKey, description, choice string) *ApprovalDecision {
	d := b.system.HandleUserChoice(sessionKey, patternKey, description, choice)
	return &ApprovalDecision{Approved: d.Approved}
}

func (b *approvalBridge) FirePreApproval(command, patternKey, description string) {
	b.system.FirePreApproval(command, patternKey, description)
}

func (b *approvalBridge) FirePostApproval(command, patternKey, description, choice string) {
	b.system.FirePostApproval(command, patternKey, description, choice)
}

// ghostModelFromConfig returns the model to use for inline ghost completions.
// It checks the user config first, then falls back to a fast default per provider.
func ghostModelFromConfig(cfg *cli.Config, providerType string) string {
	if cfg.ModelConfig != nil && cfg.ModelConfig.GhostModel != "" {
		return cfg.ModelConfig.GhostModel
	}
	return cli.DefaultGhostModel(providerType)
}
