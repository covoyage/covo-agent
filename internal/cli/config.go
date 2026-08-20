package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/covoyage/covo-agent/internal/cli/keychain"
	"gopkg.in/yaml.v3"
)

type MCPServerConfig struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args,omitempty"`
	Env     []string `yaml:"env,omitempty"`
	Timeout int      `yaml:"timeout,omitempty"`
}

type AuxiliaryModelConfig struct {
	Model    string `yaml:"model,omitempty"`
	Provider string `yaml:"provider,omitempty"`
	BaseURL  string `yaml:"base_url,omitempty"`
	APIKey   string `yaml:"api_key,omitempty"`
}

type AuxiliaryConfig struct {
	Compression *AuxiliaryModelConfig `yaml:"compression,omitempty"`
	Vision      *AuxiliaryModelConfig `yaml:"vision,omitempty"`
	WebExtract  *AuxiliaryModelConfig `yaml:"web_extract,omitempty"`
	Title       *AuxiliaryModelConfig `yaml:"title,omitempty"`
	Review      *AuxiliaryModelConfig `yaml:"review,omitempty"`
}

type ExecutionConfig struct {
	Mode        string `yaml:"mode,omitempty"` // "serial" or "parallel"
	Concurrency int    `yaml:"concurrency,omitempty"`
}

type ContextConfig struct {
	Engine string `yaml:"engine,omitempty"` // context engine: "enhanced", "compressor", "truncate"
}

type ApprovalsConfig struct {
	Mode string `yaml:"mode,omitempty"` // "manual", "smart", or "off"
}

// CustomModel represents a manually-entered model for a custom provider.
type CustomModel struct {
	Name    string `yaml:"name"`    // display name (e.g. "DeepSeek Chat")
	ID      string `yaml:"id"`      // API model ID (e.g. "deepseek-chat")
	Context int    `yaml:"context"` // context window size in tokens
}

// CustomProvider represents a user-defined provider configuration.
type CustomProvider struct {
	Name      string        `yaml:"name"`             // display name (e.g. "My DeepSeek")
	Protocol  string        `yaml:"protocol"`         // "openai/chat", "openai/responses", "anthropic", "gemini"
	BaseURL   string        `yaml:"base_url"`         // no default, user must provide
	APIKeyEnv string        `yaml:"api_key_env"`      // env var that holds the API key
	Models    []CustomModel `yaml:"models,omitempty"` // fallback models when API doesn't list them
}

// TypeName returns the internal registry type name for this custom provider.
func (cp *CustomProvider) TypeName() string {
	return ProviderTypeName(cp.Name)
}

// ThinkingConfig mirrors the covonaut thinking configuration.
type ThinkingConfig struct {
	Effort  string `yaml:"effort,omitempty"`  // none / minimal / low / medium / high / xhigh
	Display string `yaml:"display,omitempty"` // auto / concise / detailed / none
}

// DisplayConfig controls visual presentation of model output.
type DisplayConfig struct {
	Language      string `yaml:"language,omitempty"`       // UI language: en, zh, zh-hant, ja, ko, etc.
	ShowReasoning bool   `yaml:"show_reasoning,omitempty"` // show thinking blocks (default: false)
	ThinkingMode  string `yaml:"thinking_mode,omitempty"`  // collapsed / truncated / full (default: collapsed)
	Theme         string `yaml:"theme,omitempty"`          // color theme preset name (e.g. "dracula", "nord")
}

// ModelConfig contains model-level generation controls.
type ModelConfig struct {
	Thinking *ThinkingConfig `yaml:"thinking,omitempty"`

	// GhostModel overrides the model used for inline ghost completions.
	// Defaults to a fast model based on the active provider.
	GhostModel string `yaml:"ghost_model,omitempty"`

	// FrequencyPenalty / PresencePenalty reduce the model's tendency to
	// stream the same text over and over (degeneration/repetition loops).
	// 0 = provider default (unset). Only honored by OpenAI-compatible
	// providers; ignored elsewhere. Not all models accept non-zero values
	// (some reasoning models reject them), so this is opt-in, never
	// defaulted automatically.
	FrequencyPenalty float64 `yaml:"frequency_penalty,omitempty"`
	PresencePenalty  float64 `yaml:"presence_penalty,omitempty"`
}

