package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/i18n"
)

// cmdSetup runs the interactive configuration wizard.
// It is idempotent — safe to run multiple times to update settings.

// providerItem represents a selectable provider with config status.
type providerItem struct {
	name       string
	configured bool
}

func installShellCompletion() {
	shell := os.Getenv("SHELL")
	if shell == "" {
		fmt.Println("  Could not detect shell from $SHELL. To install manually:")
		fmt.Println("    source <(covo-agent completion zsh)   # zsh")
		fmt.Println("    source <(covo-agent completion bash)  # bash")
		return
	}

	shellName := filepath.Base(shell)
	switch shellName {
	case "zsh":
		fmt.Println("  Detected shell: zsh")
		rcFile := filepath.Join(os.Getenv("HOME"), ".zshrc")
		hook := `source <(covo-agent completion zsh)`
		if tryAppendHook(rcFile, hook) {
			fmt.Printf("  ✓ Added completion to %s\n", rcFile)
		} else {
			fmt.Printf("  Add this to %s:\n    %s\n", rcFile, hook)
		}

	case "bash":
		fmt.Println("  Detected shell: bash")
		rcFile := filepath.Join(os.Getenv("HOME"), ".bashrc")
		hook := `source <(covo-agent completion bash)`
		if tryAppendHook(rcFile, hook) {
			fmt.Printf("  ✓ Added completion to %s\n", rcFile)
		} else {
			fmt.Printf("  Add this to %s:\n    %s\n", rcFile, hook)
		}

	case "fish":
		fmt.Println("  Detected shell: fish")
		fmt.Println("  Run: covo-agent completion fish | source")

	default:
		fmt.Printf("  Detected shell: %s\n", shellName)
		fmt.Println("  To install manually:")
		fmt.Println("    source <(covo-agent completion zsh)   # zsh")
		fmt.Println("    source <(covo-agent completion bash)  # bash")
	}
}

func tryAppendHook(rcFile, hook string) bool {
	data, err := os.ReadFile(rcFile)
	if err == nil && strings.Contains(string(data), hook) {
		return true // already installed
	}
	f, err := os.OpenFile(rcFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return false
	}
	defer f.Close()
	fmt.Fprintf(f, "\n# covo-agent shell completion\n%s\n", hook)
	return true
}

func cmdSetup() {
	fmt.Println(i18n.T("setup.welcome"))
	fmt.Println(strings.Repeat("=", len(i18n.T("setup.welcome"))))
	fmt.Println()

	cfg, err := cli.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		cfg = cli.DefaultConfig()
	}

	homeDir, err := cli.EnsureHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "home dir: %v\n", err)
		return
	}

	if err := cli.LoadDotEnv(); err != nil {
		// Non-fatal — .env may not exist yet.
		_ = err
	}

	// Step 1: Provider selection
	fmt.Println("Step 1: Select AI Provider")
	fmt.Println("───────────────────────────")
	fmt.Println()
	provider := promptProvider(cfg.Provider)
	fmt.Printf("  ✓ Provider: %s\n", cli.ProviderDisplayName(provider))
	fmt.Println()

	// Step 2: API Key (masked input)
	fmt.Println("Step 2: API Key")
	fmt.Println("───────────────")
	fmt.Println()
	if err := promptAPIKey(provider); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: %v\n", err)
	}
	fmt.Println()

	// Step 3: Model selection
	fmt.Println("Step 3: Default Model")
	fmt.Println("─────────────────────")
	fmt.Println()
	model := promptModel(provider, cfg.Model)
	fmt.Printf("  ✓ Model: %s\n", model)
	fmt.Println()

	// Step 4: Mode selection
	fmt.Println("Step 4: Agent Mode")
	fmt.Println("───────────────────")
	fmt.Println()
	mode := promptMode(cfg.Mode)
	fmt.Printf("  ✓ Mode: %s\n", mode)
	fmt.Println()

	// Update config
	cfg.Provider = provider
	cfg.Model = model
	cfg.Mode = mode

	if err := cli.SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "save config: %v\n", err)
		return
	}

	fmt.Println()
	fmt.Println(i18n.T("setup.saved_to", "path", homeDir))
	fmt.Println()

	// Step 5: Shell completion
	fmt.Println("Step 5: Shell Completion")
	fmt.Println("───────────────────────")
	fmt.Println()
	installShellCompletion()
	fmt.Println()

	fmt.Println(i18n.T("setup.next_title"))
	fmt.Println(i18n.T("setup.next_1"))
	fmt.Println(i18n.T("setup.next_2"))
	fmt.Println(i18n.T("setup.next_3"))
	fmt.Println()
}

