package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/covoyage/covo-agent/internal/lsp"

	"github.com/covoyage/covo-agent/internal/i18n"
)

func printJSON(v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "JSON error: %v\n", err)
		return
	}
	fmt.Println(string(data))
}

type lspInstallRecipe struct {
	Name        string
	Description string
	Check       func() bool
	Install     func() error
}

var installRecipes = []lspInstallRecipe{
	{
		Name: "gopls", Description: "Go language server (gopls)",
		Check: func() bool { return findLSPCommand("gopls") != "" },
		Install: func() error {
			return runInstall("go", "install", "golang.org/x/tools/gopls@latest")
		},
	},
	{
		Name: "pyright", Description: "Python language server (pyright-langserver)",
		Check: func() bool { return findLSPCommand("pyright-langserver") != "" },
		Install: func() error {
			return runInstall("npm", "install", "-g", "pyright")
		},
	},
	{
		Name: "typescript-language-server", Description: "TypeScript/JavaScript language server",
		Check: func() bool { return findLSPCommand("typescript-language-server") != "" },
		Install: func() error {
			return runInstall("npm", "install", "-g", "typescript-language-server")
		},
	},
	{
		Name: "rust-analyzer", Description: "Rust language server (rust-analyzer)",
		Check: func() bool { return findLSPCommand("rust-analyzer") != "" },
		Install: func() error {
			return runInstall("rustup", "component", "add", "rust-analyzer")
		},
	},
	{
		Name: "lua-language-server", Description: "Lua language server",
		Check: func() bool { return findLSPCommand("lua-language-server") != "" },
		Install: func() error {
			return runInstall("brew", "install", "lua-language-server")
		},
	},
	{
		Name: "vscode-json-language-server", Description: "JSON language server",
		Check: func() bool { return findLSPCommand("vscode-json-languageserver") != "" },
		Install: func() error {
			return runInstall("npm", "install", "-g", "vscode-json-languageserver")
		},
	},
	{
		Name: "yaml-language-server", Description: "YAML language server",
		Check: func() bool { return findLSPCommand("yaml-language-server") != "" },
		Install: func() error {
			return runInstall("npm", "install", "-g", "yaml-language-server")
		},
	},
	{
		Name: "marksman", Description: "Markdown language server",
		Check: func() bool { return findLSPCommand("marksman") != "" },
		Install: func() error {
			return runInstall("brew", "install", "marksman")
		},
	},
	{
		Name: "clangd", Description: "C/C++ language server (clangd)",
		Check: func() bool { return findLSPCommand("clangd") != "" },
		Install: func() error {
			if err := runInstall("brew", "install", "llvm"); err != nil {
				return err
			}
			// llvm is keg-only, clangd not in PATH by default — symlink it
			prefix, err := exec.Command("brew", "--prefix", "llvm").Output()
			if err != nil {
				return fmt.Errorf("get llvm prefix: %w", err)
			}
			clangdPath := strings.TrimSpace(string(prefix)) + "/bin/clangd"
			return exec.Command("ln", "-sf", clangdPath, "/usr/local/bin/clangd").Run()
		},
	},
	{
		Name: "zls", Description: "Zig language server (zls)",
		Check: func() bool { return findLSPCommand("zls") != "" },
		Install: func() error {
			return runInstall("brew", "install", "zls")
		},
	},
	{
		Name: "nil", Description: "Nix language server (nil)",
		Check: func() bool { return findLSPCommand("nil") != "" },
		Install: func() error {
			return runInstall("go", "install", "github.com/nix-community/nil@latest")
		},
	},
	{
		Name: "sourcekit-lsp", Description: "Swift language server",
		Check: func() bool { return findLSPCommand("sourcekit-lsp") != "" },
		Install: func() error {
			fmt.Println("  sourcekit-lsp ships with Xcode. Install Xcode from the Mac App Store or")
			fmt.Println("  download the Swift toolchain from https://www.swift.org/install/")
			return nil
		},
	},
}

func cmdLSP(args []string) {
	if len(args) == 0 || args[0] == "help" {
		fmt.Fprintln(os.Stderr, i18n.T("lsp.usage"))
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, i18n.T("lsp.commands"))
		fmt.Fprintln(os.Stderr, "  status     "+i18n.T("lsp.status_cmd"))
		fmt.Fprintln(os.Stderr, "  install    "+i18n.T("lsp.install_cmd"))
		return
	}

	switch args[0] {
	case "status":
		cmdLSPStatus(args[1:])
	case "install":
		cmdLSPInstall(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown lsp command: %s\n", args[0])
		fmt.Fprintln(os.Stderr, i18n.T("lsp.available"))
	}
}

