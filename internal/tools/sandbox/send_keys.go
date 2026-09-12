package sandbox

import (
	"fmt"
	"strings"
	"unicode"
)

const sendKeysMaxTokens = 32

func parseSendKeys(input string) (tokens []string, payload []byte, signals []string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, nil, nil, fmt.Errorf("keys is required")
	}
	fields := strings.Fields(input)
	if len(fields) > sendKeysMaxTokens {
		return nil, nil, nil, fmt.Errorf("too many key tokens (max %d)", sendKeysMaxTokens)
	}
	for _, raw := range fields {
		token, bytes, signal, perr := decodeKeyToken(raw)
		if perr != nil {
			return nil, nil, nil, perr
		}
		tokens = append(tokens, token)
		payload = append(payload, bytes...)
		if signal != "" {
			signals = append(signals, signal)
		}
	}
	return tokens, payload, signals, nil
}

func decodeKeyToken(raw string) (token string, payload []byte, signal string, err error) {
	orig := raw
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", nil, "", fmt.Errorf("empty key token")
	}
	lower := strings.ToLower(strings.ReplaceAll(key, "_", "-"))
	switch lower {
	case "enter", "return", "cr":
		return "Enter", []byte{'\n'}, "", nil
	case "escape", "esc":
		return "Escape", []byte{0x1b}, "", nil
	case "tab":
		return "Tab", []byte{'\t'}, "", nil
	case "space":
		return "Space", []byte{' '}, "", nil
	case "backspace", "bs":
		return "Backspace", []byte{0x7f}, "", nil
	case "c-c", "ctrl-c", "ctrl+c":
		return "C-c", []byte{0x03}, "INT", nil
	case "c-d", "ctrl-d", "ctrl+d":
		return "C-d", []byte{0x04}, "", nil
	case "c-z", "ctrl-z", "ctrl+z":
		return "C-z", []byte{0x1a}, "TSTP", nil
	case "c-l", "ctrl-l", "ctrl+l":
		return "C-l", []byte{0x0c}, "", nil
	case "c-u", "ctrl-u", "ctrl+u":
		return "C-u", []byte{0x15}, "", nil
	case "c-w", "ctrl-w", "ctrl+w":
		return "C-w", []byte{0x17}, "", nil
	}
	if strings.HasPrefix(lower, "c-") && len(lower) == 3 {
		ch := lower[2]
		if ch >= 'a' && ch <= 'z' {
			return "C-" + string(ch), []byte{ch - 'a' + 1}, "", nil
		}
	}
	if isPrintableLiteral(key) {
		return key, []byte(key), "", nil
	}
	return "", nil, "", fmt.Errorf("unknown key token %q", orig)
}

func isPrintableLiteral(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