// promptProvider shows an interactive provider selector.
func promptProvider(currentProvider string) string {
	providers := providerChoices()

	if !cli.IsTerminal(os.Stdin.Fd()) {
		return selectOne("Select AI provider", providers, currentProvider)
	}

	// Show which providers already have API keys configured
	detected := detectConfiguredProviders()

	selected := 0
	for i, opt := range providers {
		if opt == currentProvider {
			selected = i
			break
		}
	}

	fd := int(os.Stdin.Fd())
	oldState, err := cli.MakeRaw(fd)
	if err != nil {
		return selectOne("Provider", providers, currentProvider)
	}
	defer cli.RestoreTerminal(fd, oldState)

	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")

	configuredSet := make(map[string]bool)
	for _, d := range detected {
		configuredSet[d] = true
	}

	items := make([]providerItem, len(providers))
	for i, p := range providers {
		items[i] = providerItem{name: p, configured: configuredSet[p]}
	}

	renderProviderSelect(items, selected)

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			continue
		}
		if n == 1 {
			switch buf[0] {
			case 13, 10: // Enter
				clearSelect(len(items) + 1)
				fmt.Printf("\r  Provider: %s (%s)\r\n", items[selected].name, cli.ProviderDisplayName(items[selected].name))
				return items[selected].name
			case 3: // Ctrl+C
				clearSelect(len(items) + 1)
				fmt.Print("\033[?25h")
				os.Exit(1)
			case 27: // Esc
				clearSelect(len(items) + 1)
				fmt.Print("\033[?25h")
				fmt.Print("\r  Cancelled.\r\n")
				os.Exit(0)
			case 'q':
				clearSelect(len(items) + 1)
				fmt.Printf("\r  Provider: %s (%s)\r\n", items[selected].name, cli.ProviderDisplayName(items[selected].name))
				return items[selected].name
			}
		} else if n == 3 && buf[0] == 27 && buf[1] == '[' {
			switch buf[2] {
			case 'A': // Up
				if selected > 0 {
					selected--
				} else {
					selected = len(items) - 1
				}
				renderProviderSelect(items, selected)
			case 'B': // Down
				if selected < len(items)-1 {
					selected++
				} else {
					selected = 0
				}
				renderProviderSelect(items, selected)
			}
		}
	}
}

func renderProviderSelect(items []providerItem, selected int) {
	clearSelect(len(items) + 1)
	fmt.Print("\r  Select AI provider (↑↓ navigate, Enter confirm):\r\n")
	for i, item := range items {
		marker := ""
		if item.configured {
			marker = " (API key configured)"
		}
		displayName := cli.ProviderDisplayName(item.name)
		if i == selected {
			fmt.Printf("\r  \033[7m  ▶ %s — %s%s  \033[0m\r\n", item.name, displayName, marker)
		} else {
			fmt.Printf("\r     %s — %s%s\r\n", item.name, displayName, marker)
		}
	}
}

