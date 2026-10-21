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

// turnStartedMarker returns the path a fake CLI touches once it has received
// the task. Tests that cancel a run mid-flight wait for this marker instead
// of racing a wall-clock deadline: process start-up latency under load is
// unbounded, so a fixed timeout makes the test flaky.
func turnStartedMarker(t *testing.T, dir string) string {
	t.Helper()
	return filepath.Join(dir, "turn-started")
}

// waitForFile blocks until path exists, failing the test if it does not
// appear within timeout.
func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, path)
}

func writeFakeCLI(t *testing.T, dir, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		// The fake CLIs are POSIX shell scripts (case/while/sed/printf).
		// cmd.exe cannot run them, and rewriting them as batch files is not
		// worth the churn: the providers' protocol logic is platform-neutral
		// and is fully exercised by the Linux/macOS CI jobs.
		t.Skip("fake CLI is a POSIX shell script; not runnable on Windows")
	}
	path := filepath.Join(dir, name)
	body = "#!/bin/sh\n" + body
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// providerCLINames are the external agent CLIs whose presence on the host
// PATH would make provider selection non-deterministic.
var providerCLINames = []string{"claude", "codex", "opencode"}

// withPath puts dir first on PATH so the fake CLI shadows any real one, and
// strips the host directories that provide the other provider CLIs.
//
// The stripping matters: Registry.Default() walks the provider names in
// alphabetical order and returns the first available one, so a developer
// machine that has Claude Code (or Codex, or opencode) installed would make
// Default() pick a different provider than the test expects. The remaining
// PATH entries are preserved because the fake CLIs are POSIX shell scripts
// that shell out to sed/printf.
func withPath(t *testing.T, dir string) {
	t.Helper()
	sep := string(os.PathListSeparator)
	t.Setenv("PATH", dir+sep+hostPathWithoutProviderCLIs(t, dir))
}

// hostPathWithoutProviderCLIs returns the host PATH with every entry that
// provides one of the provider CLIs removed, except dir itself.
func hostPathWithoutProviderCLIs(t *testing.T, dir string) string {
	t.Helper()
	sep := string(os.PathListSeparator)
	fake, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	kept := make([]string, 0, 8)
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry == "" {
			continue
		}
		abs, err := filepath.Abs(entry)
		if err != nil {
			abs = entry
		}
		if abs == fake || providesProviderCLI(entry) {
			continue
		}
		kept = append(kept, entry)
	}
	return strings.Join(kept, sep)
}

// providesProviderCLI reports whether dir contains one of the provider CLIs.
func providesProviderCLI(dir string) bool {
	for _, name := range providerCLINames {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

func TestCLIProviderRun(t *testing.T) {
	dir := t.TempDir()
	writeFakeCLI(t, dir, "opencode", `echo "got:$@"`)
	withPath(t, dir)

	p := OpenCodeProvider()
	if _, ok := p.Available(); !ok {
		t.Fatal("opencode should be available with fake CLI on PATH")
	}
	out, err := p.Run(context.Background(), "summarize this repo", t.TempDir())
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
