// Package externalagent bridges covo-agent to external coding agents
// (Claude Code, Codex, opencode). Instead of depending on each product's
// TypeScript/Python SDK, providers drive the product's own CLI — the same
// binary those SDKs wrap under the hood. Users own installation and
// authentication for each CLI; a provider is only usable when its binary is
// found on PATH.
package externalagent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Provider runs a standalone, self-contained task on an external coding agent.
type Provider interface {
	// Name is the canonical provider id: "claude", "codex", or "opencode".
	Name() string
	// Available reports whether the provider can run right now. The returned
	// reason explains unavailability (e.g. missing binary on PATH).
	Available() (reason string, ok bool)
	// Run executes task in cwd and returns the agent's final text output.
	// The context is honoured for cancellation and timeouts.
	Run(ctx context.Context, task, cwd string) (string, error)
	// RunOpt is Run with per-call options (permission mode, allowed tools,
	// model, max turns). Providers that do not support a given option ignore
	// it.
	RunOpt(ctx context.Context, task, cwd string, opts RunOptions) (string, error)
}

// RunOptions carries per-call knobs for external agent providers.
type RunOptions struct {
	Cwd             string
	PermissionMode  string   // claude: default | acceptEdits | plan | bypassPermissions
	AllowedTools    []string // claude: tools allowed without prompting
	DisallowedTools []string // claude: tools always denied
	MaxTurns        int      // claude: cap on agent turns (0 = no cap)
	Model           string   // claude: model override (e.g. claude-sonnet-4-5)
}

// cliProvider is the generic adapter that spawns a product CLI in
// non-interactive mode and captures its final output.
type cliProvider struct {
	name string
	args func(task string) []string
}

func (p *cliProvider) Name() string { return p.name }

func (p *cliProvider) Available() (string, bool) {
	if _, err := exec.LookPath(p.name); err != nil {
		return fmt.Sprintf("%s CLI not found on PATH (install and sign in to use this provider)", p.name), false
	}
	return "", true
}

func (p *cliProvider) Run(ctx context.Context, task, cwd string) (string, error) {
	return p.RunOpt(ctx, task, cwd, RunOptions{Cwd: cwd})
}

func (p *cliProvider) RunOpt(ctx context.Context, task, cwd string, opts RunOptions) (string, error) {
	if opts.Cwd != "" {
		cwd = opts.Cwd
	}
	path, err := exec.LookPath(p.name)
	if err != nil {
		return "", fmt.Errorf("external agent %q: %w", p.name, err)
	}
	cmd := exec.CommandContext(ctx, path, p.args(task)...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("external agent %q failed: %s (exit: %v)", p.name, truncate(msg, 2000), err)
	}
	return strings.TrimSpace(out.String()), nil
}

// truncate limits a message to keep error reports from ballooning the context.
func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
