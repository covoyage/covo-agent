package auth

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type LoginResult struct {
	EnvVar   string
	Value    string
	Provider string
}

func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	return exec.Command(cmd, args...).Start()
}

// PromptAPIKey shows a prompt for the user to paste an API key and returns it.
// Opens the provider's API key page in the browser.
func PromptAPIKey(provider, keyPageURL string) (string, error) {
	fmt.Fprintf(os.Stderr, "\n  Opening %s to get your API key...\n\n", provider)
	_ = openBrowser(keyPageURL)
	fmt.Fprintf(os.Stderr, "  ───────────────────────────────────────────────\n")
	fmt.Fprintf(os.Stderr, "  1. A browser window should open to %s\n", provider)
	fmt.Fprintf(os.Stderr, "  2. If it doesn't, visit: %s\n", keyPageURL)
	fmt.Fprintf(os.Stderr, "  3. Generate/copy your API key\n")
	fmt.Fprintf(os.Stderr, "  4. Paste it here and press Enter:\n")
	fmt.Fprintf(os.Stderr, "  ───────────────────────────────────────────────\n")
	fmt.Fprintf(os.Stderr, "  ➤ ")

	var key string
	_, err := fmt.Scanln(&key)
	if err != nil {
		return "", fmt.Errorf("read key: %w", err)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("empty key")
	}
	return key, nil
}
