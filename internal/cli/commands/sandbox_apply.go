package commands

import (
	"fmt"
	"github.com/covoyage/covo-agent/internal/cli"
	"log/slog"
	"os"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/sandbox/ossandbox"
)

// ApplySandboxIfRequested applies the OS-level sandbox for the given --sandbox
// flag value, or the COVO_SANDBOX environment variable when the flag is empty.
// This must be called very early in process startup, before any file operations.
func ApplySandboxIfRequested(sandboxProfile string, runtime *cli.CommandRuntime) {
	profileName := sandboxProfile
	if profileName == "" {
		// Check environment variable
		profileName = os.Getenv("COVO_SANDBOX")
	}
	if profileName == "" {
		return // sandbox not requested
	}

	profile := ossandbox.ParseProfileName(profileName)

	// Create a minimal logger that writes to stderr
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logutil.ResolveLevel(slog.LevelInfo),
	}))

	workspace, err := os.Getwd()
	if err != nil {
		workspace = runtime.HomeDir
	}

	// Warn about profile conflicts between global and project config
	if conflicts := ossandbox.ProfileConflicts(workspace); len(conflicts) > 0 {
		for _, c := range conflicts {
			fmt.Fprintf(os.Stderr, "[sandbox] warning: profile '%s' differs between global and project config; global config takes precedence\n", c)
		}
	}

	result := ossandbox.Apply(ossandbox.ApplyOptions{
		Profile:   profile,
		Workspace: workspace,
		Logger:    logger,
	})

	if result.Applied {
		os.Stderr.WriteString("[sandbox] active: profile=" + result.Profile + "\n")
		if result.RestrictNetwork {
			details := ossandbox.NetworkRestrictionDetails()
			fmt.Fprintf(os.Stderr, "[sandbox] child process network will be restricted (%s)\n", details)
		}
	} else if result.Message != "" && profile != ossandbox.ProfileOff {
		os.Stderr.WriteString("[sandbox] " + result.Message + "\n")
	}
}
