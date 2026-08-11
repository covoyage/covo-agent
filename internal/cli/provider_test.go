package cli

import (
	"testing"
)

func TestDefaultModel(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"openai", "gpt-5.6"},
		{"anthropic", "claude-sonnet-4-20250514"},
		{"gemini", "gemini-2.5-flash"},
		{"xiaomi", "mimo-v2.5-pro"},
		{"mimo", "mimo-v2.5-pro"},
		{"xiaomi-mimo", "mimo-v2.5-pro"},
		{"openrouter", "openai/gpt-5.6"},
		{"custom", "gpt-5.6"},
		{"unknown", "gpt-5.6"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := DefaultModel(tt.provider)
			if got != tt.want {
				t.Errorf("DefaultModel(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestProviderName(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"openai", "openai"},
		{"anthropic", "anthropic"},
		{"gemini", "gemini"},
		{"xiaomi", "xiaomi"},
		{"mimo", "xiaomi"},
		{"xiaomi-mimo", "xiaomi"},
		{"openrouter", "openrouter"},
		{"custom", "custom"},
		{"unknown", "openai"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := ProviderName(tt.provider)
			if got != tt.want {
				t.Errorf("ProviderName(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestValidateProvider(t *testing.T) {
	valid := []string{"openai", "anthropic", "gemini", "xiaomi", "mimo", "xiaomi-mimo", "openrouter", "custom"}
	for _, p := range valid {
		t.Run("valid_"+p, func(t *testing.T) {
			if err := ValidateProvider(p); err != nil {
				t.Errorf("ValidateProvider(%q) = %v, want nil", p, err)
			}
		})
	}

	t.Run("invalid", func(t *testing.T) {
		if err := ValidateProvider("invalid-provider"); err == nil {
			t.Error("ValidateProvider('invalid-provider') = nil, want error")
		}
	})
}

func TestProviderDisplayName(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"openai", "OpenAI"},
		{"anthropic", "Anthropic"},
		{"gemini", "Google Gemini"},
		{"xiaomi", "Xiaomi MiMo"},
		{"mimo", "Xiaomi MiMo"},
		{"xiaomi-mimo", "Xiaomi MiMo"},
		{"openrouter", "OpenRouter"},
		{"custom", "Custom"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := ProviderDisplayName(tt.provider)
			if got != tt.want {
				t.Errorf("ProviderDisplayName(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestProviderAPIKeyEnv(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"openai", "OPENAI_API_KEY"},
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"gemini", "GEMINI_API_KEY"},
		{"xiaomi", "XIAOMI_API_KEY"},
		{"mimo", "XIAOMI_API_KEY"},
		{"xiaomi-mimo", "XIAOMI_API_KEY"},
		{"openrouter", "OPENROUTER_API_KEY"},
		{"custom", "CUSTOM_API_KEY"},
		{"unknown", "OPENAI_API_KEY"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := ProviderAPIKeyEnv(tt.provider)
			if got != tt.want {
				t.Errorf("ProviderAPIKeyEnv(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestFindCommand(t *testing.T) {
	t.Run("exists", func(t *testing.T) {
		cmd := FindCommand("help")
		if cmd == nil {
			t.Fatal("FindCommand('help') = nil, want non-nil")
		}
		if cmd.Name != "help" {
			t.Errorf("cmd.Name = %q, want %q", cmd.Name, "help")
		}
	})

	t.Run("by alias", func(t *testing.T) {
		cmd := FindCommand("?")
		if cmd == nil {
			t.Fatal("FindCommand('?') = nil, want non-nil")
		}
		if cmd.Name != "help" {
			t.Errorf("cmd.Name = %q, want %q", cmd.Name, "help")
		}
	})

	t.Run("not found", func(t *testing.T) {
		if cmd := FindCommand("nonexistent"); cmd != nil {
			t.Errorf("FindCommand('nonexistent') = %v, want nil", cmd)
		}
	})
}

func TestCommandsByCategory(t *testing.T) {
	cat := CommandsByCategory()

	if len(cat) == 0 {
		t.Fatal("CommandsByCategory() returned empty map")
	}

	expectedCategories := []CommandCategory{CatSession, CatConfiguration, CatSkills, CatMemory, CatSystem}
	for _, c := range expectedCategories {
		if _, ok := cat[c]; !ok {
			t.Errorf("missing category %q", c)
		}
	}

	total := 0
	for _, cmds := range cat {
		total += len(cmds)
	}
	if total != len(CommandRegistry) {
		t.Errorf("total commands = %d, want %d", total, len(CommandRegistry))
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig() returned nil")
	}
	if cfg.Provider != "openai" {
		t.Errorf("cfg.Provider = %q, want %q", cfg.Provider, "openai")
	}
	if cfg.Model != "gpt-5.6" {
		t.Errorf("cfg.Model = %q, want %q", cfg.Model, "gpt-5.6")
	}
	if cfg.Mode != "general" {
		t.Errorf("cfg.Mode = %q, want %q", cfg.Mode, "general")
	}
	if cfg.Skills == nil {
		t.Fatal("cfg.Skills is nil")
	}
	if cfg.Curator == nil {
		t.Fatal("cfg.Curator is nil")
	}
	if !cfg.Curator.Enabled {
		t.Error("cfg.Curator.Enabled = false, want true")
	}
}
