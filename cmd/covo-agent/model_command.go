package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/i18n"
	"golang.org/x/term"
)

const customModelOption = "Enter custom model name"
const modelPickerPageSize = 20

func runModelCommand(cfg *cli.Config, homeDir string) error {
	reader := bufio.NewReader(os.Stdin)

	currentProvider := cli.ResolveProvider(cfg)
	currentModel := cli.ResolveModel(cfg)

	fmt.Print("\r\n")
	fmt.Print("\r" + i18n.TF("model.cmd_current_model", currentModel) + "\n")
	fmt.Print("\r" + i18n.TF("model.cmd_active_provider", currentProvider) + "\n")
	fmt.Print("\r\n")

	for {
		provider := selectProvider(cfg, reader, currentProvider)
		if provider == "" {
			// Esc pressed — exit.
			return nil
		}

		// "custom" triggers the interactive custom provider creation flow.
		if provider == "custom" {
			cp, selectedModel, err := addCustomProvider(reader, cfg)
			if err != nil {
				return err
			}
			if cp == nil {
				// User cancelled, loop back to provider selection.
				continue
			}
			// Re-register custom providers so the new one appears in the list immediately.
			cli.RegisterCustomProviders(cfg)

			// Directly save the selected provider and model, bypassing another loop.
			cfg.Provider = cp.TypeName()
			cfg.Model = selectedModel
			if cfg.Model == "" && len(cp.Models) > 0 {
				cfg.Model = cp.Models[0].ID
			}
			if cfg.Mode == "" {
				cfg.Mode = cli.ResolveMode(cfg)
			}
			if err := cli.SaveConfig(cfg); err != nil {
				return err
			}

			fmt.Print("\r\n")
			fmt.Printf("\r  Saved default provider/model to %s\n", homeDir)
			fmt.Printf("\r  Provider: %s\n", cp.Name)
			fmt.Printf("\r  Model:    %s\n", selectedModel)
			fmt.Print("\r\n")
			return nil
		}

		provider = cli.ProviderName(provider)
		if err := cli.ValidateProvider(provider); err != nil {
			return err
		}

		if err := promptProviderSecrets(reader, provider); err != nil {
			return err
		}

		modelDefault := currentModel
		if provider != currentProvider || modelDefault == "" {
			modelDefault = cli.DefaultModel(provider)
		}
		model, back := selectModel(reader, provider, modelDefault)
		if back {
			currentProvider = provider
			continue
		}

		cfg.Provider = provider
		cfg.Model = model
		if cfg.Mode == "" {
			cfg.Mode = cli.ResolveMode(cfg)
		}
		if err := cli.SaveConfig(cfg); err != nil {
			return err
		}

		fmt.Print("\r\n")
		fmt.Printf("\r  Saved default provider/model to %s\n", homeDir)
		fmt.Printf("\r  Provider: %s\n", provider)
		fmt.Printf("\r  Model:    %s\n", model)
		fmt.Print("\r\n")
		return nil
	}
}

// --- Provider Selection with Actions ---

// selectProvider shows a provider picker with d=delete and e=edit for custom providers.
// Returns the selected provider type name, or "" on Esc.
func selectProvider(cfg *cli.Config, reader *bufio.Reader, currentProvider string) string {
	label := i18n.T("cli.select_provider_hint")

	for {
		choices := providerChoices()
		selected, action := selectProviderInteractive(label, choices, currentProvider)
		switch action {
		case 0: // Enter / 'q'
			return selected
		case 1: // Esc
			return ""
		case 2: // 'd' — delete custom provider
			if !strings.HasPrefix(selected, "custom_") {
				continue
			}
			if confirmDelete(selected) {
				deleteCustomProvider(cfg, selected)
				cli.UnregisterProvider(selected)
				// Update currentProvider if we deleted the active one.
				if currentProvider == selected {
					currentProvider = "openai"
				}
			}
		case 3: // 'e' — edit custom provider
			if !strings.HasPrefix(selected, "custom_") {
				continue
			}
			editCustomProvider(cfg, reader, selected)
			cli.RegisterCustomProviders(cfg)
		}
	}
}

