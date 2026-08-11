package cli

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// PromptSecret reads a secret from stdin without echoing characters.
// On terminals, it hides all input. On non-terminals, it reads a plain line.
func PromptSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		var input string
		_, err := fmt.Scanln(&input)
		fmt.Fprintln(os.Stderr)
		return input, err
	}

	password, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	return string(password), err
}

// PromptSecretMasked reads a secret from stdin, echoing a mask character
// (e.g. '*') for each typed character. Supports backspace to correct input.
//
// On POSIX systems this uses raw terminal mode for character-by-character
// reading. On Windows it uses the console API via golang.org/x/term.
//
// Falls back to term.ReadPassword (no mask) if raw mode cannot be entered.
func PromptSecretMasked(prompt string, mask rune) (string, error) {
	fmt.Fprint(os.Stderr, prompt)

	fd := int(os.Stdin.Fd())

	// Non-terminal fallback: read a plain line.
	if !term.IsTerminal(fd) {
		var input string
		_, err := fmt.Scanln(&input)
		fmt.Fprintln(os.Stderr)
		return input, err
	}

	// Enter raw mode so we can read one byte at a time and echo the mask.
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		// Fallback: standard password input (no echo at all).
		password, fallbackErr := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return string(password), fallbackErr
	}
	defer func() {
		_ = term.Restore(fd, oldState)
	}()

	var buf []byte
	one := make([]byte, 1)
	maskBytes := []byte(string(mask))

	for {
		n, readErr := os.Stdin.Read(one)
		if readErr != nil || n == 0 {
			break
		}

		switch one[0] {
		case 3: // Ctrl+C → interrupt
			fmt.Fprint(os.Stderr, "^C\r\n")
			return "", fmt.Errorf("interrupted")

		case 4: // Ctrl+D on empty input → EOF
			if len(buf) == 0 {
				fmt.Fprint(os.Stderr, "\r\n")
				return "", fmt.Errorf("EOF")
			}
			// Ctrl+D with content: ignore (treat as regular)
			continue

		case 13: // Enter (CR)
			fmt.Fprint(os.Stderr, "\r\n")
			return string(buf), nil

		case 10: // LF (some terminals send just LF)
			fmt.Fprint(os.Stderr, "\r\n")
			return string(buf), nil

		case 127: // DEL (Backspace on macOS/Linux)
			fallthrough
		case 8: // BS (Backspace on some terminals)
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				// Erase the last mask character on screen.
				fmt.Fprint(os.Stderr, "\b \b")
			}

		default:
			// Only accept printable ASCII (space through ~) and UTF-8 multi-byte.
			if one[0] >= 32 {
				buf = append(buf, one[0])
				os.Stderr.Write(maskBytes)
			}
		}
	}

	return string(buf), nil
}