type FileToolsConfig struct {
	WorkspaceOnly bool `yaml:"workspace_only,omitempty"`
}

// CustomModeConfig defines a user-created agent mode with its own system prompt
// and optional tool restrictions.
type CustomModeConfig struct {
	Name         string           `yaml:"name"`
	Description  string           `yaml:"description,omitempty"`
	SystemPrompt string           `yaml:"system_prompt"`
	Tools        *CustomModeTools `yaml:"tools,omitempty"`
}

// CustomModeTools optionally restricts which tools are available in a custom mode.
type CustomModeTools struct {
	Allow []string `yaml:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty"`
}

type Config struct {
	Provider        string                     `yaml:"provider"`
	Model           string                     `yaml:"model"`
	Mode            string                     `yaml:"mode"`
	ScopedModels    []string                   `yaml:"scoped_models,omitempty"`
	ModelConfig     *ModelConfig               `yaml:"model_config,omitempty"`
	Display         *DisplayConfig             `yaml:"display,omitempty"`
	Env             map[string]string          `yaml:"env,omitempty"`
	Skills          *SkillsConfig              `yaml:"skills,omitempty"`
	Curator         *CuratorConfig             `yaml:"curator,omitempty"`
	Auxiliary       *AuxiliaryConfig           `yaml:"auxiliary,omitempty"`
	MCPServers      map[string]MCPServerConfig `yaml:"mcp_servers,omitempty"`
	Execution       *ExecutionConfig           `yaml:"execution,omitempty"`
	ComputerUse     *bool                      `yaml:"computer_use,omitempty"`
	Context         *ContextConfig             `yaml:"context,omitempty"`
	Approvals       *ApprovalsConfig           `yaml:"approvals,omitempty"`
	FileTools       *FileToolsConfig           `yaml:"file_tools,omitempty"`
	CustomProviders []CustomProvider           `yaml:"custom_providers,omitempty"`
	CustomModes     []CustomModeConfig         `yaml:"custom_modes,omitempty"`
}

type SkillsConfig struct {
	GuardAgentCreated bool              `yaml:"guard_agent_created"`
	AdditionalDirs    []string          `yaml:"additional_dirs,omitempty"`
	HubURL            string            `yaml:"hub_url,omitempty"`
	URLs              []string          `yaml:"urls,omitempty"`
	Disabled          []string          `yaml:"disabled,omitempty"`
	Tier              string            `yaml:"tier,omitempty"`
	Config            map[string]string `yaml:"config,omitempty"`
}

type CuratorConfig struct {
	Enabled          bool `yaml:"enabled"`
	IntervalHours    int  `yaml:"interval_hours"`
	StaleAfterDays   int  `yaml:"stale_after_days"`
	ArchiveAfterDays int  `yaml:"archive_after_days"`
}

func DefaultConfig() *Config {
	return &Config{
		Provider: "openai",
		Model:    "gpt-5.6",
		Mode:     "general",
		Skills: &SkillsConfig{
			GuardAgentCreated: false,
		},
		Curator: &CuratorConfig{
			Enabled:          true,
			IntervalHours:    168,
			StaleAfterDays:   30,
			ArchiveAfterDays: 90,
		},
	}
}

func HomeDir() (string, error) {
	return HomeDirWithProfile(ResolveActiveProfile())
}

func ConfigPath() (string, error) {
	home, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "config.yaml"), nil
}

// projectConfigFile is the name of the per-project config file.
const projectConfigFile = ".covo-agent.yaml"

// FindProjectConfigPath walks up from dir (typically CWD) looking for a
// project-level config file. It stops at the first directory containing
// <projectConfigFile>, at a git root (a directory containing ".git"),
// or at the user's home directory — whichever comes first.
// Returns the path to the found file, or "" if none exists.
func FindProjectConfigPath(dir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve project dir: %w", err)
	}

	prev := ""
	for current := abs; current != prev; prev, current = current, filepath.Dir(current) {
		// Stop at home directory — never look above it.
		if home != "" {
			if same, _ := sameFile(current, home); same {
				return "", nil
			}
		}

		// If we hit a git root, check for project config here before stopping.
		if isGitRoot(current) {
			candidate := filepath.Join(current, projectConfigFile)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
			return "", nil
		}

		candidate := filepath.Join(current, projectConfigFile)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", nil
}

