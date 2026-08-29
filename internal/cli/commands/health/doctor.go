package health

import (
	"fmt"
	"github.com/covoyage/covo-agent/internal/cli"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/covoyage/covo-agent/internal/diag"
	"github.com/spf13/cobra"
)

func NewDoctorCommand() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			runDoctor(cmd.OutOrStdout(), fix)
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "attempt to auto-fix detected issues")
	return cmd
}

func runDoctor(w io.Writer, fix bool) {
	ok := true
	fixed := false

	profile := cli.ActiveProfileName()
	if profile != "" {
		fmt.Fprintf(w, "  Profile:   %s\n", profile)
	}

	// 1. Home directory
	homeDir, err := cli.HomeDir()
	if err != nil {
		fmt.Fprintf(w, "  ✗ home dir: %v\n", err)
		ok = false
	} else {
		if _, err := os.Stat(homeDir); os.IsNotExist(err) {
			if fix {
				if mkErr := os.MkdirAll(homeDir, 0755); mkErr == nil {
					fmt.Fprintf(w, "  ✓ home dir: %s (created)\n", homeDir)
					fixed = true
				} else {
					fmt.Fprintf(w, "  ✗ home dir: failed to create: %v\n", mkErr)
					ok = false
				}
			} else {
				fmt.Fprintf(w, "  ✗ home dir %s does not exist (run once to create, or use --fix)\n", homeDir)
				ok = false
			}
		} else {
			fmt.Fprintf(w, "  ✓ home dir: %s\n", homeDir)
		}
	}

	// 2. Config file
	cfgPath, _ := cli.ConfigPath()
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if fix {
			if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err == nil {
				defaultCfg := map[string]string{
					"provider": "openai",
					"model":    "gpt-5.6",
					"mode":     "code",
				}
				writeDefaultYAML(cfgPath, defaultCfg)
				fmt.Fprintf(w, "  ✓ config: %s (created with defaults)\n", cfgPath)
				fixed = true
			} else {
				fmt.Fprintf(w, "  ✗ config: failed to create: %v\n", err)
				ok = false
			}
		} else {
			fmt.Fprintf(w, "  ✗ config: %s not found (use --fix to create)\n", cfgPath)
			ok = false
		}
	} else {
		fmt.Fprintf(w, "  ✓ config: %s\n", cfgPath)
	}

	// 3. .env file
	envPath, _ := cli.EnvPath()
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		if fix {
			provider, _ := detectDefaultProvider()
			envKey := strings.ToUpper(provider) + "_API_KEY"
			content := fmt.Sprintf("# %s\n%s=\n", envPath, envKey)
			if err := os.WriteFile(envPath, []byte(content), 0600); err == nil {
				fmt.Fprintf(w, "  ✓ .env: %s (created with %s placeholder)\n", envPath, envKey)
				fixed = true
			} else {
				fmt.Fprintf(w, "  ✗ .env: failed to create: %v\n", err)
				ok = false
			}
		} else {
			fmt.Fprintf(w, "  - .env: %s not found (use --fix to create)\n", envPath)
		}
	} else {
		fmt.Fprintf(w, "  ✓ .env: %s\n", envPath)
	}

	// 4. API key check
	if err := cli.LoadDotEnv(); err == nil {
		cfg, err := cli.LoadConfig()
		if err == nil {
			provider := cli.ResolveProvider(cfg)
			model := cli.ResolveModel(cfg)
			apiKey := os.Getenv(strings.ToUpper(provider) + "_API_KEY")
			if apiKey == "" {
				apiKey = os.Getenv("API_KEY")
			}
			if apiKey == "" {
				if fix {
					fmt.Fprintf(w, "  - %s_API_KEY not set (edit %s to add your key)\n", strings.ToUpper(provider), envPath)
				} else {
					fmt.Fprintf(w, "  ✗ %s_API_KEY not set\n", strings.ToUpper(provider))
					ok = false
				}
			} else {
				fmt.Fprintf(w, "  ✓ %s_API_KEY set\n", strings.ToUpper(provider))
			}
			fmt.Fprintf(w, "  ✓ model: %s\n", model)
		}
	}

	// 5. Sessions directory
	sessionsDir := filepath.Join(homeDir, "sessions")
	if _, err := os.Stat(sessionsDir); os.IsNotExist(err) {
		if fix {
			if mkErr := os.MkdirAll(sessionsDir, 0755); mkErr == nil {
				fmt.Fprintf(w, "  ✓ sessions dir: %s (created)\n", sessionsDir)
				fixed = true
			} else {
				fmt.Fprintf(w, "  ✗ sessions dir: failed to create: %v\n", mkErr)
				ok = false
			}
		} else {
			fmt.Fprintf(w, "  - sessions dir: %s not found\n", sessionsDir)
		}
	} else {
		fmt.Fprintf(w, "  ✓ sessions dir: %s\n", sessionsDir)
	}

	// 6. Skills directory
	skillsDir := filepath.Join(homeDir, "skills")
	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		if fix {
			if mkErr := os.MkdirAll(skillsDir, 0755); mkErr == nil {
				fmt.Fprintf(w, "  ✓ skills dir: %s (created)\n", skillsDir)
				fixed = true
			} else {
				fmt.Fprintf(w, "  ✗ skills dir: failed to create: %v\n", mkErr)
				ok = false
			}
		} else {
			fmt.Fprintf(w, "  - skills dir: %s not found\n", skillsDir)
		}
	} else {
		fmt.Fprintf(w, "  ✓ skills dir: %s\n", skillsDir)
	}

	// 7. Chrome/Chromium availability
	chromePaths := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	}
	chromeFound := false
	for _, p := range chromePaths {
		if _, err := os.Stat(p); err == nil {
			fmt.Fprintf(w, "  ✓ Chrome: %s\n", p)
			chromeFound = true
			break
		}
	}
	if !chromeFound {
		if path := os.Getenv("CHROME_PATH"); path != "" {
			if _, err := os.Stat(path); err == nil {
				fmt.Fprintf(w, "  ✓ Chrome: %s (CHROME_PATH)\n", path)
				chromeFound = true
			}
		}
	}
	if !chromeFound {
		if _, err := exec.LookPath("google-chrome"); err == nil {
			chromeFound = true
			fmt.Fprintln(w, "  ✓ Chrome: found in PATH")
		}
	}
	if !chromeFound {
		if _, err := exec.LookPath("chromium"); err == nil {
			chromeFound = true
			fmt.Fprintln(w, "  ✓ Chromium: found in PATH")
		}
	}
	if !chromeFound {
		if fix {
			fmt.Fprintln(w, "  - Chrome/Chromium: not found (browser tool will use openURL fallback; install Chrome for full browser support)")
		} else {
			fmt.Fprintln(w, "  - Chrome/Chromium: not found (browser tool will use openURL fallback)")
		}
	}

	// 8. Git availability
	if _, err := exec.LookPath("git"); err == nil {
		fmt.Fprintln(w, "  ✓ git: found in PATH")
	} else {
		if fix {
			fmt.Fprintln(w, "  - git: not found (checkpoints disabled; install git for full support)")
		} else {
			fmt.Fprintln(w, "  - git: not found (checkpoints disabled)")
		}
	}

	// 9. Terminal diagnostics (color, clipboard, tmux, keyboard)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Terminal Diagnostics:")
	termReport := diag.RunDiagnostics()
	termReport.Print(w)
	if termReport.HasIssues() {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  Suggested fixes:")
		termReport.PrintFixes(w)
	}

	if !ok {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  Some checks failed. Run `covo-agent doctor --fix` to auto-fix, or `covo-agent model` to configure.\n")
	}
	if fixed {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  Fixed some issues. Run `covo-agent doctor` again to verify.")
	}
}

func detectDefaultProvider() (string, string) {
	candidates := []struct {
		envVar   string
		provider string
		model    string
	}{
		{"ANTHROPIC_API_KEY", "anthropic", "claude-sonnet-4-20250514"},
		{"OPENAI_API_KEY", "openai", "gpt-5.6"},
		{"GEMINI_API_KEY", "gemini", "gemini-2.5-flash"},
		{"XIAOMI_API_KEY", "xiaomi", "deepseek-v3-0324"},
		{"CUSTOM_API_KEY", "custom", ""},
	}
	// Check env first (already loaded)
	for _, c := range candidates {
		if os.Getenv(c.envVar) != "" {
			return c.provider, c.model
		}
	}
	return "openai", "gpt-5.6"
}

func writeDefaultYAML(path string, kv map[string]string) {
	var b strings.Builder
	for k, v := range kv {
		b.WriteString(fmt.Sprintf("%s: %s\n", k, v))
	}
	_ = os.WriteFile(path, []byte(b.String()), 0644)
}
