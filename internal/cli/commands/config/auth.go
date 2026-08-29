package config

import (
	"context"
	"fmt"
	"github.com/covoyage/covo-agent/internal/cli"
	"os"
	"path/filepath"
	"strings"

	"github.com/covoyage/covo-agent/internal/auth"
	"github.com/spf13/cobra"
)

func NewAuthCommand() *cobra.Command {
	authCmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage credentials",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cmdAuth(nil)
			return nil
		},
	}
	authCmd.AddCommand(&cobra.Command{Use: "list", Short: "List configured API keys", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { cmdAuth([]string{"list"}); return nil }})
	authCmd.AddCommand(&cobra.Command{Use: "add <key=value>", Short: "Set an environment variable in .env", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error { cmdAuth([]string{"add", args[0]}); return nil }})
	authCmd.AddCommand(&cobra.Command{Use: "remove <key>", Short: "Remove an environment variable from .env", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error { cmdAuth([]string{"remove", args[0]}); return nil }})
	authCmd.AddCommand(&cobra.Command{Use: "login [provider]", Short: "OAuth login for a provider", Args: cobra.MaximumNArgs(1), ValidArgs: []string{"github", "openai", "anthropic", "gemini"}, RunE: func(_ *cobra.Command, args []string) error { cmdAuth(append([]string{"login"}, args...)); return nil }})
	return authCmd
}

func cmdAuth(args []string) {
	homeDir, err := cli.HomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "home dir: %v\n", err)
		return
	}

	if len(args) == 0 || args[0] == "help" {
		fmt.Fprintln(os.Stderr, "Usage: covo-agent auth <command> [args]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  list                  List configured API keys")
		fmt.Fprintln(os.Stderr, "  add <key>=<value>     Set an environment variable in .env")
		fmt.Fprintln(os.Stderr, "  remove <key>          Remove an environment variable from .env")
		fmt.Fprintln(os.Stderr, "  login [provider]      OAuth login for a provider (github, openai, anthropic, gemini)")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "  Keys are stored in: %s\n", filepath.Join(homeDir, ".env"))
		return
	}

	switch args[0] {
	case "list":
		envPath := filepath.Join(homeDir, ".env")
		data, err := os.ReadFile(envPath)
		if err != nil {
			fmt.Println("  No credentials configured.")
			return
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			idx := strings.Index(line, "=")
			if idx < 0 {
				continue
			}
			key := line[:idx]
			val := line[idx+1:]
			if len(val) > 8 {
				val = val[:4] + "****" + val[len(val)-4:]
			}
			fmt.Printf("  %s=%s\n", key, val)
		}

	case "add":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: covo-agent auth add <key>=<value>")
			return
		}
		idx := strings.Index(args[1], "=")
		if idx < 0 {
			fmt.Fprintln(os.Stderr, "  ✗ Expected format: KEY=VALUE")
			return
		}
		key := strings.TrimSpace(args[1][:idx])
		val := strings.TrimSpace(args[1][idx+1:])
		if key == "" {
			fmt.Fprintln(os.Stderr, "  ✗ Key cannot be empty")
			return
		}
		if err := cli.SaveEnvValue(key, val); err != nil {
			fmt.Fprintf(os.Stderr, "save: %v\n", err)
			return
		}
		fmt.Printf("  ✓ %s set\n", key)

	case "remove":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: covo-agent auth remove <key>")
			return
		}
		_ = args[1]
		// Read existing, filter out the key, re-write
		envPath := filepath.Join(homeDir, ".env")
		data, err := os.ReadFile(envPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read .env: %v\n", err)
			return
		}
		var lines []string
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, args[1]+"=") {
				continue
			}
			lines = append(lines, line)
		}
		if err := os.WriteFile(envPath, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
			fmt.Fprintf(os.Stderr, "write .env: %v\n", err)
			return
		}
		fmt.Printf("  ✓ %s removed\n", args[1])

	case "login":
		cmdAuthLogin(args[1:])

	default:
		fmt.Fprintf(os.Stderr, "Unknown auth subcommand: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "Available: list, add, remove, login")
	}
}

type providerInfo struct {
	Name    string
	Type    string
	EnvVar  string
	KeyPage string
}

var loginProviders = []providerInfo{
	{Name: "GitHub", Type: "github", EnvVar: "GITHUB_TOKEN", KeyPage: "https://github.com/settings/tokens"},
	{Name: "OpenAI", Type: "openai", EnvVar: "OPENAI_API_KEY", KeyPage: "https://platform.openai.com/api-keys"},
	{Name: "Anthropic", Type: "anthropic", EnvVar: "ANTHROPIC_API_KEY", KeyPage: "https://console.anthropic.com/settings/keys"},
	{Name: "Google Gemini", Type: "gemini", EnvVar: "GEMINI_API_KEY", KeyPage: "https://aistudio.google.com/apikey"},
}

func cmdAuthLogin(args []string) {
	switch {
	case len(args) == 0:
		showLoginMenu()
	case len(args) == 1:
		runLoginFlow(strings.ToLower(args[0]))
	default:
		fmt.Fprintln(os.Stderr, "Usage: covo-agent auth login [provider]")
		fmt.Fprintln(os.Stderr, "Providers: github, openai, anthropic, gemini")
	}
}

func showLoginMenu() {
	fmt.Fprintln(os.Stderr, "Select a provider to log in:")
	for i, p := range loginProviders {
		fmt.Fprintf(os.Stderr, "  %d. %s\n", i+1, p.Name)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "Enter number (1-%d) or name: ", len(loginProviders))

	var input string
	if _, err := fmt.Scanln(&input); err != nil {
		fmt.Fprintf(os.Stderr, "read: %v\n", err)
		return
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return
	}

	// Try numeric selection
	for i, p := range loginProviders {
		if input == fmt.Sprintf("%d", i+1) {
			runLoginFlow(p.Type)
			return
		}
	}

	// Try name match
	runLoginFlow(strings.ToLower(input))
}

func runLoginFlow(providerType string) {
	ctx := context.Background()

	switch providerType {
	case "github":
		result, err := auth.GitHubDeviceLogin(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ GitHub login failed: %v\n", err)
			return
		}
		if err := cli.SaveEnvValue(result.EnvVar, result.Value); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ save: %v\n", err)
			return
		}
		fmt.Fprintf(os.Stderr, "  ✓ %s credentials saved\n", result.Provider)

	case "openai", "anthropic", "gemini":
		var info providerInfo
		found := false
		for _, p := range loginProviders {
			if p.Type == providerType {
				info = p
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "  ✗ Unknown provider: %s\n", providerType)
			return
		}
		key, err := auth.PromptAPIKey(info.Name, info.KeyPage)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", info.Name, err)
			return
		}
		if err := cli.SaveEnvValue(info.EnvVar, key); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ save: %v\n", err)
			return
		}
		fmt.Fprintf(os.Stderr, "  ✓ %s credentials saved\n", info.Name)

	default:
		fmt.Fprintf(os.Stderr, "  ✗ Unknown provider: %s\n", providerType)
		fmt.Fprintln(os.Stderr, "  Available: github, openai, anthropic, gemini")
	}
}
