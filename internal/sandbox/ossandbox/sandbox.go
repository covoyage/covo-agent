package ossandbox

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
)

// SandboxState tracks the global sandbox state for the process.
type SandboxState struct {
	mu              sync.RWMutex
	profile         string
	applied         bool
	restrictNetwork bool // child processes should have network blocked
}

var globalState = &SandboxState{}

// ApplyOptions configures the sandbox application.
type ApplyOptions struct {
	Profile   ProfileName
	Workspace string
	Logger    *slog.Logger
}

// ApplyResult holds the outcome of a sandbox apply attempt.
type ApplyResult struct {
	Applied         bool
	Profile         string
	RestrictNetwork bool // child processes should have network blocked
	Message         string
}

// Apply applies the OS-level sandbox to the current process.
//
// This is IRREVERSIBLE — once applied, the restrictions cannot be removed.
// On platforms without sandbox support, it degrades gracefully (no-op).
//
// IMPORTANT: The sandbox restricts FILE SYSTEM access only. Network is always
// allowed for the agent process itself (it needs LLM API access). The
// RestrictNetwork flag indicates that CHILD processes (e.g. bash commands)
// should have their network blocked — this is enforced at the child-execution
// layer, not by the kernel sandbox.
func Apply(opts ApplyOptions) ApplyResult {
	if opts.Profile == ProfileOff {
		opts.Logger.Info("sandbox disabled (profile: off)")
		return ApplyResult{
			Profile: "off",
			Message: "sandbox disabled",
		}
	}

	config := LoadSandboxConfig(opts.Workspace)
	profile, err := ResolveProfile(opts.Profile, opts.Workspace, config)
	if err != nil {
		opts.Logger.Warn("failed to resolve sandbox profile, continuing without sandbox",
			"profile", opts.Profile, "error", err)
		return ApplyResult{
			Profile: opts.Profile.String(),
			Message: fmt.Sprintf("profile resolution failed: %v", err),
		}
	}

	// Check platform support
	supported, details := platformSupportInfo()
	if !supported {
		opts.Logger.Warn("sandbox not supported on this platform, continuing without sandbox",
			"details", details)
		return ApplyResult{
			Profile: opts.Profile.String(),
			Message: fmt.Sprintf("unsupported: %s", details),
		}
	}

	// Apply platform-specific enforcement
	err = applySandbox(profile, opts.Workspace)
	if err != nil {
		opts.Logger.Warn("sandbox could not be applied, continuing without sandbox",
			"profile", opts.Profile, "error", err)
		return ApplyResult{
			Profile: opts.Profile.String(),
			Message: fmt.Sprintf("apply failed: %v", err),
		}
	}

	globalState.mu.Lock()
	globalState.profile = opts.Profile.String()
	globalState.applied = true
	globalState.restrictNetwork = profile.RestrictNetwork
	globalState.mu.Unlock()

	opts.Logger.Info("sandbox applied (kernel-enforced, irreversible)",
		"profile", opts.Profile,
		"workspace", opts.Workspace,
		"restrict_child_network", profile.RestrictNetwork)

	return ApplyResult{
		Applied:         true,
		Profile:         opts.Profile.String(),
		RestrictNetwork: profile.RestrictNetwork,
		Message:         "sandbox active",
	}
}

// IsActive returns true if the sandbox was successfully applied to this process.
func IsActive() bool {
	globalState.mu.RLock()
	defer globalState.mu.RUnlock()
	return globalState.applied
}

// ActiveProfile returns the active sandbox profile name, or empty if not applied.
func ActiveProfile() string {
	globalState.mu.RLock()
	defer globalState.mu.RUnlock()
	if globalState.applied {
		return globalState.profile
	}
	return ""
}

// ShouldRestrictChildNetwork returns true if child processes (e.g. bash
// commands spawned by the agent) should have network access blocked.
//
// This is separate from the agent's own network access, which is always
// allowed (the agent needs to reach LLM APIs). When true, the child-process
// execution layer should apply network restrictions (e.g. via seccomp on
// Linux or sandbox-exec on macOS for the specific child command).
func ShouldRestrictChildNetwork() bool {
	globalState.mu.RLock()
	defer globalState.mu.RUnlock()
	return globalState.applied && globalState.restrictNetwork
}

// LogViolation records a sandbox violation for diagnostics.
func LogViolation(target, operation string) {
	globalState.mu.RLock()
	defer globalState.mu.RUnlock()
	if !globalState.applied {
		return
	}
	fmt.Fprintf(os.Stderr, "[sandbox] violation: %s %s (profile: %s)\n",
		operation, target, globalState.profile)
}