// selectProviderInteractive shows a raw-mode arrow-key list selector.
// Returns (selected option, action): 0=Enter, 1=Esc, 2=Delete, 3=Edit.
// / to search, ↑↓ navigate, pgup/pgdn page, ESC cancel.
func selectProviderInteractive(label string, options []string, defaultVal string) (string, int) {
	const pageSize = 15

	if !cli.IsTerminal(os.Stdin.Fd()) {
		choice := promptChoice(bufio.NewReader(os.Stdin), label, options, defaultVal)
		return choice, 0
	}

	filterOptions := func(search string) []string {
		if search == "" {
			return options
		}
		s := strings.ToLower(search)
		var out []string
		for _, o := range options {
			dn := strings.ToLower(cli.ProviderDisplayName(o))
			if strings.Contains(dn, s) || strings.Contains(o, s) {
				out = append(out, o)
			}
		}
		return out
	}

	search := ""
	filtered := options
	searching := false // true when / has been pressed — capture keystrokes as search
	selected := 0
	offset := 0
	for i, opt := range filtered {
		if opt == defaultVal {
			selected = i
			break
		}
	}

	fd := int(os.Stdin.Fd())
	oldState, err := cli.MakeRaw(fd)
	if err != nil {
		choice := promptChoice(bufio.NewReader(os.Stdin), label, options, defaultVal)
		return choice, 0
	}
	defer cli.RestoreTerminal(fd, oldState)

	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")

	renderProviderList(label, filtered, selected, offset, pageSize, search, searching, defaultVal)

	buf := make([]byte, 6)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			continue
		}

		// In search mode: capture printable chars
		if searching && n == 1 && buf[0] >= 32 && buf[0] < 127 {
			if buf[0] == '/' {
				continue // don't double-toggle
			}
			search += string(buf[0])
			filtered = filterOptions(search)
			selected = 0
			offset = 0
			renderProviderList(label, filtered, selected, offset, pageSize, search, searching, defaultVal)
			continue
		}

		if n == 1 {
			switch buf[0] {
			case '/':
				searching = true
				search = ""
				filtered = options
				selected = 0
				offset = 0
				renderProviderList(label, filtered, selected, offset, pageSize, search, searching, defaultVal)
				continue
			case 13: // Enter
				if searching {
					searching = false
					renderProviderList(label, filtered, selected, offset, pageSize, search, searching, defaultVal)
					continue
				}
				if len(filtered) == 0 {
					continue
				}
				clearSelect(len(filtered) + 8)
				fmt.Printf("\r  %s: %s\r\n", label, filtered[selected])
				return filtered[selected], 0
			case 127: // backspace
				if searching {
					if len(search) > 0 {
						search = search[:len(search)-1]
						filtered = filterOptions(search)
						selected = 0
						offset = 0
						renderProviderList(label, filtered, selected, offset, pageSize, search, searching, defaultVal)
					}
					continue
				}
			case 3: // Ctrl+C
				clearSelect(len(filtered) + 8)
				fmt.Print("\033[?25h")
				os.Exit(1)
			case 27: // Esc
				if searching {
					searching = false
					search = ""
					filtered = options
					selected = 0
					offset = 0
					renderProviderList(label, filtered, selected, offset, pageSize, search, searching, defaultVal)
					continue
				}
				clearSelect(len(filtered) + 8)
				fmt.Print("\r  Cancelled.\r\n")
				return "", 1
			case 'q':
				if searching {
					continue
				}
				if len(filtered) == 0 {
					continue
				}
				clearSelect(len(filtered) + 8)
				fmt.Printf("\r  %s: %s\r\n", label, filtered[selected])
				return filtered[selected], 0
			case 'd', 'D':
				if len(filtered) == 0 || searching {
					continue
				}
				return filtered[selected], 2
			case 'e', 'E':
				if len(filtered) == 0 || searching {
					continue
				}
				return filtered[selected], 3
			}
		} else if n == 3 && buf[0] == 27 && buf[1] == '[' {
			switch buf[2] {
			case 'A': // Up
				if len(filtered) == 0 {
					continue
				}
				if selected > 0 {
					selected--
				} else {
					selected = len(filtered) - 1
				}
				if selected < offset {
					offset = selected
				}
				if selected >= offset+pageSize {
					offset = selected - pageSize + 1
				}
				renderProviderList(label, filtered, selected, offset, pageSize, search, searching, defaultVal)
			case 'B': // Down
				if len(filtered) == 0 {
					continue
				}
				if selected < len(filtered)-1 {
					selected++
				} else {
					selected = 0
				}
				if selected < offset {
					offset = selected
				}
				if selected >= offset+pageSize {
					offset = selected - pageSize + 1
				}
				renderProviderList(label, filtered, selected, offset, pageSize, search, searching, defaultVal)
			}
		} else if n == 4 && buf[0] == 27 && buf[1] == '[' && buf[2] == '5' && buf[3] == '~' {
			// PgUp
			offset -= pageSize
			if offset < 0 {
				offset = 0
			}
			selected = offset
			renderProviderList(label, filtered, selected, offset, pageSize, search, searching, defaultVal)
		} else if n == 4 && buf[0] == 27 && buf[1] == '[' && buf[2] == '6' && buf[3] == '~' {
			// PgDn
			offset += pageSize
			maxOff := (len(filtered) - 1) / pageSize * pageSize
			if offset >= len(filtered) || offset > maxOff {
				offset = maxOff
			}
			if offset < 0 {
				offset = 0
			}
			selected = offset
			renderProviderList(label, filtered, selected, offset, pageSize, search, searching, defaultVal)
		}
	}
}

