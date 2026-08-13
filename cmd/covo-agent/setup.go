package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/i18n"
)

func runFirstTimeSetup(cfg *cli.Config, homeDir string) {
	fmt.Println()
	fmt.Println()
	fmt.Println("  ┌─────────────────────────────────────────────┐")
	fmt.Println("  │     Welcome to covo-agent!                  │")
	fmt.Println("  │     Let's get you set up.                   │")
	fmt.Println("  └─────────────────────────────────────────────┘")
	fmt.Println()

	detected := detectConfiguredProviders()
	reader := bufio.NewReader(os.Stdin)

	// ── Provider ──────────────────────────────────────────────
	var provider string
	if len(detected) > 0 {
		fmt.Println(i18n.T("setup.detected_providers"))
		for _, d := range detected {
			fmt.Printf("    • %s (%s)\n", cli.ProviderDisplayName(d), cli.ProviderAPIKeyEnv(d))
		}
		fmt.Println(i18n.T("setup.use_or_new"))
		allOptions := append(detected, providerChoices()...)
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
		provider = selectOne("Provider", deduped, provider)
	} else {
		provider = selectOne("Provider", providerChoices(), "openai")
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
	modeChoice := selectOne("Default mode", []string{"general", "code"}, "general")
	cfg.Mode = modeChoice

	// ── Advanced options ──────────────────────────────────────
	fmt.Println()
	fmt.Println(i18n.T("setup.advanced"))
	fmt.Println()

	if askYesNo(reader, "  "+i18n.T("setup.enable_computer"), false) {
		enabled := true
		cfg.ComputerUse = &enabled
	}

	contextEngines := []string{"enhanced", "compressor", "truncate"}
	ctxEngine := selectOne("Context engine", contextEngines, "enhanced")
	if ctxEngine != "enhanced" {
		if cfg.Context == nil {
			cfg.Context = &cli.ContextConfig{}
		}
		cfg.Context.Engine = ctxEngine
	}

	if askYesNo(reader, "  "+i18n.T("setup.enable_curator"), true) {
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

func detectConfiguredProviders() []string {
	var detected []string
	for _, pt := range cli.RegisteredProviderTypes() {
		apiKeyEnv := cli.ProviderAPIKeyEnv(pt)
		if apiKeyEnv != "" && os.Getenv(apiKeyEnv) != "" {
			detected = append(detected, pt)
		}
	}
	return detected
}

func askYesNo(reader *bufio.Reader, prompt string, defaultYes bool) bool {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	fmt.Printf("  %s %s: ", prompt, suffix)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		return defaultYes
	}
	return input == "y" || input == "yes"
}

// selectOneOrCancel shows a raw-mode arrow-key list selector.
// Returns the selected option, or "" if the user pressed Esc to cancel.
func selectOneOrCancel(label string, options []string, defaultVal string) string {
	if !cli.IsTerminal(os.Stdin.Fd()) {
		return promptChoice(bufio.NewReader(os.Stdin), label, options, defaultVal)
	}

	selected := 0
	for i, opt := range options {
		if opt == defaultVal {
			selected = i
			break
		}
	}

	fd := int(os.Stdin.Fd())
	oldState, err := cli.MakeRaw(fd)
	if err != nil {
		return promptChoice(bufio.NewReader(os.Stdin), label, options, defaultVal)
	}
	defer cli.RestoreTerminal(fd, oldState)

	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")

	renderSelect(label, options, selected)

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			continue
		}
		if n == 1 {
			switch buf[0] {
			case 13:
				clearSelect(len(options))
				fmt.Printf("\r  %s: %s\r\n", label, options[selected])
				return options[selected]
			case 3:
				clearSelect(len(options))
				fmt.Print("\033[?25h")
				os.Exit(1)
			case 27:
				clearSelect(len(options))
				fmt.Print("\r  Cancelled.\r\n")
				return ""
			case 'q':
				clearSelect(len(options))
				fmt.Printf("\r  %s: %s\r\n", label, options[selected])
				return options[selected]
			}
		} else if n == 3 && buf[0] == 27 && buf[1] == '[' {
			switch buf[2] {
			case 'A':
				if selected > 0 {
					selected--
				} else {
					selected = len(options) - 1
				}
				renderSelect(label, options, selected)
			case 'B':
				if selected < len(options)-1 {
					selected++
				} else {
					selected = 0
				}
				renderSelect(label, options, selected)
			}
		}
	}
}

func selectOne(label string, options []string, defaultVal string) string {
	if !cli.IsTerminal(os.Stdin.Fd()) {
		return promptChoice(bufio.NewReader(os.Stdin), label, options, defaultVal)
	}

	selected := 0
	for i, opt := range options {
		if opt == defaultVal {
			selected = i
			break
		}
	}

	fd := int(os.Stdin.Fd())
	oldState, err := cli.MakeRaw(fd)
	if err != nil {
		return promptChoice(bufio.NewReader(os.Stdin), label, options, defaultVal)
	}
	defer cli.RestoreTerminal(fd, oldState)

	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")

	renderSelect(label, options, selected)

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			continue
		}
		if n == 1 {
			switch buf[0] {
			case 13:
				clearSelect(len(options))
				fmt.Printf("\r  %s: %s\r\n", label, options[selected])
				return options[selected]
			case 3:
				clearSelect(len(options))
				fmt.Print("\033[?25h")
				os.Exit(1)
			case 27:
				clearSelect(len(options))
				fmt.Print("\033[?25h")
				fmt.Print("\r  Cancelled.\r\n")
				os.Exit(0)
			case 'q':
				clearSelect(len(options))
				fmt.Printf("\r  %s: %s\r\n", label, options[selected])
				return options[selected]
			}
		} else if n == 3 && buf[0] == 27 && buf[1] == '[' {
			switch buf[2] {
			case 'A':
				if selected > 0 {
					selected--
				} else {
					selected = len(options) - 1
				}
				renderSelect(label, options, selected)
			case 'B':
				if selected < len(options)-1 {
					selected++
				} else {
					selected = 0
				}
				renderSelect(label, options, selected)
			}
		}
	}
}