func cmdLSPStatus(args []string) {
	flagSet := flag.NewFlagSet("lsp-status", flag.ExitOnError)
	jsonOutput := flagSet.Bool("json", false, "Output in JSON format")
	_ = flagSet.Parse(args)

	servers := lsp.AllServers()

	if *jsonOutput {
		type serverStatus struct {
			Name    string `json:"name"`
			Command string `json:"command"`
			Found   bool   `json:"found"`
		}
		type lspStatus struct {
			Servers []serverStatus `json:"servers"`
		}
		status := lspStatus{}
		for _, s := range servers {
			found := findLSPCommand(s.Command) != ""
			status.Servers = append(status.Servers, serverStatus{
				Name:    s.ID,
				Command: s.Command,
				Found:   found,
			})
		}
		printJSON(status)
		return
	}

	fmt.Println("LSP Server Status:")
	fmt.Println(strings.Repeat("-", 60))

	var available, missing []lsp.ServerDef
	for _, s := range servers {
		if findLSPCommand(s.Command) != "" {
			available = append(available, s)
		} else {
			missing = append(missing, s)
		}
	}

	sort.Slice(available, func(i, j int) bool { return available[i].ID < available[j].ID })
	sort.Slice(missing, func(i, j int) bool { return missing[i].ID < missing[j].ID })

	if len(available) > 0 {
		fmt.Printf("\n\033[32m✓ Available (%d):\033[0m\n", len(available))
		for _, s := range available {
			fmt.Printf("  %-20s %s\n", s.ID, s.Command)
		}
	}

	if len(missing) > 0 {
		fmt.Printf("\n\033[33m✗ Missing (%d):\033[0m\n", len(missing))
		for _, s := range missing {
			recipe := findRecipe(s.ID)
			hint := ""
			if recipe != nil {
				hint = fmt.Sprintf("  → run: covo-agent lsp install %s", s.ID)
			}
			fmt.Printf("  %-20s %-30s %s\n", s.ID, s.Command, hint)
		}
	}

	fmt.Println()
	if len(missing) > 0 {
		fmt.Println("Run 'covo-agent lsp install' to install missing servers.")
	} else {
		fmt.Println("All LSP servers are available.")
	}
}

func cmdLSPInstall(args []string) {
	flagSet := flag.NewFlagSet("lsp-install", flag.ExitOnError)
	_ = flagSet.Parse(args)

	targets := flagSet.Args()

	if len(targets) > 0 {
		for _, name := range targets {
			recipe := findRecipe(name)
			if recipe == nil {
				fmt.Fprintf(os.Stderr, "  ✗ Unknown LSP server: %s\n", name)
				continue
			}
			if recipe.Check() {
				fmt.Printf("  ✓ %s already installed\n", recipe.Name)
				continue
			}
			fmt.Printf("  Installing %s...\n", recipe.Description)
			if err := recipe.Install(); err != nil {
				fmt.Fprintf(os.Stderr, "  ✗ Failed to install %s: %v\n", recipe.Name, err)
			} else {
				fmt.Printf("  ✓ %s installed\n", recipe.Name)
			}
		}
		return
	}

	var toInstall []lspInstallRecipe
	for _, r := range installRecipes {
		if !r.Check() {
			toInstall = append(toInstall, r)
		}
	}

	if len(toInstall) == 0 {
		fmt.Println("All LSP servers are already installed.")
		return
	}

	fmt.Println("The following LSP servers are missing:")
	for _, r := range toInstall {
		fmt.Printf("  • %s (%s)\n", r.Name, r.Description)
	}

	fmt.Println()
	fmt.Print("Install all missing servers? [y/N] ")
	var resp string
	fmt.Scanln(&resp)
	resp = strings.ToLower(strings.TrimSpace(resp))
	if resp != "y" && resp != "yes" {
		fmt.Println("Skipping installation.")
		return
	}

	for _, r := range toInstall {
		fmt.Printf("  Installing %s...\n", r.Description)
		if err := r.Install(); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ Failed: %v\n", err)
		} else {
			fmt.Printf("  ✓ %s installed\n", r.Name)
		}
	}
}

func findRecipe(name string) *lspInstallRecipe {
	for i, r := range installRecipes {
		if r.Name == name {
			return &installRecipes[i]
		}
	}
	return nil
}

func findLSPCommand(name string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	// Some LSP servers (sourcekit-lsp) are managed by xcrun on macOS
	if path, err := exec.LookPath("xcrun"); err == nil {
		if out, err := exec.Command(path, "--find", name).Output(); err == nil && len(out) > 0 {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}

func runInstall(command string, args ...string) error {
	cmd := exec.Command(command, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