// Confirm panel for provider deletion
func confirmDelete(providerType string) bool {
	if !cli.IsTerminal(os.Stdin.Fd()) {
		return false
	}
	fd := int(os.Stdin.Fd())
	oldState, err := cli.MakeRaw(fd)
	if err != nil {
		return false
	}
	defer cli.RestoreTerminal(fd, oldState)

	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")

	fmt.Printf("\r  Delete provider %q? (y/N): ", providerType)

	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			continue
		}
		switch buf[0] {
		case 'y', 'Y':
			fmt.Print("y\r\n")
			return true
		case 'n', 'N', 13, 10, 27:
			if buf[0] == 27 {
				fmt.Print("Cancelled.\r\n")
			} else {
				fmt.Print("\r\n")
			}
			return false
		}
	}
}

func deleteCustomProvider(cfg *cli.Config, providerType string) {
	var remaining []cli.CustomProvider
	for _, cp := range cfg.CustomProviders {
		if cp.TypeName() != providerType {
			remaining = append(remaining, cp)
		}
	}
	cfg.CustomProviders = remaining
	cli.SaveConfig(cfg)
	fmt.Printf("\r  Deleted provider %q.\r\n", providerType)
}

func editCustomProvider(cfg *cli.Config, reader *bufio.Reader, providerType string) {
	// Find the existing provider.
	var target *cli.CustomProvider
	var idx int
	for i := range cfg.CustomProviders {
		if cfg.CustomProviders[i].TypeName() == providerType {
			target = &cfg.CustomProviders[i]
			idx = i
			break
		}
	}
	if target == nil {
		return
	}

	fmt.Print("\r\n")
	fmt.Printf("\r  ── Edit Provider: %s ──\n", target.Name)
	fmt.Print("\r\n")

	// 1. Provider name (pre-filled)
	name, cancelled := promptLineWithDefault("Provider name", target.Name)
	if cancelled {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = target.Name
	}

	// 2. Protocol type (pre-selected)
	protocol := selectOneOrCancel("Protocol type", protocolOptions, target.Protocol)
	if protocol == "" {
		return
	}

	// 3. Base URL (pre-filled)
	baseURL, cancelled := promptLineWithDefault("Base URL", target.BaseURL)
	if cancelled {
		return
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = target.BaseURL
	}

	// 4. API Key (optional, keep existing if blank)
	apiKeyEnv := target.APIKeyEnv
	if name != target.Name {
		apiKeyEnv = apiKeyEnvForProvider(name)
	}
	label := fmt.Sprintf("API Key (%s)", apiKeyEnv)
	apiKey, cancelled := promptLine(label)
	if cancelled {
		return
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey != "" {
		os.Setenv(apiKeyEnv, apiKey)
		cli.SaveEnvValue(apiKeyEnv, apiKey)
	}

	// Update the provider.
	oldTypeName := target.TypeName()
	cfg.CustomProviders[idx] = cli.CustomProvider{
		Name:      name,
		Protocol:  protocol,
		BaseURL:   baseURL,
		APIKeyEnv: apiKeyEnv,
		Models:    target.Models,
	}
	cli.SaveConfig(cfg)
	// If the name changed, unregister the old type.
	if name != target.Name {
		cli.UnregisterProvider(oldTypeName)
	}
	fmt.Print("\r\n")
	fmt.Printf("\r  Provider %q updated.\r\n", name)
}

// promptLineWithDefault is like promptLine but pre-fills the field with a
// default value (kept when the user confirms without editing).
func promptLineWithDefault(label, defaultVal string) (string, bool) {
	if !cli.IsTerminal(os.Stdin.Fd()) {
		fmt.Printf("  %s [%s]: ", label, defaultVal)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return defaultVal, false
		}
		return line, false
	}

	fd := int(os.Stdin.Fd())
	oldState, err := cli.MakeRaw(fd)
	if err != nil {
		fmt.Printf("  %s [%s]: ", label, defaultVal)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return defaultVal, false
		}
		return line, false
	}
	defer cli.RestoreTerminal(fd, oldState)

	return promptLineRaw(fd, oldState, label, []rune(defaultVal))
}