func renderSelect(label string, options []string, selected int) {
	clearSelect(len(options))
	fmt.Printf("\r  %s (↑↓ navigate, Enter confirm, Esc exit):\r\n", label)
	for i, opt := range options {
		if i == selected {
			fmt.Printf("\r  \033[7m  ▶ %s  \033[0m\r\n", opt)
		} else {
			fmt.Printf("\r     %s\r\n", opt)
		}
	}
}

// renderProviderList renders the provider list with category headers, status,
// search bar, and pagination. Pass pageSize=0 to show all items.
func renderProviderList(label string, options []string, selected, offset, pageSize int, search string, searching bool, defaultVal string) {
	// Build all output lines first so we can count them accurately.
	var lines []string

	// Search bar
	if searching {
		lines = append(lines, fmt.Sprintf("\033[33mSearch:\033[0m %s  backspace edit  enter confirm  esc cancel", overlayEndCursor(search)))
	} else if search != "" {
		lines = append(lines, fmt.Sprintf("\033[33mFilter:\033[0m %s  / refine  esc clear  PgUp/PgDn", search))
	} else {
		lines = append(lines, "↑↓ move  Enter pick  / filter  PgUp/PgDn  Esc exit")
	}

	// Determine visible range
	visible := options
	page := 0
	totalPages := 1
	if pageSize > 0 && len(options) > pageSize {
		page = offset / pageSize
		totalPages = (len(options)-1)/pageSize + 1
		end := offset + pageSize
		if end > len(options) {
			end = len(options)
		}
		visible = options[offset:end]
		lines = append(lines, fmt.Sprintf("%s (%d total, page %d/%d)", label, len(options), page+1, totalPages))
	} else {
		lines = append(lines, fmt.Sprintf("%s (%d total)", label, len(options)))
	}

	for i, opt := range visible {
		realIdx := i + offset
		radio := "\033[90m(○)\033[0m"
		if cli.HasProviderConfiguredFor(cli.ProviderName(opt)) {
			radio = "\033[32m(●)\033[0m"
		}
		providerDisplay := cli.ProviderDisplayName(opt)
		if providerDisplay == "" {
			providerDisplay = opt
		}
		if !cli.HasProviderConfiguredFor(cli.ProviderName(opt)) {
			providerDisplay = fmt.Sprintf("\033[90m%s\033[0m", providerDisplay)
		}
		if opt == defaultVal && defaultVal != "" {
			providerDisplay += "  \033[90m← current\033[0m"
		}

		prefix := "    "
		if realIdx == selected {
			prefix = "\033[1;32m→ "
			radio = "\033[32m(●)\033[0m"
			lines = append(lines, fmt.Sprintf("%s%s %s\033[0m", prefix, radio, providerDisplay))
		} else {
			lines = append(lines, fmt.Sprintf("%s%s %s", prefix, radio, providerDisplay))
		}
	}

	// Pad to keep constant height
	for len(lines) < pageSize+3 {
		lines = append(lines, "")
	}

	// Render from the top of the window so the interface stays anchored and
	// does not drift upward while scrolling.
	fmt.Print("\033[2J\033[H")
	for _, l := range lines {
		fmt.Printf("\r  %s\r\n", l)
	}
}

// clearScreenHome clears the whole terminal and moves the cursor to the
// top-left corner, anchoring an interactive picker at the top of the window.
func clearScreenHome() {
	fmt.Print("\033[2J\033[H")
}

func clearSelect(count int) {
	for i := 0; i <= count; i++ {
		fmt.Print("\r\033[A\033[2K")
	}
}

// overlayEndCursor renders s with the last character shown as a block cursor
// (reverse video), so the cursor does not consume an extra character cell.
// An empty string renders as a single block.
func overlayEndCursor(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return "▎"
	}
	return string(r[:len(r)-1]) + "\033[7m" + string(r[len(r)-1]) + "\033[0m"
}

func promptChoice(reader *bufio.Reader, label string, options []string, defaultVal string) string {
	for i, opt := range options {
		marker := ""
		if opt == defaultVal {
			marker = " (default)"
		}
		fmt.Printf("    %d. %s%s\n", i+1, opt, marker)
	}
	fmt.Printf("  %s [%s]: ", label, defaultVal)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultVal
	}
	for _, opt := range options {
		if strings.EqualFold(input, opt) {
			return opt
		}
	}
	if len(input) == 1 && input[0] >= '1' && input[0] <= '9' {
		i := int(input[0] - '1')
		if i < len(options) {
			return options[i]
		}
	}
	return defaultVal
}
