package shared

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/covoyage/covo-agent/internal/cli"
)

// DetectConfiguredProviders returns the registered provider types that have
// an API key present in the environment.
func DetectConfiguredProviders() []string {
	var detected []string
	for _, pt := range cli.RegisteredProviderTypes() {
		apiKeyEnv := cli.ProviderAPIKeyEnv(pt)
		if apiKeyEnv != "" && os.Getenv(apiKeyEnv) != "" {
			detected = append(detected, pt)
		}
	}
	return detected
}

// AskYesNo prints a yes/no prompt and reads the answer from reader.
func AskYesNo(reader *bufio.Reader, prompt string, defaultYes bool) bool {
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

// SelectOneOrCancel shows a raw-mode arrow-key list selector.
// Returns the selected option, or "" if the user pressed Esc to cancel.
func SelectOneOrCancel(label string, options []string, defaultVal string) string {
	if !cli.IsTerminal(os.Stdin.Fd()) {
		return PromptChoice(bufio.NewReader(os.Stdin), label, options, defaultVal)
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
		return PromptChoice(bufio.NewReader(os.Stdin), label, options, defaultVal)
	}
	defer cli.RestoreTerminal(fd, oldState)

	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")

	RenderSelect(label, options, selected)

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			continue
		}
		if n == 1 {
			switch buf[0] {
			case 13:
				ClearSelect(len(options))
				fmt.Printf("\r  %s: %s\r\n", label, options[selected])
				return options[selected]
			case 3:
				ClearSelect(len(options))
				fmt.Print("\033[?25h")
				os.Exit(1)
			case 27:
				ClearSelect(len(options))
				fmt.Print("\r  Cancelled.\r\n")
				return ""
			case 'q':
				ClearSelect(len(options))
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
				RenderSelect(label, options, selected)
			case 'B':
				if selected < len(options)-1 {
					selected++
				} else {
					selected = 0
				}
				RenderSelect(label, options, selected)
			}
		}
	}
}

// SelectOne shows a raw-mode arrow-key list selector. Esc exits the program.
func SelectOne(label string, options []string, defaultVal string) string {
	if !cli.IsTerminal(os.Stdin.Fd()) {
		return PromptChoice(bufio.NewReader(os.Stdin), label, options, defaultVal)
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
		return PromptChoice(bufio.NewReader(os.Stdin), label, options, defaultVal)
	}
	defer cli.RestoreTerminal(fd, oldState)

	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")

	RenderSelect(label, options, selected)

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			continue
		}
		if n == 1 {
			switch buf[0] {
			case 13:
				ClearSelect(len(options))
				fmt.Printf("\r  %s: %s\r\n", label, options[selected])
				return options[selected]
			case 3:
				ClearSelect(len(options))
				fmt.Print("\033[?25h")
				os.Exit(1)
			case 27:
				ClearSelect(len(options))
				fmt.Print("\033[?25h")
				fmt.Print("\r  Cancelled.\r\n")
				os.Exit(0)
			case 'q':
				ClearSelect(len(options))
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
				RenderSelect(label, options, selected)
			case 'B':
				if selected < len(options)-1 {
					selected++
				} else {
					selected = 0
				}
				RenderSelect(label, options, selected)
			}
		}
	}
}

// RenderSelect renders the arrow-key list selector.
func RenderSelect(label string, options []string, selected int) {
	ClearSelect(len(options))
	fmt.Printf("\r  %s (↑↓ navigate, Enter confirm, Esc exit):\r\n", label)
	for i, opt := range options {
		if i == selected {
			fmt.Printf("\r  \033[7m  ▶ %s  \033[0m\r\n", opt)
		} else {
			fmt.Printf("\r     %s\r\n", opt)
		}
	}
}

// RenderProviderList renders the provider list with category headers, status,
// search bar, and pagination. Pass pageSize=0 to show all items.
func RenderProviderList(label string, options []string, selected, offset, pageSize int, search string, searching bool, defaultVal string) {
	// Build all output lines first so we can count them accurately.
	var lines []string

	// Search bar
	if searching {
		lines = append(lines, fmt.Sprintf("\033[33mSearch:\033[0m %s  backspace edit  enter confirm  esc cancel", OverlayEndCursor(search)))
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

// ClearScreenHome clears the whole terminal and moves the cursor to the
// top-left corner, anchoring an interactive picker at the top of the window.
func ClearScreenHome() {
	fmt.Print("\033[2J\033[H")
}

// ClearSelect erases the lines previously drawn by RenderSelect.
func ClearSelect(count int) {
	for i := 0; i <= count; i++ {
		fmt.Print("\r\033[A\033[2K")
	}
}

// OverlayEndCursor renders s with the last character shown as a block cursor
// (reverse video), so the cursor does not consume an extra character cell.
// An empty string renders as a single block.
func OverlayEndCursor(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return "▎"
	}
	return string(r[:len(r)-1]) + "\033[7m" + string(r[len(r)-1]) + "\033[0m"
}

// PromptChoice shows a numbered fallback prompt reading a line from reader.
func PromptChoice(reader *bufio.Reader, label string, options []string, defaultVal string) string {
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