// promptLineRaw reads a single line in raw mode with caret editing support:
// ←/→ move the caret, Home/End (and Ctrl+A/E) jump to the line edges, Ctrl+B/F
// move by one, characters are inserted at the caret, and backspace deletes the
// character before it. Returns (line, true) on Esc, (line, false) on Enter.
func promptLineRaw(fd int, oldState *term.State, label string, initial []rune) (string, bool) {
	line := append([]rune(nil), initial...)
	caret := len(line)

	drawPromptLine(label, line, caret)

	buf := make([]byte, 256)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			continue
		}
		for i := 0; i < n; i++ {
			switch buf[i] {
			case 13, 10: // Enter
				fmt.Print("\r\n")
				return string(line), false
			case 27: // Esc or escape sequence
				if i+1 >= n {
					fmt.Print("\r  Cancelled.\r\n")
					return "", true
				}
				key, consumed := decodeEscapeSeq(buf, i+1, n)
				if consumed == 0 {
					fmt.Print("\r  Cancelled.\r\n")
					return "", true
				}
				i += consumed
				switch key {
				case "left":
					if caret > 0 {
						caret--
						fmt.Print("\033[1D")
					}
				case "right":
					if caret < len(line) {
						caret++
						fmt.Print("\033[1C")
					}
				case "home":
					if caret > 0 {
						fmt.Printf("\033[%dD", caret)
						caret = 0
					}
				case "end":
					if dist := len(line) - caret; dist > 0 {
						fmt.Printf("\033[%dC", dist)
						caret = len(line)
					}
				default:
					fmt.Print("\r  Cancelled.\r\n")
					return "", true
				}
			case 3: // Ctrl-C
				cli.RestoreTerminal(fd, oldState)
				fmt.Print("\033[?25h")
				fmt.Print("\r\n")
				os.Exit(1)
			case 127, 8: // Backspace
				if caret > 0 {
					line = append(line[:caret-1], line[caret:]...)
					caret--
					drawPromptLine(label, line, caret)
				}
			case 1: // Ctrl+A → start of line
				if caret > 0 {
					fmt.Printf("\033[%dD", caret)
					caret = 0
				}
			case 5: // Ctrl+E → end of line
				if dist := len(line) - caret; dist > 0 {
					fmt.Printf("\033[%dC", dist)
					caret = len(line)
				}
			case 2: // Ctrl+B → left
				if caret > 0 {
					caret--
					fmt.Print("\033[1D")
				}
			case 6: // Ctrl+F → right
				if caret < len(line) {
					caret++
					fmt.Print("\033[1C")
				}
			default:
				if buf[i] >= 32 {
					line = append(line, 0)
					copy(line[caret+1:], line[caret:])
					line[caret] = rune(buf[i])
					caret++
					drawPromptLine(label, line, caret)
				}
			}
		}
	}
}

// decodeEscapeSeq decodes a CSI escape sequence whose bytes start at buf[start]
// (immediately after the leading ESC). Returns the key ID and the number of
// bytes consumed (excluding the ESC). Returns ("", 0) if the sequence is
// incomplete, and ("", n) for an unrecognized sequence.
func decodeEscapeSeq(buf []byte, start, n int) (string, int) {
	if start >= n || buf[start] != '[' {
		if start < n {
			return "", 1
		}
		return "", 0
	}
	end := start + 1
	for end < n {
		c := buf[end]
		if c >= 0x40 && c <= 0x7e {
			break
		}
		end++
	}
	if end >= n {
		return "", 0
	}
	params := string(buf[start+1 : end])
	consumed := end - start + 1
	switch buf[end] {
	case 'A':
		if params == "" {
			return "up", consumed
		}
	case 'B':
		if params == "" {
			return "down", consumed
		}
	case 'C':
		if params == "" {
			return "right", consumed
		}
	case 'D':
		if params == "" {
			return "left", consumed
		}
	case 'H':
		if params == "" {
			return "home", consumed
		}
	case 'F':
		if params == "" {
			return "end", consumed
		}
	case '~':
		switch params {
		case "1", "7":
			return "home", consumed
		case "4", "8":
			return "end", consumed
		}
	}
	return "", consumed
}

// drawPromptLine redraws the current input line and positions the terminal
// cursor at the caret so the caret is always visible in the right place.
func drawPromptLine(label string, line []rune, caret int) {
	fmt.Printf("\r  %s: %s", label, string(line))
	fmt.Print("\033[K")
	if dist := len(line) - caret; dist > 0 {
		fmt.Printf("\033[%dD", dist)
	}
}

// --- Add Custom Provider Interactive Flow ---

