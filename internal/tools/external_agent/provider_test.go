package externalagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeFakeCLI(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		path += ".bat"
		body = "@echo off\r\n" + body
	} else {
		body = "#!/bin/sh\n" + body
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func withPath(t *testing.T, dir string) {
	t.Helper()
	sep := string(os.PathListSeparator)
	t.Setenv("PATH", dir+sep+os.Getenv("PATH"))
}

func TestCLIProviderRun(t *testing.T) {
	dir := t.TempDir()
	writeFakeCLI(t, dir, "opencode", `echo "got:$@"`)
	withPath(t, dir)

	p := OpenCodeProvider()
	if _, ok := p.Available(); !ok {
		t.Fatal("opencode should be available with fake CLI on PATH")
	}
	out, err := p.Run(context.Background(), "summarize this repo", "/tmp")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !strings.Contains(out, "got:run summarize this repo") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestCLIProviderRunCwd(t *testing.T) {
	dir := t.TempDir()
	writeFakeCLI(t, dir, "opencode", `pwd`)
	withPath(t, dir)

	workdir := t.TempDir()
	p := OpenCodeProvider()
	out, err := p.Run(context.Background(), "hi", workdir)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if out != workdir {
		t.Fatalf("expected cwd %q, got %q", workdir, out)
	}
}

func TestCLIProviderRunError(t *testing.T) {
	dir := t.TempDir()
	writeFakeCLI(t, dir, "opencode", `echo "boom" >&2; exit 3`)
	withPath(t, dir)

	p := OpenCodeProvider()
	_, err := p.Run(context.Background(), "task", "")
	if err == nil {
		t.Fatal("expected error from failing CLI")
	}
	if !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCLIProviderMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	p := ClaudeProvider()
	if reason, ok := p.Available(); ok {
		t.Fatalf("expected unavailable, got ok with reason %q", reason)
	}
	if _, err := p.Run(context.Background(), "x", ""); err == nil {
		t.Fatal("expected error when binary missing")
	}
}

func TestRegistryConfigure(t *testing.T) {
	cases := []struct {
		raw      string
		wantAny  bool
		wantName string // expected provider name if any
	}{
		{"", true, "claude"},
		{"all", true, "claude"},
		{"claude", true, "claude"},
		{"codex,opencode", true, "codex"},
		{"off", false, ""},
		{"none", false, ""},
		{"bogus", false, ""},
	}
	for _, c := range cases {
		r := NewRegistry("/work", c.raw)
		if r.AnyEnabled() != c.wantAny {
			t.Errorf("raw=%q AnyEnabled=%v, want %v", c.raw, r.AnyEnabled(), c.wantAny)
		}
		if c.wantName != "" {
			if _, ok := r.Get(c.wantName); !ok {
				t.Errorf("raw=%q: provider %q not registered", c.raw, c.wantName)
			}
		}
	}
}

func TestToolFuncAutoProvider(t *testing.T) {
	dir := t.TempDir()
	writeCodexFake(t, dir, "0.147.0", `
      echo '{"method":"item/completed","params":{"threadId":"thr_1","turnId":"turn_1","item":{"type":"agentMessage","id":"a1","text":"auto-ok","phase":"final_answer"}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thr_1","turn":{"id":"turn_1","status":"completed"}}}'
`)

	workdir := t.TempDir()
	r := NewRegistry(workdir, "all")
	tool := BuildExternalAgentTool(r)

	args := json.RawMessage(`{"task":"do the thing"}`)
	res, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("Func failed: %v", err)
	}
	m := res.(map[string]any)
	if m["provider"] != "codex" {
		t.Fatalf("expected codex provider, got %v", m["provider"])
	}
	if m["output"] != "auto-ok" {
		t.Fatalf("unexpected output: %v", m["output"])
	}
}

func TestToolFuncExplicitProviderDisabled(t *testing.T) {
	workdir := t.TempDir()
	r := NewRegistry(workdir, "claude")
	tool := BuildExternalAgentTool(r)

	args := json.RawMessage(`{"task":"x","provider":"codex"}`)
	_, err := tool.Func(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for disabled provider")
	}
	if !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestToolFuncNoProvidersEnabled(t *testing.T) {
	workdir := t.TempDir()
	r := NewRegistry(workdir, "off")
	tool := BuildExternalAgentTool(r)

	args := json.RawMessage(`{"task":"x"}`)
	_, err := tool.Func(context.Background(), args)
	if err == nil {
		t.Fatal("expected error when nothing enabled")
	}
}

func TestToolFuncTimeout(t *testing.T) {
	dir := t.TempDir()
	writeCodexFake(t, dir, "0.147.0", "")

	r := NewRegistry("/work", "all")
	tool := BuildExternalAgentTool(r)

	args := json.RawMessage(`{"task":"slow task","timeout_seconds":1}`)
	start := time.Now()
	res, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("Func failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout not honoured, took %v", elapsed)
	}
	m := res.(map[string]any)
	if _, ok := m["error"]; !ok {
		t.Fatal("expected timeout error")
	}
}
