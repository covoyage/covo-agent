package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// --- parseCodexHooks ---

const codexFullFormat = `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "^Bash$",
        "hooks": [
          { "type": "command", "command": "echo codex-pre", "timeout": 30, "statusMessage": "checking bash" }
        ]
      },
      {
        "matcher": "*",
        "hooks": [
          { "type": "command", "command": "echo codex-all", "timeoutSec": 5 }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [
          { "type": "command", "command": "echo codex-prompt" }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          { "type": "command", "command": "echo codex-stop" }
        ]
      }
    ]
  }
}`

const codexSimpleFormat = `{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "^read$",
        "hooks": [
          { "type": "command", "command": "echo codex-post", "timeout": 10 }
        ]
      }
    ]
  }
}`

func findSpec(specs []map[string]any, command string) map[string]any {
	for _, s := range specs {
		if s["command"] == command {
			return s
		}
	}
	return nil
}

func TestParseCodexHooks_FullFormat(t *testing.T) {
	specs, err := parseCodexHooks([]byte(codexFullFormat))
	if err != nil {
		t.Fatalf("parseCodexHooks: %v", err)
	}
	if len(specs) != 4 {
		t.Fatalf("expected 4 specs, got %d", len(specs))
	}

	pre := findSpec(specs, "echo codex-pre")
	if pre == nil {
		t.Fatal("missing codex-pre spec")
	}
	if pre["event"] != "PreToolUse" || pre["matcher"] != "^Bash$" {
		t.Errorf("unexpected pre spec: %#v", pre)
	}
	if pre["timeout"].(float64) != 30 {
		t.Errorf("expected timeout 30, got %#v", pre["timeout"])
	}

	// matcher "*" means match all tools → normalized to "".
	all := findSpec(specs, "echo codex-all")
	if all == nil {
		t.Fatal("missing codex-all spec")
	}
	if all["matcher"] != "" {
		t.Errorf("expected '*' matcher normalized to empty, got %#v", all["matcher"])
	}
	// timeoutSec alias is honored.
	if all["timeout"].(float64) != 5 {
		t.Errorf("expected timeoutSec 5, got %#v", all["timeout"])
	}

	// UserPromptSubmit and Stop ignore matchers.
	prompt := findSpec(specs, "echo codex-prompt")
	if prompt == nil || prompt["matcher"] != "" {
		t.Errorf("expected UserPromptSubmit matcher dropped, got %#v", prompt)
	}
	stop := findSpec(specs, "echo codex-stop")
	if stop == nil || stop["matcher"] != "" {
		t.Errorf("expected Stop matcher dropped, got %#v", stop)
	}
}

func TestParseCodexHooks_NonCommandHandlerSkipped(t *testing.T) {
	specs, err := parseCodexHooks([]byte(`{
	  "hooks": {
	    "PreToolUse": [
	      {
	        "matcher": "Bash",
	        "hooks": [
	          { "type": "command", "command": "echo keep" },
	          { "type": "agent", "agent": "some-agent" },
	          { "type": "notification", "message": "hi" }
	        ]
	      }
	    ]
	  }
	}`))
	if err != nil {
		t.Fatalf("parseCodexHooks: %v", err)
	}
	if len(specs) != 1 || specs[0]["command"] != "echo keep" {
		t.Fatalf("expected only the command handler, got %#v", specs)
	}
}