var protocolOptions = []string{
	"openai/chat",
	"openai/responses",
	"anthropic",
	"gemini",
}

// addCustomProvider runs an interactive flow to create a new custom provider.
// Returns (provider, selectedModel, error).
// Returns nil, "", nil if the user cancels at any step.
func addCustomProvider(_ *bufio.Reader, cfg *cli.Config) (*cli.CustomProvider, string, error) {
	fmt.Print("\r\n")
	fmt.Print("\r  ── Add Custom Provider ──\n")
	fmt.Print("\r\n")

	// 1. Provider name
	name, cancelled := promptLine("Provider name")
	if cancelled {
		return nil, "", nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		fmt.Print("\r  Provider name is required.\n")
		return nil, "", nil
	}

	// 2. Protocol type
	protocol := selectOneOrCancel("Protocol type", protocolOptions, "")
	if protocol == "" {
		return nil, "", nil
	}

	// 3. Base URL (no default, must be provided)
	var baseURL string
	for {
		var canc bool
		baseURL, canc = promptLine("Base URL")
		if canc {
			return nil, "", nil
		}
		baseURL = strings.TrimSpace(baseURL)
		if baseURL != "" {
			break
		}
		fmt.Print("\r  Base URL is required. Please enter a valid endpoint.\n")
	}

	// 4. API Key
	apiKeyEnv := apiKeyEnvForProvider(name)
	apiKey, cancelled := promptLine(fmt.Sprintf("API Key (will be saved as %s)", apiKeyEnv))
	if cancelled {
		return nil, "", nil
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		fmt.Print("\r  No API key entered. You can set it later via the .env file.\n")
	} else {
		os.Setenv(apiKeyEnv, apiKey)
		if err := cli.SaveEnvValue(apiKeyEnv, apiKey); err != nil {
			return nil, "", fmt.Errorf("save API key: %w", err)
		}
	}

	cp := &cli.CustomProvider{
		Name:      name,
		Protocol:  protocol,
		BaseURL:   baseURL,
		APIKeyEnv: apiKeyEnv,
	}

	// 5. Try to auto-fetch models
	fmt.Print("\r\n")
	fmt.Printf("\r  Fetching models from %s...\n", name)
	models, err := fetchCustomModels(cp)
	if err != nil {
		fmt.Printf("\r  Could not fetch models: %v\n", err)
	}
	var selectedModel string
	var back bool
	if len(models) > 0 {
		// Store all fetched models for future reference
		for _, m := range models {
			cp.Models = append(cp.Models, cli.CustomModel{
				ID:      m.ID,
				Name:    m.Name,
				Context: m.Context,
			})
		}

		// Show interactive model picker
		fmt.Print("\r\n")
		selectedModel, back = selectProviderModelInteractive(cp.Name, models, "")
		if back {
			return nil, "", nil
		}
		if selectedModel == customModelOption {
			// User chose to enter manually — prompt for model name
			selectedModel = promptModelNameWithDefault(bufio.NewReader(os.Stdin), "", models[0].ID)
		}

		// If the selected model's context is unknown, prompt for it.
		for i := range cp.Models {
			if cp.Models[i].ID == selectedModel && cp.Models[i].Context == 0 {
				ctxInput, cancelled := promptLine(fmt.Sprintf("  Context window size (in tokens) for %s", selectedModel))
				if cancelled {
					return nil, "", nil
				}
				ctxInput = strings.TrimSpace(ctxInput)
				if ctxInput != "" {
					if n := parseSize(ctxInput); n > 0 {
						cp.Models[i].Context = n
					}
				}
				break
			}
		}
	} else {
		// 6. Manual model entry
		fmt.Print("\r  No models returned. You can enter models manually.\n")
		var cancelled bool
		cp.Models, cancelled = promptCustomModels()
		if cancelled {
			return nil, "", nil
		}
		if len(cp.Models) > 0 {
			selectedModel = cp.Models[0].ID
		}
	}

	// 7. Save to config
	if cfg.CustomProviders == nil {
		cfg.CustomProviders = make([]cli.CustomProvider, 0)
	}
	cfg.CustomProviders = append(cfg.CustomProviders, *cp)
	if err := cli.SaveConfig(cfg); err != nil {
		return nil, "", fmt.Errorf("save config: %w", err)
	}

	fmt.Print("\r\n")
	fmt.Printf("\r  Custom provider \"%s\" added successfully.\n", name)
	fmt.Print("\r\n")
	return cp, selectedModel, nil
}