// isGitRoot reports whether dir contains a ".git" entry.
func isGitRoot(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && fi.IsDir()
}

// sameFile reports whether two paths refer to the same file (or directory).
func sameFile(a, b string) (bool, error) {
	sa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(sa, sb), nil
}

// LoadConfig loads the global config from ~/.covo-agent/config.yaml and, if
// a project-level config exists near CWD, merges it on top (project values
// override global defaults). Returns the merged Config.
func LoadConfig() (*Config, error) {
	cfg, err := loadGlobalConfig()
	if err != nil {
		return nil, err
	}

	// Merge project-level config if present.
	cwd, err := os.Getwd()
	if err == nil {
		projPath, pErr := FindProjectConfigPath(cwd)
		if pErr == nil && projPath != "" {
			projCfg, pErr := loadConfigFile(projPath)
			if pErr == nil {
				cfg = MergeConfig(cfg, projCfg)
			}
		}
	}

	return cfg, nil
}

// loadGlobalConfig loads config from the canonical global location
// (~/.covo-agent/config.yaml or profile variant).
func loadGlobalConfig() (*Config, error) {
	cfgPath, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	return loadConfigFile(cfgPath)
}

func LoadConfigFrom(dir string) (*Config, error) {
	cfgPath := filepath.Join(dir, "config.yaml")
	return loadConfigFile(cfgPath)
}

func loadConfigFile(cfgPath string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	expanded := os.ExpandEnv(string(data))
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, friendlyYAMLError(err, string(data), cfgPath)
	}

	return cfg, nil
}

// MergeConfig merges the project config on top of the global config.
// Fields set in project override global; maps are merged key-by-key;
// slices from project are appended; nil/zero fields leave global intact.
func MergeConfig(global, project *Config) *Config {
	if project == nil {
		return global
	}
	out := *global // shallow copy

	if project.Provider != "" {
		out.Provider = project.Provider
	}
	if project.Model != "" {
		out.Model = project.Model
	}
	if project.Mode != "" {
		out.Mode = project.Mode
	}
	if project.ModelConfig != nil {
		out.ModelConfig = project.ModelConfig
	}
	if project.Display != nil {
		out.Display = project.Display
	}
	if project.Env != nil {
		if out.Env == nil {
			out.Env = project.Env
		} else {
			for k, v := range project.Env {
				out.Env[k] = v
			}
		}
	}
	if project.Skills != nil {
		if out.Skills == nil {
			out.Skills = project.Skills
		} else {
			sk := *out.Skills
			if project.Skills.GuardAgentCreated {
				sk.GuardAgentCreated = true
			}
			sk.AdditionalDirs = append(sk.AdditionalDirs, project.Skills.AdditionalDirs...)
			sk.URLs = append(sk.URLs, project.Skills.URLs...)
			if project.Skills.HubURL != "" {
				sk.HubURL = project.Skills.HubURL
			}
			sk.Disabled = append(sk.Disabled, project.Skills.Disabled...)
			if project.Skills.Tier != "" {
				sk.Tier = project.Skills.Tier
			}
			if project.Skills.Config != nil {
				if sk.Config == nil {
					sk.Config = project.Skills.Config
				} else {
					for k, v := range project.Skills.Config {
						sk.Config[k] = v
					}
				}
			}
			out.Skills = &sk
		}
	}
	if project.Curator != nil {
		out.Curator = project.Curator
	}
	if project.Auxiliary != nil {
		out.Auxiliary = project.Auxiliary
	}
	if project.MCPServers != nil {
		if out.MCPServers == nil {
			out.MCPServers = project.MCPServers
		} else {
			for k, v := range project.MCPServers {
				out.MCPServers[k] = v
			}
		}
	}
	if project.Execution != nil {
		out.Execution = project.Execution
	}
	if project.ComputerUse != nil {
		out.ComputerUse = project.ComputerUse
	}
	if project.Context != nil {
		out.Context = project.Context
	}
	if project.Approvals != nil {
		out.Approvals = project.Approvals
	}
	if project.FileTools != nil {
		out.FileTools = project.FileTools
	}
	out.CustomProviders = append(out.CustomProviders, project.CustomProviders...)
	out.CustomModes = append(out.CustomModes, project.CustomModes...)

	return &out
}