func TestParseCodexHooks_Unsupported(t *testing.T) {
	if _, err := parseCodexHooks([]byte(`{"foo": 1}`)); err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

// --- parseHooksFile (hot reload dispatch) ---

func TestParseHooksFile_DispatchesToCodexAndClaude(t *testing.T) {
	codex, err := parseHooksFile([]byte(codexFullFormat))
	if err != nil || len(codex) != 4 {
		t.Fatalf("codex file: specs=%v err=%v", len(codex), err)
	}

	claude, err := parseHooksFile([]byte(claudeMapFormat))
	if err != nil || len(claude) != 2 {
		t.Fatalf("claude map file: specs=%v err=%v", len(claude), err)
	}

	legacy, err := parseHooksFile([]byte(claudeLegacyFormat))
	if err != nil || len(legacy) != 1 {
		t.Fatalf("legacy file: specs=%v err=%v", len(legacy), err)
	}
}

// --- LoadCodexHooks ---

func TestLoadCodexHooks_MergesUserProjectAndExtra(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	extra := t.TempDir()

	mustWrite(t, filepath.Join(home, ".codex", "hooks.json"), codexFullFormat)
	mustWrite(t, filepath.Join(proj, ".codex", "hooks.json"), codexSimpleFormat)
	mustWrite(t, filepath.Join(extra, "extra.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo extra-stop"}]}]}}`)

	t.Setenv("COVO_CODEX_HOOKS_DISABLED", "")
	t.Setenv("COVO_CODEX_HOOKS_PATH", filepath.Join(extra, "extra.json"))

	m := NewShellHookManager(t.TempDir(), true)
	if n := m.LoadCodexHooks(proj, home); n != 6 {
		t.Fatalf("expected 6 hooks (4 user + 1 project + 1 extra), got %d", n)
	}

	// Codex camelCase events land in the same normalized buckets as Claude.
	for _, ev := range []string{"PreToolUse", "PostToolUse", "UserPromptSubmit", "Stop"} {
		if !m.HasEvent(ev) {
			t.Errorf("expected hook bucket for %q", ev)
		}
	}
}

func TestLoadCodexHooks_Disabled(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	mustWrite(t, filepath.Join(home, ".codex", "hooks.json"), codexFullFormat)

	t.Setenv("COVO_CODEX_HOOKS_DISABLED", "true")
	t.Setenv("COVO_CODEX_HOOKS_PATH", "")

	m := NewShellHookManager(t.TempDir(), true)
	if n := m.LoadCodexHooks(proj, home); n != 0 {
		t.Fatalf("expected 0 hooks when disabled, got %d", n)
	}
}

func TestLoadCodexHooks_NoFiles(t *testing.T) {
	t.Setenv("COVO_CODEX_HOOKS_DISABLED", "")
	t.Setenv("COVO_CODEX_HOOKS_PATH", "")
	m := NewShellHookManager(t.TempDir(), true)
	if n := m.LoadCodexHooks(t.TempDir(), t.TempDir()); n != 0 {
		t.Fatalf("expected 0 hooks with no files, got %d", n)
	}
}

// --- bucket sharing + agent wiring ---

func TestCodexHooks_ShareBucketsWithClaude(t *testing.T) {
	// A Codex PreToolUse hook and a Claude PreToolUse hook land in the same
	// bucket and are both fired by the agent's combined pre-tool-use gate.
	proj := t.TempDir()
	mustWrite(t, filepath.Join(proj, ".codex", "hooks.json"), `{
	  "hooks": {
	    "PreToolUse": [
	      { "matcher": "^bash$", "hooks": [ { "type": "command", "command": "echo '{\"decision\":\"deny\",\"reason\":\"codex blocked\"}'" } ] }
	    ]
	  }
	}`)
	t.Setenv("COVO_CODEX_HOOKS_DISABLED", "")

	m := NewShellHookManager(t.TempDir(), true)
	m.LoadCodexHooks(proj, t.TempDir())

	if !m.HasEvent("PreToolUse") {
		t.Fatal("Codex PreToolUse hook did not register")
	}
	res := m.Invoke("PreToolUse", &HookEvent{EventName: "PreToolUse", ToolName: "bash"})
	if res == nil || !res.Blocked {
		t.Fatalf("expected Codex hook to block via shared bucket, got %#v", res)
	}
	if res.Reason != "codex blocked" {
		t.Errorf("unexpected reason: %q", res.Reason)
	}

	// And through the agent's pre-tool-use wiring.
	ca := &CovoAgent{shellHooks: m, workDir: proj}
	ov := ca.claudeHooksPreToolUse()(context.Background(), agentcore.ToolCall{Name: "bash"})
	if ov == nil || !ov.Block {
		t.Fatalf("expected blocking override from Codex hook, got %#v", ov)
	}
}

func TestCodexHooks_MatcherStarMatchesAllTools(t *testing.T) {
	m := NewShellHookManager(t.TempDir(), true)
	m.RegisterFromConfig([]map[string]any{
		{"event": "PreToolUse", "command": `echo '{"decision":"deny","reason":"all blocked"}'`, "matcher": ""},
	})
	for _, tool := range []string{"bash", "read", "write"} {
		res := m.Invoke("PreToolUse", &HookEvent{EventName: "PreToolUse", ToolName: tool})
		if res == nil || !res.Blocked {
			t.Fatalf("expected match-all hook to block %q, got %#v", tool, res)
		}
	}
}

func TestCodexHooks_PayloadIncludesModelPermissionAndSource(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "payload.json")
	m := NewShellHookManager(t.TempDir(), true)
	m.RegisterFromConfig([]map[string]any{
		{"event": "PostToolUse", "command": "cat > " + out},
	})
	t.Setenv("COVO_ACCEPT_HOOKS", "true")
	ca := &CovoAgent{shellHooks: m, workDir: dir, model: "gpt-5-codex"}

	tc := agentcore.ToolCall{Name: "read", Arguments: `{"path":"x"}`}
	got := ca.claudeHooksPostToolUse()(context.Background(), tc, &agentcore.ToolResult{Result: "ok"})
	if got == nil {
		t.Fatal("PostToolUse returned nil result")
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("hook did not receive payload: %v", err)
	}
	var ev HookEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("hook payload is not valid JSON: %v\n%s", err, data)
	}
	if ev.Model != "gpt-5-codex" {
		t.Errorf("expected Codex model field, got %q", ev.Model)
	}
	if ev.Source != "covo-agent" {
		t.Errorf("expected source covo-agent, got %q", ev.Source)
	}
	if ev.PermissionMode != "bypassPermissions" {
		t.Errorf("expected bypassPermissions with COVO_ACCEPT_HOOKS=true, got %q", ev.PermissionMode)
	}
}

func TestHookPermissionMode(t *testing.T) {
	t.Setenv("COVO_ACCEPT_HOOKS", "true")
	if got := hookPermissionMode(false); got != "bypassPermissions" {
		t.Errorf("accept-hooks on: got %q", got)
	}
	t.Setenv("COVO_ACCEPT_HOOKS", "false")
	if got := hookPermissionMode(false); got != "acceptEdits" {
		t.Errorf("default: got %q", got)
	}
	if got := hookPermissionMode(true); got != "plan" {
		t.Errorf("plan mode: got %q", got)
	}
}

// --- hot reload with mixed Claude + Codex files ---

func TestLoadCodexHooks_ExtraPathSurvivesHotReload(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	extra := t.TempDir()

	claudeHooksPath := filepath.Join(proj, ".claude", "hooks.json")
	codexHooksPath := filepath.Join(proj, ".codex", "hooks.json")
	extraPath := filepath.Join(extra, "extra.json")

	mustWrite(t, claudeHooksPath, `{"Hooks":[{"Hooks":[{"Type":"PreToolUse","Command":"echo claude-pre"}]}]}`)
	mustWrite(t, codexHooksPath, codexSimpleFormat)
	mustWrite(t, extraPath, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo extra-stop"}]}]}}`)

	t.Setenv("COVO_CLAUDE_HOOKS_DISABLED", "")
	t.Setenv("COVO_CLAUDE_HOOKS_PATH", "")
	t.Setenv("COVO_CODEX_HOOKS_DISABLED", "")
	t.Setenv("COVO_CODEX_HOOKS_PATH", extraPath)

	m := NewShellHookManager(t.TempDir(), true)
	m.LoadClaudeHooks(proj, home)
	m.LoadCodexHooks(proj, home)
	reloadPaths := append(claudeHooksPaths(proj, home), codexHooksPaths(proj, home)...)
	m.StartHotReload(proj, reloadPaths...)
	defer m.Stop()

	for _, ev := range []string{"PreToolUse", "PostToolUse", "Stop"} {
		if !m.HasEvent(ev) {
			t.Fatalf("hook for %q not loaded before reload", ev)
		}
	}

	// Touch a watched file to force a hot reload.
	time.Sleep(50 * time.Millisecond)
	mustWrite(t, codexHooksPath, `{"hooks":{"PostToolUse":[{"matcher":"^read$","hooks":[{"type":"command","command":"echo codex-post","timeout":10}]},{"hooks":[{"type":"command","command":"echo codex-post-2"}]}]}}`)

	deadline := time.Now().Add(3 * hotReloadInterval)
	for {
		m.mu.Lock()
		post := len(m.hooks["post_tool_use"])
		stop := len(m.hooks["stop"])
		pre := len(m.hooks["pre_tool_use"])
		m.mu.Unlock()
		if post == 2 && stop == 1 && pre == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("hooks lost/mishandled after hot reload: pre=%d post=%d stop=%d", pre, post, stop)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