// promptLine reads a single line of text in raw mode with caret editing
// support (see promptLineRaw), with ESC to cancel.
// Returns (line, true) if ESC was pressed, (line, false) on Enter.
func promptLine(label string) (string, bool) {
	if !cli.IsTerminal(os.Stdin.Fd()) {
		fmt.Printf("  %s: ", label)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		return strings.TrimRight(line, "\r\n"), false
	}

	fd := int(os.Stdin.Fd())
	oldState, err := cli.MakeRaw(fd)
	if err != nil {
		fmt.Printf("  %s: ", label)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		return strings.TrimRight(line, "\r\n"), false
	}
	defer cli.RestoreTerminal(fd, oldState)

	return promptLineRaw(fd, oldState, label, nil)
}

// apiKeyEnvForProvider generates a unique env var name for a custom provider's API key.
func apiKeyEnvForProvider(name string) string {
	key := strings.ToUpper(name)
	re := regexp.MustCompile(`[^A-Z0-9]+`)
	key = re.ReplaceAllString(key, "_")
	key = strings.Trim(key, "_")
	return "COVO_" + key + "_API_KEY"
}

// fetchCustomModels tries to fetch models from a custom provider's API.
func fetchCustomModels(cp *cli.CustomProvider) ([]cli.ProviderModel, error) {
	apiKey := os.Getenv(cp.APIKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("API key not set")
	}

	switch cp.Protocol {
	case "anthropic":
		return cli.FetchCustomAnthropicModels(cp.BaseURL, apiKey)
	case "gemini":
		return cli.FetchCustomGeminiModels(cp.BaseURL, apiKey)
	default:
		return cli.FetchCustomOpenAIModels(cp.BaseURL, apiKey)
	}
}

// promptCustomModels interactively prompts the user to enter models manually.
// Returns (models, true) if user cancelled, (models, false) on completion.
func promptCustomModels() ([]cli.CustomModel, bool) {
	var models []cli.CustomModel
	fmt.Print("\r\n")
	fmt.Print("\r  Enter models manually (leave Name blank to finish, Esc to cancel):\n")
	for {
		fmt.Print("\r\n")
		name, cancelled := promptLine("  Model name (display name)")
		if cancelled {
			return nil, true
		}
		name = strings.TrimSpace(name)
		if name == "" {
			break
		}

		id, cancelled := promptLine("  Model ID (API identifier)")
		if cancelled {
			return nil, true
		}
		id = strings.TrimSpace(id)
		if id == "" {
			fmt.Print("\r  Model ID is required. Skipping...\n")
			continue
		}

		ctxStr, cancelled := promptLine("  Context window size (in tokens)")
		if cancelled {
			return nil, true
		}
		ctxStr = strings.TrimSpace(ctxStr)
		ctx := 0
		if ctxStr != "" {
			if n := parseSize(ctxStr); n > 0 {
				ctx = n
			}
		}

		models = append(models, cli.CustomModel{
			Name:    name,
			ID:      id,
			Context: ctx,
		})
		fmt.Printf("\r  Added model: %s (ID: %s, Context: %d)\n", name, id, ctx)
	}
	return models, false
}

// Any provider not in this list appears after them, sorted alphabetically.
var providerOrder = map[string]int{
	"custom":       0,
	"openai":       1,
	"anthropic":    2,
	"gemini":       3,
	"xai":          4,
	"deepseek":     5,
	"openrouter":   6,
	"vertex":       7,
	"kimi-coding":  8,
	"qwen-oauth":   9,
	"opencode-zen": 10,
}

func providerChoices() []string {
	types := cli.RegisteredProviderTypes()
	sort.Slice(types, func(i, j int) bool {
		iCustom := types[i] == "custom" || strings.HasPrefix(types[i], "custom_")
		jCustom := types[j] == "custom" || strings.HasPrefix(types[j], "custom_")
		if iCustom || jCustom {
			return iCustom && !jCustom
		}
		// Within same group: configured first, then alphabetically.
		icfg := cli.HasProviderConfiguredFor(cli.ProviderName(types[i]))
		jcfg := cli.HasProviderConfiguredFor(cli.ProviderName(types[j]))
		if icfg != jcfg {
			return icfg
		}
		// Show known (order-mapped) providers before new ones.
		oi, hasI := providerOrder[types[i]]
		oj, hasJ := providerOrder[types[j]]
		if hasI != hasJ {
			return hasI
		}
		if hasI && hasJ {
			return oi < oj
		}
		return types[i] < types[j]
	})
	return types
}

