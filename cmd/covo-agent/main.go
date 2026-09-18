package main

import (
	"fmt"
	"os"

	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/crash"
	"github.com/covoyage/covo-agent/internal/logutil"
)

func main() {
	if h := newCrashHandler(); h != nil {
		defer h.RecoverAndReport()
	}
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// newCrashHandler builds the system-level crash handler so unrecovered panics
// in the main goroutine are written to ~/.covo-agent/crash-reports/ before the
// process exits. Returns nil if the home directory cannot be resolved.
func newCrashHandler() *crash.Handler {
	home, err := cli.HomeDir()
	if err != nil {
		return nil
	}
	h := crash.New(home)
	h.SetLogFile(logutil.AppLogPath(home))
	return h
}
