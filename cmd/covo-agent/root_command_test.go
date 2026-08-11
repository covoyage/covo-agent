package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRootCommandRegistersLegacyCommands(t *testing.T) {
	root := newRootCommand()
	want := []string{
		"acp", "analyze", "auth", "backup", "commitments", "completion",
		"config", "cron", "doctor", "dreaming", "ext", "features", "gateway",
		"heartbeat", "language", "lsp", "mcp", "memory", "migrate", "model",
		"package", "pairing", "plugin", "pr", "profile", "restore", "review",
		"session", "setup", "skill", "status", "template", "testgen", "theme",
		"update", "version", "worktree",
	}

	for _, name := range want {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatalf("find command %q: %v", name, err)
		}
		if cmd == root || cmd.Name() != name {
			t.Errorf("command %q was not registered", name)
		}
	}
}

func TestRootCommandLanguageAlias(t *testing.T) {
	root := newRootCommand()
	cmd, _, err := root.Find([]string{"lang"})
	if err != nil {
		t.Fatalf("find lang alias: %v", err)
	}
	if cmd.Name() != "language" {
		t.Fatalf("lang resolved to %q, want language", cmd.Name())
	}
}

func TestRootCommandParsesOneshotShorthand(t *testing.T) {
	root := newRootCommand()
	if err := root.ParseFlags([]string{"-z", "summarize this"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	got, err := root.Flags().GetString("oneshot")
	if err != nil {
		t.Fatalf("read oneshot flag: %v", err)
	}
	if got != "summarize this" {
		t.Fatalf("oneshot = %q, want %q", got, "summarize this")
	}
}

func TestRootCommandHelpUsesCobraCommandTree(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"--help"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute help: %v", err)
	}
	help := output.String()
	for _, text := range []string{"Available Commands:", "completion", "gateway", "--oneshot"} {
		if !strings.Contains(help, text) {
			t.Errorf("help output missing %q", text)
		}
	}
}

func TestCompletionSupportsAllCobraShells(t *testing.T) {
	root := newRootCommand()
	cmd, _, err := root.Find([]string{"completion"})
	if err != nil {
		t.Fatalf("find completion: %v", err)
	}
	want := []string{"bash", "zsh", "fish", "powershell"}
	if strings.Join(cmd.ValidArgs, ",") != strings.Join(want, ",") {
		t.Fatalf("completion shells = %v, want %v", cmd.ValidArgs, want)
	}
}

func TestNoBuiltinCommandUsesDisableFlagParsing(t *testing.T) {
	root := newRootCommand()
	builtins := map[string]struct{}{
		"acp": {}, "analyze": {}, "auth": {}, "backup": {}, "commitments": {}, "completion": {},
		"config": {}, "cron": {}, "doctor": {}, "dreaming": {}, "ext": {}, "features": {}, "gateway": {},
		"heartbeat": {}, "language": {}, "lsp": {}, "mcp": {}, "memory": {}, "migrate": {}, "model": {},
		"package": {}, "pairing": {}, "plugin": {}, "pr": {}, "profile": {}, "restore": {}, "review": {},
		"session": {}, "setup": {}, "skill": {}, "status": {}, "template": {}, "testgen": {}, "theme": {},
		"update": {}, "version": {}, "worktree": {},
	}
	for _, c := range root.Commands() {
		if _, ok := builtins[c.Name()]; !ok {
			continue
		}
		if c.DisableFlagParsing {
			t.Fatalf("builtin command %q has DisableFlagParsing enabled", c.CommandPath())
		}
		for _, sc := range c.Commands() {
			if sc.DisableFlagParsing {
				t.Fatalf("builtin command %q has DisableFlagParsing enabled", sc.CommandPath())
			}
			for _, ssc := range sc.Commands() {
				if ssc.DisableFlagParsing {
					t.Fatalf("builtin command %q has DisableFlagParsing enabled", ssc.CommandPath())
				}
			}
		}
	}
}

func TestExpectedAliasesAndTrees(t *testing.T) {
	root := newRootCommand()

	assertFind := func(args ...string) *cobra.Command {
		t.Helper()
		cmd, _, err := root.Find(args)
		if err != nil {
			t.Fatalf("find %v: %v", args, err)
		}
		if cmd == root {
			t.Fatalf("find %v resolved to root", args)
		}
		return cmd
	}

	if got := assertFind("lang").Name(); got != "language" {
		t.Fatalf("lang alias resolved to %q", got)
	}
	if got := assertFind("template", "ls").Name(); got != "list" {
		t.Fatalf("template ls resolved to %q", got)
	}
	if got := assertFind("template", "rm", "x").Name(); got != "remove" {
		t.Fatalf("template rm resolved to %q", got)
	}
	if got := assertFind("package", "i", "x").Name(); got != "install" {
		t.Fatalf("package i resolved to %q", got)
	}
	if got := assertFind("package", "ls").Name(); got != "list" {
		t.Fatalf("package ls resolved to %q", got)
	}
	if got := assertFind("package", "rm", "x").Name(); got != "remove" {
		t.Fatalf("package rm resolved to %q", got)
	}

	for _, path := range [][]string{
		{"mcp", "test"}, {"mcp", "add"}, {"mcp", "list"}, {"mcp", "remove"}, {"mcp", "serve"},
		{"cron", "list"}, {"cron", "add"}, {"cron", "remove"}, {"cron", "pause"}, {"cron", "resume"},
		{"skill", "list"}, {"skill", "search"}, {"skill", "info"}, {"skill", "disable"}, {"skill", "enable"},
		{"skill", "config"}, {"skill", "hub", "list"}, {"skill", "hub", "search"}, {"skill", "hub", "install"},
		{"memory", "providers"}, {"memory", "ping"}, {"memory", "migrate"},
		{"pr", "create"}, {"pr", "review"},
		{"auth", "list"}, {"auth", "add"}, {"auth", "remove"}, {"auth", "login"},
		{"pairing", "approve"}, {"pairing", "list"}, {"pairing", "revoke"},
		{"lsp", "status"}, {"lsp", "install"},
		{"profile", "list"}, {"profile", "use"}, {"profile", "create"}, {"profile", "delete"}, {"profile", "info"},
		{"theme", "show"}, {"theme", "list"}, {"theme", "set"},
		{"ext", "list"}, {"ext", "info"}, {"ext", "run"}, {"ext", "reload"},
		{"features", "list"}, {"features", "enable"}, {"features", "disable"}, {"features", "promote"},
		{"commitments", "list"}, {"commitments", "dismiss"}, {"commitments", "complete"},
		{"worktree", "list"}, {"worktree", "prune"}, {"worktree", "gc"},
		{"dreaming", "run"}, {"dreaming", "diary"}, {"dreaming", "status"}, {"dreaming", "enable"}, {"dreaming", "disable"}, {"dreaming", "config"},
		{"migrate", "memory"},
	} {
		cmd := assertFind(path...)
		if cmd == nil {
			t.Fatalf("missing command path: %v", path)
		}
	}
}

func TestRepresentativeFlagParsing(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "doctor fix", args: []string{"doctor", "--fix"}},
		{name: "skill list flags", args: []string{"skill", "list", "--all", "--platform", "--tier", "pro"}},
		{name: "memory migrate clear", args: []string{"memory", "migrate", "--clear", "file", "sqlite"}},
		{name: "lsp status json", args: []string{"lsp", "status", "--json"}},
		{name: "review apply", args: []string{"review", "--apply", "main"}},
		{name: "pr create publish", args: []string{"pr", "create", "--publish"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := newRootCommand()
			cmd, remain, err := root.Find(tc.args)
			if err != nil {
				t.Fatalf("find %v: %v", tc.args, err)
			}
			if cmd == nil || cmd == root {
				t.Fatalf("find %v resolved to root", tc.args)
			}
			if err := cmd.ParseFlags(remain); err != nil {
				t.Fatalf("parse flags %v on %s: %v", remain, cmd.Name(), err)
			}
			if cmd.Args != nil {
				if err := cmd.Args(cmd, cmd.Flags().Args()); err != nil {
					t.Fatalf("arg validation for %v failed: %v", tc.args, err)
				}
			}
		})
	}
}

func TestMCPCommandsPreserveDashPrefixedProcessArgs(t *testing.T) {
	for _, args := range [][]string{
		{"mcp", "test", "npx", "-y", "@modelcontextprotocol/server-filesystem"},
		{"mcp", "add", "filesystem", "npx", "-y", "@modelcontextprotocol/server-filesystem"},
	} {
		root := newRootCommand()
		cmd, remain, err := root.Find(args)
		if err != nil {
			t.Fatalf("find %v: %v", args, err)
		}
		if err := cmd.ParseFlags(remain); err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
		got := cmd.Flags().Args()
		if !strings.Contains(strings.Join(got, " "), "-y") {
			t.Fatalf("dash-prefixed process arg was lost: got %v", got)
		}
	}
}
