package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SkinConfig defines the YAML skin file format at ~/.covo-agent/skin.yaml.
type SkinConfig struct {
	// Theme name (dark, light, or custom).
	Theme string `yaml:"theme,omitempty"`
	// Custom colors override the default theme.
	Colors SkinColors `yaml:"colors,omitempty"`
	// Tool emoji mappings.
	ToolEmoji map[string]string `yaml:"tool_emoji,omitempty"`
	// UI symbols.
	Symbols SkinSymbols `yaml:"symbols,omitempty"`
}

type SkinColors struct {
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

type SkinSymbols struct {
	Check    string `yaml:"check,omitempty"`
	Cross    string `yaml:"cross,omitempty"`
	Arrow    string `yaml:"arrow,omitempty"`
	Bullet   string `yaml:"bullet,omitempty"`
	Warning  string `yaml:"warning,omitempty"`
	Info     string `yaml:"info,omitempty"`
	Thinking string `yaml:"thinking,omitempty"`
	Star     string `yaml:"star,omitempty"`
}

func LoadSkinConfig(homeDir string) (*SkinConfig, error) {
	path := filepath.Join(homeDir, "skin.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read skin: %w", err)
	}
	var cfg SkinConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse skin: %w", err)
	}
	return &cfg, nil
}