// envDenyList is the list of environment variable names (case-insensitive)
// that are blocked from being saved via SaveEnvValue because they can be
// used for code injection or privilege escalation.
var envDenyList = []string{
	"LD_PRELOAD",
	"LD_LIBRARY_PATH",
	"DYLD_INSERT_LIBRARIES",
	"DYLD_LIBRARY_PATH",
	"PYTHONPATH",
	"RUBYLIB",
	"PERL5LIB",
	"JAVA_TOOL_OPTIONS",
	"NODE_OPTIONS",
	"GEM_HOME",
	"GEM_PATH",
	"LD_AUDIT",
	"LD_DEBUG",
	"LD_ORIGIN_PATH",
	"LD_RUN_PATH",
	"PROMPT_COMMAND",
	"BASH_ENV",
	"BASH_FUNC_",
	"ENV",
}

// ValidateEnvKey returns an error if key is in the deny list of dangerous
// environment variables that could be used for code injection.
func ValidateEnvKey(key string) error {
	upper := strings.ToUpper(strings.TrimSpace(key))
	for _, blocked := range envDenyList {
		if strings.EqualFold(blocked, upper) {
			return fmt.Errorf("blocked: %q is a dangerous environment variable and cannot be set via .env (it can be used for code injection)", key)
		}
		// Also block keys that start with BASH_FUNC_ (they have a suffix like BASH_FUNC_foo%%)
		if strings.HasPrefix(upper, "BASH_FUNC_") {
			return fmt.Errorf("blocked: %q matches BASH_FUNC_* pattern (shell function injection)", key)
		}
	}
	return nil
}

