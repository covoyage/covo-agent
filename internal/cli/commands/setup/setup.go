package setup

import (
	"bufio"
	"fmt"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/cli/commands/model"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"os"
	"strings"

	"github.com/covoyage/covo-agent/internal/i18n"
)

func RunFirstTimeSetup(cfg *cli.Config, homeDir string) {
	fmt.Println()
	fmt.Println()
	fmt.Println("  ┌─────────────────────────────────────────────┐")
	fmt.Println("  │     Welcome to covo-agent!                  │")
	fmt.Println("  │     Let's get you set up.                   │")
	fmt.Println("  └─────────────────────────────────────────────┘")
	fmt.Println()

	detected := shared.DetectConfiguredProviders()
	reader := bufio.NewReader(os.Stdin)

	// ── Provider ──────────────────────────────────────────────
	var provider string
	if len(detected) > 0 {
		fmt.Println(i18n.T("setup.detected_providers"))
		for _, d := range detected {
			fmt.Printf("    • %s (%s)\n", cli.ProviderDisplayName(d), cli.ProviderAPIKeyEnv(d))
		}
		fmt.Println(i18n.T("setup.use_or_new"))
		allOptions := append(detected, model.ProviderChoices()...)
		seen := make(map[string]bool)
		var deduped []string
		for _, opt := range allOptions {
			if !seen[opt] {
				seen[opt] = true
				deduped = append(deduped, opt)
			}
		}
		if len(detected) > 0 {
			provider = detected[0]
		}
		provider = shared.SelectOne("Provider", deduped, provider)
	} else {
		provider = shared.SelectOne("Provider", model.ProviderChoices(), "openai")
	}
	cfg.Provider = provider

	// ── API Key ───────────────────────────────────────────────
	apiKeyEnv := cli.ProviderAPIKeyEnv(provider)
	existingKey := os.Getenv(apiKeyEnv)
	if existingKey != "" && provider != "custom" {
		masked := existingKey
		if len(masked) > 8 {
			masked = masked[:4] + strings.Repeat("*", len(masked)-8) + masked[len(masked)-4:]
		}
		fmt.Print(i18n.T("setup.already_set", "key", apiKeyEnv, "val", masked) + "\n")
		fmt.Print(i18n.T("setup.press_enter"))
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input != "" {
			os.Setenv(apiKeyEnv, input)
			_ = cli.SaveEnvValue(apiKeyEnv, input)
		}
	} else {
		fmt.Printf("  %s: ", apiKeyEnv)
		apiKey, _ := reader.ReadString('\n')
		apiKey = strings.TrimSpace(apiKey)
		if apiKey != "" {
			os.Setenv(apiKeyEnv, apiKey)
			_ = cli.SaveEnvValue(apiKeyEnv, apiKey)
		}
	}

	// ── Model ─────────────────────────────────────────────────
	model := cli.ResolveModel(cfg)
	if provider == "openrouter" {
		fmt.Println("  Fetching available models from OpenRouter...")
		models, err := cli.FetchOpenRouterModels()
		if err != nil {
			fmt.Printf("  (could not fetch models: %v, using default)\n", err)
		} else {
			fmt.Printf("  Available models (%d total):\n", len(models))
			shown := 0
			for _, m := range models {
				if shown >= 20 {
					fmt.Printf("    ... and %d more (type a model ID)\n", len(models)-20)
					break
				}
				fmt.Printf("    %s\n", m.ID)
				shown++
			}
		}
	}
	fmt.Printf("  Model [%s]: ", model)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input != "" {
		model = input
	}
	cfg.Model = model

	// ── Base URL (custom provider) ────────────────────────────
	if cli.ProviderNeedsBaseURL(provider) {
		existingURL := os.Getenv("CUSTOM_BASE_URL")
		if existingURL != "" {
			fmt.Printf("  CUSTOM_BASE_URL: %s (already set)\n", existingURL)
			fmt.Printf("  Press Enter to keep, or type a new URL: ")
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)
			if input != "" {
				os.Setenv("CUSTOM_BASE_URL", input)
				_ = cli.SaveEnvValue("CUSTOM_BASE_URL", input)
			}
		} else {
			fmt.Printf("  CUSTOM_BASE_URL: ")
			baseURL, _ := reader.ReadString('\n')
			baseURL = strings.TrimSpace(baseURL)
			if baseURL != "" {
				os.Setenv("CUSTOM_BASE_URL", baseURL)
				_ = cli.SaveEnvValue("CUSTOM_BASE_URL", baseURL)
			}
		}
	}

	// ── Mode ──────────────────────────────────────────────────
	modeChoice := shared.SelectOne("Default mode", []string{"general", "code"}, "general")
	cfg.Mode = modeChoice

	// ── Advanced options ──────────────────────────────────────
	fmt.Println()
	fmt.Println(i18n.T("setup.advanced"))
	fmt.Println()

	if shared.AskYesNo(reader, "  "+i18n.T("setup.enable_computer"), false) {
		enabled := true
		cfg.ComputerUse = &enabled
	}

	contextEngines := []string{"enhanced", "compressor", "truncate"}
	ctxEngine := shared.SelectOne("Context engine", contextEngines, "enhanced")
	if ctxEngine != "enhanced" {
		if cfg.Context == nil {
			cfg.Context = &cli.ContextConfig{}
		}
		cfg.Context.Engine = ctxEngine
	}

	if shared.AskYesNo(reader, "  "+i18n.T("setup.enable_curator"), true) {
		if cfg.Curator == nil {
			cfg.Curator = &cli.CuratorConfig{Enabled: true, IntervalHours: 168, StaleAfterDays: 30, ArchiveAfterDays: 90}
		}
		cfg.Curator.Enabled = true
	}

	// ── Save ──────────────────────────────────────────────────
	if err := cli.SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: could not save config: %v\n", err)
	}

	fmt.Println()
	fmt.Println(i18n.T("setup.saved_to", "path", homeDir))
	fmt.Println()
	fmt.Println(i18n.T("setup.next_title"))
	fmt.Println(i18n.T("setup.next_1"))
	fmt.Println(i18n.T("setup.next_2"))
	fmt.Println(i18n.T("setup.next_3"))
	fmt.Println("    • List skills: covo-agent skills list")
	fmt.Println()
	fmt.Println("  Starting covo-agent...")
	fmt.Println()
}
