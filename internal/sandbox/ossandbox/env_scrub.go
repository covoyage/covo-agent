package ossandbox

import (
	"os"
	"strings"
)

// NoisyMallocEnvVars are macOS malloc/debug environment variables that produce
// noisy stderr warnings when inherited by child processes.
//
// The most common culprit is MallocStackLogging. When a parent shell or
// debugger (lldb, Instruments, leaks(1)) sets it, every spawned subprocess
// inherits the variable. The child's libmalloc sees the variable, attempts to
// tear down stack logging on exit, but — because the stack logger was never
// actually initialised in the child — prints:
//
//	MallocStackLogging: can't turn off malloc stack logging because it was not enabled
//
// This floods the TUI with hundreds of identical lines (one per subprocess).
// We strip the entire family of Malloc* debug variables at startup so that
// neither the agent process nor any of its children inherit them.
//
// Developers who explicitly want malloc debugging (e.g. running under leaks(1))
// can set COVO_KEEP_MALLOC_DEBUG=1 to opt out of the scrub.
var NoisyMallocEnvVars = []string{
	"MallocStackLogging",
	"MallocStackLoggingNoHistory",
	"MallocStackLoggingDirectory",
	"MallocStackLoggingDontCallStack",
	"MallocStackLoggingLite",
	"MallocNanoZone",
	"MallocErrorAbort",
	"MallocCorruptionAbort",
	"MallocCheckHeapStart",
	"MallocCheckHeapEach",
	"MallocCheckStackLogging",
	"MallocLogFile",
	"MallocGuardEdges",
	"MallocPreGuardEdges",
	"MallocProbabilisticGuard",
	"MallocHelp",
}

// ScrubNoisyEnv removes macOS malloc/debug environment variables from the
// current process that produce noisy stderr warnings in child processes.
//
// This is safe to call on any platform: on non-Apple systems these variables
// are simply not present and os.Unsetenv is a no-op.
//
// Set COVO_KEEP_MALLOC_DEBUG=1 to preserve these variables (e.g. when running
// under leaks(1) or Instruments for memory debugging).
func ScrubNoisyEnv() {
	if keepMallocDebug() {
		return
	}
	for _, name := range NoisyMallocEnvVars {
		os.Unsetenv(name)
	}
}

// ScrubEnvSlice returns env (as from os.Environ()) with noisy macOS malloc
// debug variables removed. Unlike ScrubNoisyEnv (which mutates the live
// process environment), this operates on a slice and is suitable for
// exec.Cmd.Env construction.
func ScrubEnvSlice(env []string) []string {
	if keepMallocDebug() {
		return env
	}
	// Build a set of noisy names for O(1) lookup (case-insensitive).
	noisy := make(map[string]struct{}, len(NoisyMallocEnvVars))
	for _, n := range NoisyMallocEnvVars {
		noisy[strings.ToLower(n)] = struct{}{}
	}

	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		name := kv
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			name = kv[:idx]
		}
		if _, drop := noisy[strings.ToLower(name)]; drop {
			continue
		}
		filtered = append(filtered, kv)
	}
	return filtered
}

func keepMallocDebug() bool {
	v := os.Getenv("COVO_KEEP_MALLOC_DEBUG")
	return v == "1" || strings.EqualFold(v, "true")
}