// friendlyYAMLError produces a user-friendly error message for config.yaml
// parse errors, showing the offending line, surrounding context, and
// actionable fix suggestions.
func friendlyYAMLError(err error, raw string, path string) error {
	// Try to extract line/column info from yaml.v3 type errors.
	var line, col int
	var msg string

	// yaml.v3 uses *yaml.TypeError and *yaml.LineError internally.
	// The error message format is "yaml: line N: description" or
	// "yaml: line N: column M: description".
	errStr := err.Error()

	// Parse "yaml: line X: ..." or "yaml: line X: column Y: ..."
	if rest, ok := cutPrefix(errStr, "yaml: line "); ok {
		parts := strings.SplitN(rest, ":", 3)
		if len(parts) >= 1 {
			if n, parseErr := strconv.Atoi(strings.TrimSpace(parts[0])); parseErr == nil {
				line = n
			}
		}
		// Check for "column Y" pattern
		if len(parts) >= 2 {
			secondPart := strings.TrimSpace(parts[1])
			lowerSecond := strings.ToLower(secondPart)
			if strings.HasPrefix(lowerSecond, "column ") {
				colStr := strings.TrimPrefix(lowerSecond, "column ")
				if n, parseErr := strconv.Atoi(strings.TrimSpace(colStr)); parseErr == nil {
					col = n
				}
				// The remaining part is the actual error message
				if len(parts) >= 3 {
					msg = strings.TrimSpace(parts[2])
				}
			} else {
				msg = secondPart
			}
		}
	}

	if msg == "" {
		msg = errStr
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Error parsing %s:\n", path))

	if line > 0 {
		if col > 0 {
			b.WriteString(fmt.Sprintf("  line %d, column %d: %s\n", line, col, msg))
		} else {
			b.WriteString(fmt.Sprintf("  line %d: %s\n", line, msg))
		}

		// Show context around the error line.
		lines := strings.Split(raw, "\n")
		contextStart := line - 2
		if contextStart < 1 {
			contextStart = 1
		}
		contextEnd := line + 2
		if contextEnd > len(lines) {
			contextEnd = len(lines)
		}

		b.WriteString("\n")
		for i := contextStart; i <= contextEnd; i++ {
			prefix := "  "
			if i == line {
				prefix = "> "
			}
			lineNum := fmt.Sprintf("%d", i)
			padding := strings.Repeat(" ", 4-len(lineNum))
			b.WriteString(fmt.Sprintf("%s%s| %s\n", prefix, padding+lineNum, lines[i-1]))
		}
		b.WriteString("\n")

		// Suggest fixes.
		b.WriteString("Suggestions:\n")
		suggestions := yamlFixSuggestions(lines, line, msg)
		for _, s := range suggestions {
			b.WriteString(fmt.Sprintf("  - %s\n", s))
		}
	} else {
		b.WriteString(fmt.Sprintf("  %s\n", msg))
		b.WriteString("\nSuggestions:\n")
		b.WriteString("  - Check for missing quotes around special characters\n")
		b.WriteString("  - Check for inconsistent indentation (use spaces, not tabs)\n")
		b.WriteString("  - Ensure all keys are followed by a colon (key: value)\n")
	}

	return fmt.Errorf("%s", b.String())
}

// yamlFixSuggestions returns context-aware fix suggestions based on the
// error message and the content of the error line.
func yamlFixSuggestions(lines []string, line int, msg string) []string {
	var suggestions []string
	errLine := ""
	if line > 0 && line <= len(lines) {
		errLine = strings.TrimRight(lines[line-1], " \t")
	}

	lowerMsg := strings.ToLower(msg)

	// Missing colon after key
	if strings.Contains(lowerMsg, "did not find expected key") ||
		strings.Contains(lowerMsg, "did not find expected") {
		if errLine != "" && !strings.Contains(errLine, ":") {
			// Guess the key name from the line
			key := strings.Fields(errLine)
			if len(key) > 0 {
				suggestions = append(suggestions,
					fmt.Sprintf("Missing \":\" after key %q? Try: \"%s:\"", key[0], key[0]))
			}
		}
		if errLine != "" && !strings.HasSuffix(errLine, ":") {
			suggestions = append(suggestions,
				"Missing \":\" after a mapping key — each key needs a colon and a space")
		}
	}

	// Indentation issues
	if strings.Contains(lowerMsg, "mapping values are not allowed") {
		if errLine != "" && strings.Contains(errLine, ":") && !strings.Contains(errLine, ": ") {
			suggestions = append(suggestions,
				"Missing space after \":\"? YAML requires a space after the colon: \"key: value\"")
		}
		suggestions = append(suggestions,
			"Check indentation — child values must be indented more than their parent")
	}

	// Tab characters
	if errLine != "" && strings.Contains(errLine, "\t") {
		suggestions = append(suggestions,
			"Tab characters detected — YAML requires spaces for indentation, not tabs")
	}

	// Trailing or invalid characters
	if strings.Contains(lowerMsg, "found character") ||
		strings.Contains(lowerMsg, "cannot unmarshal") {
		suggestions = append(suggestions,
			"Check for special characters that need quoting (e.g. \":\", \"#\", \"&\", \"*\")")
	}

	// Generic fallbacks
	if len(suggestions) == 0 {
		suggestions = append(suggestions,
			"Check syntax with a YAML validator (e.g. https://www.yamllint.com)")
		suggestions = append(suggestions,
			"Use consistent indentation with spaces (2 or 4 spaces per level)")
		suggestions = append(suggestions,
			"Ensure all string values containing special characters are quoted")
	}

	return suggestions
}

// cutPrefix removes the prefix from s (case-sensitive). Returns the
// remainder and true if the prefix was present.
func cutPrefix(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

func EnsureHomeDir() (string, error) {
	home, err := HomeDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(home, 0755); err != nil {
		return "", fmt.Errorf("create home dir: %w", err)
	}
	return home, nil
}

func ResolveProvider(cfg *Config) string {
	if v := os.Getenv("PROVIDER"); v != "" {
		return strings.ToLower(v)
	}
	if cfg.Provider != "" {
		return strings.ToLower(cfg.Provider)
	}
	return autoDetectProvider()
}

func autoDetectProvider() string {
	detectors := []struct {
		envVar string
		prov   string
	}{
		{"XIAOMI_API_KEY", "xiaomi"},
		{"OPENROUTER_API_KEY", "openrouter"},
		{"ANTHROPIC_API_KEY", "anthropic"},
		{"GEMINI_API_KEY", "gemini"},
		{"GOOGLE_API_KEY", "gemini"},
		{"CUSTOM_API_KEY", "custom"},
		{"OPENAI_API_KEY", "openai"},
		{"API_KEY", "openai"},
	}
	for _, d := range detectors {
		if os.Getenv(d.envVar) != "" {
			return d.prov
		}
	}
	return "openai"
}

func ResolveModel(cfg *Config) string {
	if v := os.Getenv("AGENT_MODEL"); v != "" {
		return v
	}
	if cfg.Model != "" {
		return cfg.Model
	}
	return "gpt-5.6"
}

func ResolveMode(cfg *Config) string {
	if v := os.Getenv("AGENT_MODE"); v != "" {
		return strings.ToLower(v)
	}
	if cfg.Mode != "" {
		return strings.ToLower(cfg.Mode)
	}
	return "general"
}

func EnvPath() (string, error) {
	home, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".env"), nil
}

func LoadDotEnv() error {
	// Phase 1: load from .env file (backward compat, lower priority).
	envPath, err := EnvPath()
	if err != nil {
		return err
	}

	if f, err := os.Open(envPath); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			idx := strings.Index(line, "=")
			if idx < 0 {
				continue
			}
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			val = strings.Trim(val, `"'`)
			if key != "" && os.Getenv(key) == "" {
				os.Setenv(key, val)
			}
		}
		if err := scanner.Err(); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("open .env: %w", err)
	}

	// Phase 2: overlay Keychain values (higher priority – they were saved
	// from the most recent config wizard / model-picker).  Keychain
	// overrides any conflicting .env entries.
	overlayEnvFromKeychain()
	return nil
}