func promptProviderSecrets(reader *bufio.Reader, provider string) error {
	apiKeyEnv := cli.ProviderAPIKeyEnv(provider)
	if !cli.HasProviderConfiguredFor(provider) {
		fmt.Printf("  %s (leave blank to keep current environment): ", apiKeyEnv)
		apiKey, _ := reader.ReadString('\n')
		apiKey = strings.TrimSpace(apiKey)
		if apiKey != "" {
			os.Setenv(apiKeyEnv, apiKey)
			if err := cli.SaveEnvValue(apiKeyEnv, apiKey); err != nil {
				return err
			}
		}
	}

	if cli.ProviderNeedsBaseURL(provider) {
		baseURLEnv := cli.ProviderBaseURLEnv(provider)
		if os.Getenv(baseURLEnv) == "" {
			fmt.Printf("  %s: ", baseURLEnv)
			baseURL, _ := reader.ReadString('\n')
			baseURL = strings.TrimSpace(baseURL)
			if baseURL != "" {
				os.Setenv(baseURLEnv, baseURL)
				if err := cli.SaveEnvValue(baseURLEnv, baseURL); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func selectModel(reader *bufio.Reader, provider, currentModel string) (string, bool) {
	fmt.Printf("  Fetching %s models from API...\n", cli.ProviderDisplayName(provider))
	models, err := cli.FetchProviderModels(provider)
	if err != nil {
		fmt.Printf("  (could not fetch %s models: %v)\n", cli.ProviderDisplayName(provider), err)
		fmt.Println("  Enter the model name manually.")
		return promptModelNameWithDefault(reader, currentModel, cli.DefaultModel(provider)), false
	}
	if len(models) == 0 {
		fmt.Printf("  (%s model API returned no models)\n", cli.ProviderDisplayName(provider))
		fmt.Println("  Enter the model name manually.")
		return promptModelNameWithDefault(reader, currentModel, cli.DefaultModel(provider)), false
	}

	if cli.IsTerminal(os.Stdin.Fd()) {
		return selectProviderModelInteractive(provider, models, currentModel)
	}

	options := appendUnique(filterProviderModels(models, "", 0), currentModel, cli.DefaultModel(provider), customModelOption)
	return selectModelFromChoices(reader, options, currentModel), false
}

func selectModelFromChoices(reader *bufio.Reader, options []string, currentModel string) string {
	choice := selectOne("Select default model", options, currentModel)
	if choice == customModelOption {
		return promptCustomModel(reader, currentModel)
	}
	return choice
}

func promptCustomModel(reader *bufio.Reader, currentModel string) string {
	return promptModelNameWithDefault(reader, currentModel, currentModel)
}

func promptModelNameWithDefault(reader *bufio.Reader, currentModel, defaultModel string) string {
	if defaultModel == "" {
		defaultModel = currentModel
	}
	if defaultModel != "" {
		fmt.Printf("  Enter model name [%s]: ", defaultModel)
	} else {
		fmt.Print("  Enter model name: ")
	}
	model, _ := reader.ReadString('\n')
	model = strings.TrimSpace(model)
	if model != "" {
		return model
	}
	return defaultModel
}

func selectProviderModelInteractive(provider string, models []cli.ProviderModel, currentModel string, scoped ...string) (string, bool) {
	fd := int(os.Stdin.Fd())
	oldState, err := cli.MakeRaw(fd)
	if err != nil {
		return selectModelFromChoices(bufio.NewReader(os.Stdin), appendUnique(filterProviderModels(models, "", modelPickerPageSize), currentModel, customModelOption), currentModel), false
	}
	defer cli.RestoreTerminal(fd, oldState)

	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")

	query := ""
	selected := 0
	offset := 0
	renderedLines := 0

	for {
		choices := providerModelChoices(models, query, currentModel, scoped...)
		if len(choices) == 0 {
			choices = []string{customModelOption}
		}
		if selected >= len(choices) {
			selected = len(choices) - 1
		}
		if selected < 0 {
			selected = 0
		}
		offset = adjustPickerOffset(selected, offset, modelPickerPageSize, len(choices))
		renderedLines = renderModelPicker(renderedLines, provider, query, choices, selected, offset, currentModel)

		var buf [8]byte
		n, readErr := os.Stdin.Read(buf[:])
		if readErr != nil || n == 0 {
			continue
		}
		b := buf[:n]
		if len(b) == 1 {
			switch b[0] {
			case 3:
				clearRenderedBlock(renderedLines)
				os.Exit(1)
			case 27:
				clearRenderedBlock(renderedLines)
				fmt.Print("\r  Back to provider selection\r\n")
				return "", true
			case 13, 10:
				clearRenderedBlock(renderedLines)
				choice := choices[selected]
				if choice == customModelOption {
					return promptCustomModelWithTerminalRestore(oldState, fd, currentModel), false
				}
				fmt.Printf("\r  Select default model: %s\r\n", choice)
				return choice, false
			case 127, 8:
				if query != "" {
					query = query[:len(query)-1]
					selected = 0
					offset = 0
				}
			default:
				if b[0] >= 32 && b[0] <= 126 {
					query += string(b[0])
					selected = 0
					offset = 0
				}
			}
			continue
		}

		if len(b) >= 3 && b[0] == 27 && b[1] == '[' {
			switch b[2] {
			case 'A':
				if selected > 0 {
					selected--
				} else {
					selected = len(choices) - 1
				}
			case 'B':
				if selected < len(choices)-1 {
					selected++
				} else {
					selected = 0
				}
			}
		}
	}
}

func promptCustomModelWithTerminalRestore(oldState *term.State, fd int, currentModel string) string {
	cli.RestoreTerminal(fd, oldState)
	fmt.Print("\033[?25h")
	return promptCustomModel(bufio.NewReader(os.Stdin), currentModel)
}

func providerModelChoices(models []cli.ProviderModel, query, currentModel string, scoped ...string) []string {
	matches := filterProviderModels(models, query, 0, scoped...)
	if strings.TrimSpace(query) == "" {
		return appendUnique(append([]string{currentModel}, matches...), customModelOption)
	}
	return appendUnique(matches, customModelOption)
}

func filterProviderModels(models []cli.ProviderModel, query string, limit int, scoped ...string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	var matches []string
	for _, m := range models {
		if query != "" && !providerModelMatches(m, query) {
			continue
		}
		if len(scoped) > 0 {
			found := false
			for _, s := range scoped {
				if strings.EqualFold(m.ID, s) || strings.EqualFold(m.Name, s) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		matches = append(matches, m.ID)
		if limit > 0 && len(matches) >= limit {
			break
		}
	}
	return matches
}

func providerModelMatches(model cli.ProviderModel, query string) bool {
	return strings.Contains(strings.ToLower(model.ID), query) ||
		strings.Contains(strings.ToLower(model.Name), query) ||
		strings.Contains(strings.ToLower(model.Description), query)
}

func adjustPickerOffset(selected, offset, pageSize, total int) int {
	if pageSize <= 0 || total <= pageSize {
		return 0
	}
	if selected < offset {
		return selected
	}
	if selected >= offset+pageSize {
		return selected - pageSize + 1
	}
	return offset
}

func renderModelPicker(previousLines int, provider, query string, choices []string, selected, offset int, currentModel string) int {
	clearRenderedBlock(previousLines)

	visibleEnd := offset + modelPickerPageSize
	if visibleEnd > len(choices) {
		visibleEnd = len(choices)
	}

	lineCount := 0
	fmt.Print("\r  Select default model (type to search, ↑↓ navigate, Enter confirm, Esc back):\r\n")
	lineCount++
	fmt.Printf("\r  Search: %s\r\n", overlayEndCursor(query))
	lineCount++
	fmt.Printf("\r  Showing %d-%d of %d %s models\r\n", offset+1, visibleEnd, len(choices), cli.ProviderDisplayName(provider))
	lineCount++

	for i := offset; i < visibleEnd; i++ {
		label := choices[i]
		if label == currentModel {
			label += "  ← currently in use"
		}
		if i == selected {
			fmt.Printf("\r  \033[7m  ▶ %s  \033[0m\r\n", label)
		} else {
			fmt.Printf("\r     %s\r\n", label)
		}
		lineCount++
	}
	return lineCount
}

func clearRenderedBlock(lineCount int) {
	if lineCount <= 0 {
		return
	}
	for i := 0; i < lineCount; i++ {
		fmt.Print("\r\033[A\033[2K")
	}
	fmt.Print("\r\033[2K")
}

func appendUnique(values []string, extras ...string) []string {
	seen := make(map[string]bool, len(values)+len(extras))
	result := make([]string, 0, len(values)+len(extras))
	for _, v := range append(values, extras...) {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		result = append(result, v)
	}
	return result
}

// parseSize parses a human-readable size string like "128000", "128K", "1M".
// Supports K (thousand), M (million) suffixes (case-insensitive). Returns 0 on failure.
func parseSize(s string) int {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0
	}
	multiplier := 1
	if strings.HasSuffix(s, "K") {
		multiplier = 1000
		s = strings.TrimSuffix(s, "K")
	} else if strings.HasSuffix(s, "M") {
		multiplier = 1000000
		s = strings.TrimSuffix(s, "M")
	} else if strings.HasSuffix(s, "B") {
		multiplier = 1000000000
		s = strings.TrimSuffix(s, "B")
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0
	}
	return n * multiplier
}
