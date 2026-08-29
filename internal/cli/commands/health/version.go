package health

import (
	"fmt"
	"github.com/covoyage/covo-agent/internal/cli"
	"os"
	"path/filepath"
	"runtime"

	"github.com/covoyage/covo-agent/internal/sandbox/ossandbox"
	"github.com/covoyage/covo-agent/internal/selfupdate"
)

var Version = cli.Version

func cmdVersion() {
	fmt.Printf("covo-agent v%s\n", Version)
	fmt.Println("https://github.com/covoyage/covo-agent")
}

func cmdUpdate() {
	fmt.Printf("covo-agent v%s\n", Version)
	fmt.Println("Checking for updates...")

	latest, url, digest, err := selfupdate.CheckForUpdates(Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking for updates: %v\n", err)
		os.Exit(1)
	}
	if url == "" {
		if latest != "" && latest != Version {
			fmt.Printf("Latest version: %s (no compatible binary for %s/%s)\n", latest, runtime.GOOS, runtime.GOARCH)
		} else {
			fmt.Println("You're up to date!")
		}
		return
	}

	fmt.Printf("New version available: %s (current: %s)\n", latest, Version)
	fmt.Println("Downloading...")

	if err := selfupdate.PerformUpdate(url, digest); err != nil {
		fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Update complete! Please restart covo-agent.")
}

func cmdStatus() {
	if err := cli.LoadDotEnv(); err != nil {
		return
	}
	cfg, err := cli.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		return
	}
	homeDir, _ := cli.HomeDir()

	provider := cli.ResolveProvider(cfg)
	model := cli.ResolveModel(cfg)
	mode := cli.ResolveMode(cfg)
	providerDisplay := provider
	if displayName := cli.ProviderDisplayName(provider); displayName != "" {
		providerDisplay = displayName
	}

	if profile := cli.ActiveProfileName(); profile != "" {
		fmt.Printf("  Profile:   %s\n", profile)
	}
	fmt.Printf("  Home:      %s\n", homeDir)
	fmt.Printf("  Provider:  %s\n", providerDisplay)
	fmt.Printf("  Model:     %s\n", model)
	fmt.Printf("  Mode:      %s\n", mode)

	// Sandbox status
	if ossandbox.IsActive() {
		sbProfile := ossandbox.ActiveProfile()
		fmt.Printf("  Sandbox:   %s (active, kernel-enforced)\n", sbProfile)
		if ossandbox.ShouldRestrictChildNetwork() {
			fmt.Printf("             child process network: restricted\n")
		}
	} else if envSandbox := os.Getenv("COVO_SANDBOX"); envSandbox != "" {
		fmt.Printf("  Sandbox:   %s (configured via COVO_SANDBOX)\n", envSandbox)
	} else {
		fmt.Printf("  Sandbox:   off\n")
	}

	fmt.Printf("  Config:    %s\n", filepath.Join(homeDir, "config.yaml"))
	fmt.Printf("  Sessions:  %s\n", filepath.Join(homeDir, "sessions"))
}
