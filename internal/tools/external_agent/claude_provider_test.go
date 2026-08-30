package externalagent

import (
	"context"
	"strings"
	"testing"
	"time"
)

// claudeFakeScript is a minimal Claude Code CLI that speaks enough of the
// stream-json control protocol to exercise the Go provider. $VERSION is the
// --version reply; BODY is inserted into the read loop.
const claudeFakeScript = `#!/bin/sh
case "$1" in
  --version) echo "$VERSION"; exit 0 ;;
esac
while IFS= read -r line; do
  case "$line" in
    *'"subtype":"initialize"'*)
      req=$(printf '%s' "$line" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
      echo "{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"$req\",\"response\":{}}}"
      ;;
    *'"subtype":"interrupt"'*)
      exit 0
      ;;
    $BODY
  esac
done
exit 0
`

func writeClaudeFake(t *testing.T, dir, version, body string) {
	t.Helper()
	script := strings.Replace(claudeFakeScript, "$VERSION", version, 1)
	script = strings.Replace(script, "$BODY", body, 1)
	writeFakeCLI(t, dir, "claude", script)
	withPath(t, dir)
}

func TestClaudeProviderRun(t *testing.T) {
	dir := t.TempDir()
	writeClaudeFake(t, dir, "2.5.0", `
    *'"type":"user"'*)
      echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"working"}]}}'
      echo '{"type":"result","subtype":"success","result":"final-ok","session_id":"s1","total_cost_usd":0.05}'
      ;;
`)

	p := ClaudeProvider()
	out, err := p.Run(context.Background(), "do it", "")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if out != "final-ok" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestClaudeProviderPermissionDeny(t *testing.T) {
	dir := t.TempDir()
	writeClaudeFake(t, dir, "2.5.0", `
    *'"type":"user"'*)
      echo '{"type":"control_request","request_id":"perm1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"rm -rf /"}}}'
      IFS= read -r resp
      case "$resp" in
        *'"behavior":"deny"'*) echo '{"type":"result","subtype":"success","result":"denied-ok","session_id":"s1"}' ;;
        *) echo '{"type":"result","subtype":"success","result":"unexpected","session_id":"s1"}' ;;
      esac
      ;;
`)

	p := ClaudeProvider()
	out, err := p.Run(context.Background(), "do it", "")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if out != "denied-ok" {
		t.Fatalf("expected denied-ok, got %q", out)
	}
}

func TestClaudeProviderReadOnlyAllow(t *testing.T) {
	dir := t.TempDir()
	writeClaudeFake(t, dir, "2.5.0", `
    *'"type":"user"'*)
      echo '{"type":"control_request","request_id":"perm1","request":{"subtype":"can_use_tool","tool_name":"Read","input":{"file_path":"a.txt"}}}'
      IFS= read -r resp
      case "$resp" in
        *'"behavior":"allow"'*) echo '{"type":"result","subtype":"success","result":"allowed-ok","session_id":"s1"}' ;;
        *) echo '{"type":"result","subtype":"success","result":"unexpected","session_id":"s1"}' ;;
      esac
      ;;
`)

	p := ClaudeProvider()
	out, err := p.Run(context.Background(), "do it", "")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if out != "allowed-ok" {
		t.Fatalf("expected allowed-ok, got %q", out)
	}
}

func TestClaudeProviderAllowedTool(t *testing.T) {
	dir := t.TempDir()
	writeClaudeFake(t, dir, "2.5.0", `
    *'"type":"user"'*)
      echo '{"type":"control_request","request_id":"perm1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"git status"}}}'
      IFS= read -r resp
      case "$resp" in
        *'"behavior":"allow"'*) echo '{"type":"result","subtype":"success","result":"bash-allowed","session_id":"s1"}' ;;
        *) echo '{"type":"result","subtype":"success","result":"unexpected","session_id":"s1"}' ;;
      esac
      ;;
`)

	p := ClaudeProvider()
	out, err := p.RunOpt(context.Background(), "do it", "", RunOptions{
		AllowedTools: []string{"Bash"},
	})
	if err != nil {
		t.Fatalf("RunOpt failed: %v", err)
	}
	if out != "bash-allowed" {
		t.Fatalf("expected bash-allowed, got %q", out)
	}
}

func TestClaudeProviderVersionTooOld(t *testing.T) {
	dir := t.TempDir()
	writeClaudeFake(t, dir, "1.9.0", "")

	p := ClaudeProvider()
	_, err := p.Run(context.Background(), "do it", "")
	if err == nil {
		t.Fatal("expected version error")
	}
	if !strings.Contains(err.Error(), "too old") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClaudeProviderErrorResult(t *testing.T) {
	dir := t.TempDir()
	writeClaudeFake(t, dir, "2.5.0", `
    *'"type":"user"'*)
      echo '{"type":"result","subtype":"error_max_turns","result":"","is_error":true,"errors":["reached max turns"],"session_id":"s1"}'
      ;;
`)

	p := ClaudeProvider()
	_, err := p.Run(context.Background(), "do it", "")
	if err == nil {
		t.Fatal("expected error from error result")
	}
	if !strings.Contains(err.Error(), "max turns") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClaudeProviderExitsWithoutResult(t *testing.T) {
	dir := t.TempDir()
	writeClaudeFake(t, dir, "2.5.0", `
    *'"type":"user"'*)
      exit 1
      ;;
`)

	p := ClaudeProvider()
	_, err := p.Run(context.Background(), "do it", "")
	if err == nil {
		t.Fatal("expected error when CLI exits without a result")
	}
	if !strings.Contains(err.Error(), "without producing a result") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClaudeProviderInterrupt(t *testing.T) {
	dir := t.TempDir()
	writeClaudeFake(t, dir, "2.5.0", `
    *'"type":"user"'*)
      sleep 3
      IFS= read -r line2
      case "$line2" in
        *interrupt*) echo '{"type":"result","subtype":"success","result":"interrupted-gracefully","session_id":"s1"}' ;;
      esac
      ;;
`)

	p := ClaudeProvider()
	// 1.5s leaves headroom for the handshake under load while still firing
	// before the fake CLI's 3s sleep finishes.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	start := time.Now()
	out, err := p.Run(ctx, "slow task", "")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if elapsed > 8*time.Second {
		t.Fatalf("interrupt not honoured, took %v", elapsed)
	}
	if out != "interrupted-gracefully" {
		t.Fatalf("expected interrupt result, got %q", out)
	}
}

func TestAutoAllowSet_DisallowedToolsWin(t *testing.T) {
	opts := RunOptions{
		AllowedTools:    []string{"Bash", "Read"},
		DisallowedTools: []string{"Bash"},
	}

	// Disallowed always wins, even when also allowed or under bypass mode.
	if opts.autoAllowSet("Bash", nil) {
		t.Error("Bash is disallowed; expected deny despite AllowedTools entry")
	}
	opts.PermissionMode = "bypassPermissions"
	if opts.autoAllowSet("Bash", nil) {
		t.Error("Bash is disallowed; expected deny despite bypassPermissions")
	}

	// Allowed tools still pass through.
	opts.PermissionMode = "default"
	if !opts.autoAllowSet("Read", nil) {
		t.Error("Read is allowed; expected allow")
	}
	// Read-only tools are auto-allowed when not disallowed.
	if !opts.autoAllowSet("Glob", nil) {
		t.Error("Glob is read-only; expected allow")
	}
	// Unknown, non-allowed, non-readonly tools are denied.
	if opts.autoAllowSet("Write", nil) {
		t.Error("Write is not allowed; expected deny")
	}
}
