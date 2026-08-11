package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/theme"

	"github.com/covoyage/covo-agent/internal/i18n"
	ossandbox "github.com/covoyage/covo-agent/internal/sandbox/ossandbox"
)

const bashModePrefix = "!"

var workingDirOverride string

func executeShellCommand(
	ctx context.Context,
	cmdStr string,
	wd string,
	app *chat.ChatApp,
	busy *atomic.Bool,
) {
	if cmdStr == "" {
		return
	}

	busy.Store(true)
	defer busy.Store(false)

	if wd == "" {
		wd = workingDirOverride
	}
	if wd == "" {
		var err error
		wd, err = os.Getwd()
		if err != nil {
			wd = "."
		}
	}

	app.PrintUser("$ " + cmdStr)

	cmdCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	shell := "sh"
	shellFlag := "-c"
	if _, err := os.Stat("/bin/bash"); err == nil {
		shell = "bash"
	} else if _, err := os.Stat("/bin/zsh"); err == nil {
		shell = "zsh"
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(cmdCtx, shell, shellFlag, cmdStr)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = wd

	// Apply child process network restriction if the OS-level sandbox requires it.
	if ossandbox.ShouldRestrictChildNetwork() {
		if err := ossandbox.ApplyChildNetworkRestriction(cmd); err != nil {
			app.PrintSystem(theme.CurrentPalette().Error.Render(
				fmt.Sprintf("network restriction error: %v", err),
			))
			return
		}
	}

	err := cmd.Run()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if cmdCtx.Err() != nil {
			app.PrintSystem(theme.CurrentPalette().Error.Render(
				i18n.T("bash.timeout"),
			))
			return
		} else {
			app.PrintSystem(theme.CurrentPalette().Error.Render(
				fmt.Sprintf("error: %v", err),
			))
			return
		}
	}

	outText := stdout.String()
	errText := stderr.String()

	pal := theme.CurrentPalette()

	var buf strings.Builder
	if outText != "" {
		buf.WriteString(strings.TrimRight(outText, "\n"))
	}
	if errText != "" {
		if outText != "" {
			buf.WriteByte('\n')
		}
		buf.WriteString(strings.TrimRight(errText, "\n"))
	}
	if buf.Len() > 0 {
		app.PrintUser(buf.String())
	}
	if exitCode != 0 {
		app.PrintUser(pal.Error.Render(fmt.Sprintf("exit code: %d", exitCode)))
	}
}