// promptAPIKey reads an API key with masked input.
func promptAPIKey(provider string) error {
	apiKeyEnv := cli.ProviderAPIKeyEnv(provider)
	existingKey := os.Getenv(apiKeyEnv)

	if existingKey != "" && provider != "custom" {
		masked := existingKey
		if len(masked) > 8 {
			masked = masked[:4] + strings.Repeat("*", len(masked)-8) + masked[len(masked)-4:]
		}
		fmt.Printf("  %s: %s (already set)\n", apiKeyEnv, masked)
		fmt.Print("  Press Enter to keep, or type a new key: ")

		input, err := cli.PromptSecret("")
		if err != nil {
			return err
		}
		if input != "" {
			os.Setenv(apiKeyEnv, input)
			if err := cli.SaveEnvValue(apiKeyEnv, input); err != nil {
				return err
			}
			fmt.Println("  ✓ API key updated")
		} else {
			fmt.Println("  (kept existing)")
		}
	} else {
		fmt.Printf("  %s: ", apiKeyEnv)
		input, err := cli.PromptSecret("")
		if err != nil {
			return err
		}
		input = strings.TrimSpace(input)
		if input != "" {
			os.Setenv(apiKeyEnv, input)
			if err := cli.SaveEnvValue(apiKeyEnv, input); err != nil {
				return err
			}
			fmt.Println("  ✓ API key saved")
		} else {
			fmt.Println("  (skipped — no key provided)")
		}
	}

	// Handle custom provider base URL
	if cli.ProviderNeedsBaseURL(provider) {
		baseURLEnv := cli.ProviderBaseURLEnv(provider)
		existingURL := os.Getenv(baseURLEnv)
		if existingURL != "" {
			fmt.Printf("  %s: %s (already set)\n", baseURLEnv, existingURL)
			fmt.Print("  Press Enter to keep, or type a new URL: ")
			var input string
			fmt.Scanln(&input)
			input = strings.TrimSpace(input)
			if input != "" {
				os.Setenv(baseURLEnv, input)
				_ = cli.SaveEnvValue(baseURLEnv, input)
			}
		} else {
			fmt.Printf("  %s: ", baseURLEnv)
			var input string
			fmt.Scanln(&input)
			input = strings.TrimSpace(input)
			if input != "" {
				os.Setenv(baseURLEnv, input)
				_ = cli.SaveEnvValue(baseURLEnv, input)
			}
		}
	}

	return nil
}

// promptModel lets the user pick a model for the given provider.
func promptModel(provider, currentModel string) string {
	defaultModel := cli.DefaultModel(provider)
	prompt := currentModel
	if prompt == "" {
		prompt = defaultModel
	}

	fmt.Printf("  Provider: %s\n", cli.ProviderDisplayName(provider))
	fmt.Printf("  Default model for %s: %s\n", cli.ProviderDisplayName(provider), defaultModel)

	// Try to fetch available models from the API
	models, err := cli.FetchProviderModels(provider)
	if err == nil && len(models) > 0 {
		fmt.Printf("  Available models from %s API:\n", cli.ProviderDisplayName(provider))
		shown := 0
		for _, m := range models {
			if shown >= 15 {
				fmt.Printf("    ... and %d more models\n", len(models)-15)
				break
			}
			if m.Name != "" && m.Name != m.ID {
				fmt.Printf("    %s — %s\n", m.ID, m.Name)
			} else {
				fmt.Printf("    %s\n", m.ID)
			}
			shown++
		}
	}

	fmt.Printf("  Model name [%s]: ", prompt)
	var input string
	fmt.Scanln(&input)
	input = strings.TrimSpace(input)
	if input != "" {
		return input
	}
	return prompt
}

// promptMode lets the user pick an agent mode.
func promptMode(currentMode string) string {
	modes := []string{"general", "code"}
	if currentMode == "" {
		currentMode = "general"
	}
	return selectOne("Default mode", modes, currentMode)
}

// promptChoice is a non-interactive fallback for selectOne.
// selectOne is defined in setup.go and handles non-terminal fallback internally.