func overlayEnvFromKeychain() {
	// Collect all known provider API key env vars from the registration table.
	seen := map[string]bool{}
	providerRegMu.RLock()
	for _, reg := range providerReg {
		if seen[reg.Type] {
			continue
		}
		seen[reg.Type] = true
		for _, keyEnv := range reg.APIKeyEnvs {
			if _, ok := seen[keyEnv]; ok {
				continue
			}
			seen[keyEnv] = true
			if val, err := keychain.Get(keyEnv, keychain.Auto); err == nil && val != "" {
				os.Setenv(keyEnv, val)
			}
		}
	}
	providerRegMu.RUnlock()
	// Also cover custom provider keys outside the registry.
	for _, key := range []string{"CUSTOM_API_KEY", "CUSTOM_FF_API_KEY"} {
		if _, ok := seen[key]; ok {
			continue
		}
		if val, err := keychain.Get(key, keychain.Auto); err == nil && val != "" {
			os.Setenv(key, val)
		}
	}
}

func SaveConfig(cfg *Config) error {
	cfgPath, err := ConfigPath()
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func SaveConfigToDir(cfg *Config, dir string) error {
	cfgPath := filepath.Join(dir, "config.yaml")
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(cfgPath, data, 0644)
}

func SaveEnvValue(key, value string) error {
	if err := ValidateEnvKey(key); err != nil {
		return err
	}

	// Phase 1: save to system keychain (auto mode → fall back to file).
	if err := keychain.Set(key, value, keychain.Auto); err != nil {
		return fmt.Errorf("keychain: %w", err)
	}

	// Phase 2: also save to .env for backward compatibility (plain file).
	envPath, err := EnvPath()
	if err != nil {
		return err
	}

	existing := make(map[string]string)
	if f, err := os.Open(envPath); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			idx := strings.Index(line, "=")
			if idx < 0 {
				continue
			}
			k := strings.TrimSpace(line[:idx])
			v := strings.TrimSpace(line[idx+1:])
			v = strings.Trim(v, `"'`)
			existing[k] = v
		}
		f.Close()
	}

	existing[key] = value

	var lines []string
	for k, v := range existing {
		lines = append(lines, fmt.Sprintf("%s=%s", k, v))
	}

	return os.WriteFile(envPath, []byte(strings.Join(lines, "\n")+"\n"), 0600)
}
