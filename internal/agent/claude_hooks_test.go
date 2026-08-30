package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// requirePOSIXShell skips the test on Windows, where the hook command relies
// on POSIX shell utilities (touch, cat) that are not on the default PATH. The
// hook payload/execution protocol is platform-neutral and is fully covered by
// the Linux/macOS jobs.
func requirePOSIXShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook command requires POSIX shell utilities (touch/cat), unavailable on Windows")
	}
}

// --- normalizeEvent ---

func TestNormalizeEvent(t *testing.T) {
	cases := map[string]string{
		"PreToolUse":       "pre_tool_use",
		"PostToolUse":      "post_tool_use",
		"UserPromptSubmit": "user_prompt_submit",
		"SessionStart":     "session_start",
		"Stop":             "stop",
		"PreCompact":       "pre_compact",
		"SubagentStop":     "subagent_stop",
		"stop":             "stop",
		"pre_tool":         "pre_tool",
		"pre_tool_call":    "pre_tool_call",
		"":                 "",
	}
	for in, want := range cases {
		if got := normalizeEvent(in); got != want {
			t.Errorf("normalizeEvent(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- parseClaudeHooks ---

const claudeArrayFormat = `{
  "Hooks": [
    {
      "Matcher": "Bash",
      "Hooks": [
        { "Type": "PreToolUse", "Command": "echo block-bash", "TimeoutSeconds": 30 }
      ]
    },
    {
      "Hooks": [
        { "Type": "UserPromptSubmit", "Command": "echo approve", "Async": true }
      ]
    }
  ]
}`

const claudeMapFormat = `{
  "hooks": {
    "PostToolUse": [
      { "Matcher": "read", "Command": "echo post-read", "TimeoutSeconds": 5 }
    ],
    "Stop": [
      { "Command": "echo stop-check" }
    ]
  }
}`

const claudeLegacyFormat = `{
  "hooks": [
    { "event": "stop", "command": "echo legacy-stop" }
  ]
}`

func TestParseClaudeHooks_ArrayFormat(t *testing.T) {
	specs, err := parseClaudeHooks([]byte(claudeArrayFormat))
	if err != nil {
		t.Fatalf("parseClaudeHooks: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}
	pre := specs[0]
	if pre["event"] != "PreToolUse" || pre["command"] != "echo block-bash" || pre["matcher"] != "Bash" {
		t.Errorf("unexpected pre spec: %#v", pre)
	}
	if _, ok := pre["timeout"].(float64); !ok || pre["timeout"].(float64) != 30 {
		t.Errorf("expected timeout 30, got %#v", pre["timeout"])
	}
	prompt := specs[1]
	if prompt["event"] != "UserPromptSubmit" || prompt["async"] != true {
		t.Errorf("unexpected prompt spec: %#v", prompt)
	}
}

func TestParseClaudeHooks_MapFormat(t *testing.T) {
	specs, err := parseClaudeHooks([]byte(claudeMapFormat))
	if err != nil {
		t.Fatalf("parseClaudeHooks: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}
	byEvent := map[string]map[string]any{}
	for _, s := range specs {
		byEvent[s["event"].(string)] = s
	}
	if s, ok := byEvent["PostToolUse"]; !ok || s["matcher"] != "read" {
		t.Errorf("expected PostToolUse with matcher read, got %#v", byEvent)
	}
	if s, ok := byEvent["Stop"]; !ok || s["command"] != "echo stop-check" {
		t.Errorf("expected Stop hook, got %#v", byEvent)
	}
}

func TestParseClaudeHooks_LegacyFormat(t *testing.T) {
	specs, err := parseClaudeHooks([]byte(claudeLegacyFormat))
	if err != nil {
		t.Fatalf("parseClaudeHooks: %v", err)
	}
	if len(specs) != 1 || specs[0]["event"] != "stop" {
		t.Fatalf("unexpected specs: %#v", specs)
	}
}

func TestParseClaudeHooks_Unsupported(t *testing.T) {
	if _, err := parseClaudeHooks([]byte(`{"foo": 1}`)); err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

// --- LoadClaudeHooks ---

func TestLoadClaudeHooks_MergesUserProjectAndExtra(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	extra := t.TempDir()

	mustWrite(t, filepath.Join(home, ".claude", "hooks.json"), claudeMapFormat)
	mustWrite(t, filepath.Join(proj, ".claude", "hooks.json"), claudeArrayFormat)
	mustWrite(t, filepath.Join(extra, "extra.json"), claudeLegacyFormat)

	t.Setenv("COVO_CLAUDE_HOOKS_DISABLED", "")
	t.Setenv("COVO_CLAUDE_HOOKS_PATH", filepath.Join(extra, "extra.json"))

	m := NewShellHookManager(t.TempDir(), true)
	if n := m.LoadClaudeHooks(proj, home); n != 5 {
		t.Fatalf("expected 5 hooks loaded (2 user + 2 project + 1 extra), got %d", n)
	}

	// camelCase events from the Claude files land in normalized buckets.
	for _, ev := range []string{"PostToolUse", "Stop", "PreToolUse", "UserPromptSubmit", "stop"} {
		if !m.HasEvent(ev) {
			t.Errorf("expected hook bucket for %q", ev)
		}
	}
}

func TestLoadClaudeHooks_Disabled(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	mustWrite(t, filepath.Join(home, ".claude", "hooks.json"), claudeArrayFormat)

	t.Setenv("COVO_CLAUDE_HOOKS_DISABLED", "true")
	t.Setenv("COVO_CLAUDE_HOOKS_PATH", "")

	m := NewShellHookManager(t.TempDir(), true)
	if n := m.LoadClaudeHooks(proj, home); n != 0 {
		t.Fatalf("expected 0 hooks when disabled, got %d", n)
	}
}

func TestLoadClaudeHooks_NoFiles(t *testing.T) {
	t.Setenv("COVO_CLAUDE_HOOKS_DISABLED", "")
	t.Setenv("COVO_CLAUDE_HOOKS_PATH", "")
	m := NewShellHookManager(t.TempDir(), true)
	if n := m.LoadClaudeHooks(t.TempDir(), t.TempDir()); n != 0 {
		t.Fatalf("expected 0 hooks with no files, got %d", n)
	}
}

// --- Invoke semantics ---

func TestInvoke_DenyBlocks(t *testing.T) {
	m := NewShellHookManager(t.TempDir(), true)
	m.RegisterFromConfig([]map[string]any{
		{"event": "PreToolUse", "command": `echo '{"decision":"deny","reason":"no bash"}'`, "matcher": "Bash"},
	})

	res := m.Invoke("PreToolUse", &HookEvent{EventName: "PreToolUse", ToolName: "Bash"})
	if res == nil || !res.Blocked {
		t.Fatalf("expected blocked result, got %#v", res)
	}
	if res.Reason != "no bash" {
		t.Errorf("expected reason 'no bash', got %q", res.Reason)
	}
}

func TestInvoke_ApproveDoesNotBlock(t *testing.T) {
	m := NewShellHookManager(t.TempDir(), true)
	m.RegisterFromConfig([]map[string]any{
		{"event": "PreToolUse", "command": `echo '{"decision":"approve"}'`},
	})
	res := m.Invoke("PreToolUse", &HookEvent{EventName: "PreToolUse", ToolName: "Bash"})
	if res == nil || res.Blocked {
		t.Fatalf("expected non-blocking result, got %#v", res)
	}
}

func TestInvoke_AsyncDoesNotBlock(t *testing.T) {
	m := NewShellHookManager(t.TempDir(), true)
	m.RegisterFromConfig([]map[string]any{
		{"event": "SessionStart", "command": "sleep 2 && echo '{\"decision\":\"deny\"}'", "async": true},
	})

	start := time.Now()
	res := m.Invoke("SessionStart", &HookEvent{EventName: "SessionStart"})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("async hook blocked: took %v", elapsed)
	}
	if res != nil {
		t.Fatalf("expected nil result for async hook, got %#v", res)
	}
}

// --- agent wiring ---

func TestClaudeHooksPreToolUse_Deny(t *testing.T) {
	dir := t.TempDir()
	m := NewShellHookManager(t.TempDir(), true)
	m.RegisterFromConfig([]map[string]any{
		{"event": "PreToolUse", "command": `echo '{"decision":"deny","reason":"no shell"}'`, "matcher": "bash"},
	})
	ca := &CovoAgent{shellHooks: m, workDir: dir}

	ov := ca.claudeHooksPreToolUse()(context.Background(), agentcore.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"ls"}`,
	})
	if ov == nil || !ov.Block {
		t.Fatalf("expected blocking override, got %#v", ov)
	}
	if ov.Result == "" || ov.IsError != true {
		t.Errorf("expected error result with reason, got %#v", ov)
	}
}

func TestClaudeHooksPreToolUse_Approve(t *testing.T) {
	dir := t.TempDir()
	m := NewShellHookManager(t.TempDir(), true)
	m.RegisterFromConfig([]map[string]any{
		{"event": "PreToolUse", "command": `echo '{"decision":"approve"}'`},
	})
	ca := &CovoAgent{shellHooks: m, workDir: dir}

	ov := ca.claudeHooksPreToolUse()(context.Background(), agentcore.ToolCall{Name: "bash"})
	if ov != nil {
		t.Fatalf("expected nil override, got %#v", ov)
	}
}

func TestClaudeHooksPreToolUse_NoHooks(t *testing.T) {
	ca := &CovoAgent{shellHooks: NewShellHookManager(t.TempDir(), true)}
	if ov := ca.claudeHooksPreToolUse()(context.Background(), agentcore.ToolCall{Name: "bash"}); ov != nil {
		t.Fatalf("expected nil override without hooks, got %#v", ov)
	}
}

func TestClaudeHooksPostToolUse_ExecutesAndKeepsResult(t *testing.T) {
	requirePOSIXShell(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "post-called")
	m := NewShellHookManager(t.TempDir(), true)
	m.RegisterFromConfig([]map[string]any{
		{"event": "PostToolUse", "command": "touch " + marker},
	})
	ca := &CovoAgent{shellHooks: m, workDir: dir}

	tc := agentcore.ToolCall{Name: "read", Arguments: `{"path":"x"}`}
	res := &agentcore.ToolResult{Result: "file contents"}

	got := ca.claudeHooksPostToolUse()(context.Background(), tc, res)
	if got != res {
		t.Fatalf("PostToolUse changed the result")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("PostToolUse hook did not run: %v", err)
	}
}

func TestClaudeHooksPostToolUse_PayloadIncludesToolResponse(t *testing.T) {
	requirePOSIXShell(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "payload.json")
	m := NewShellHookManager(t.TempDir(), true)
	m.RegisterFromConfig([]map[string]any{
		{"event": "PostToolUse", "command": "cat > " + out},
	})
	ca := &CovoAgent{shellHooks: m, workDir: dir}

	tc := agentcore.ToolCall{Name: "bash", Arguments: `{"command":"ls"}`}
	res := &agentcore.ToolResult{Result: "line1\nline2"}
	got := ca.claudeHooksPostToolUse()(context.Background(), tc, res)
	if got != res {
		t.Fatalf("PostToolUse changed the result")
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("hook did not receive payload: %v", err)
	}
	var ev HookEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("hook payload is not valid JSON: %v\n%s", err, data)
	}
	if ev.EventName != "PostToolUse" || ev.ToolName != "bash" || ev.ToolResponse != "line1\nline2" {
		t.Errorf("unexpected hook payload: %#v", ev)
	}
	if ev.ToolInput == nil || ev.ToolInput["command"] != "ls" {
		t.Errorf("expected tool_input parsed, got %#v", ev.ToolInput)
	}
}

func TestClaudeHooksStop_ReachableViaStopGateBucket(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	// A Claude Code Stop hook is registered in the "stop" bucket, which is
	// exactly the event the stop gate invokes (see stop_gate.go).
	mustWrite(t, filepath.Join(proj, ".claude", "hooks.json"), `{"Hooks":[{"Hooks":[{"Type":"Stop","Command":"echo '{\"decision\":\"deny\",\"reason\":\"tests failing\"}'"}]}]}`)
	t.Setenv("COVO_CLAUDE_HOOKS_DISABLED", "")

	m := NewShellHookManager(t.TempDir(), true)
	m.LoadClaudeHooks(proj, home)

	if !m.HasEvent("stop") {
		t.Fatal("Stop hook did not register in the stop bucket")
	}
	res := m.Invoke("stop", &HookEvent{EventName: "stop", Cwd: proj})
	if res == nil || !res.Blocked {
		t.Fatalf("expected stop gate to trigger Claude Stop hook, got %#v", res)
	}
	if res.Reason != "tests failing" {
		t.Errorf("unexpected reason: %q", res.Reason)
	}
}

func TestClaudeHooksUserPromptSubmit_BlocksRunInput(t *testing.T) {
	m := NewShellHookManager(t.TempDir(), true)
	m.RegisterFromConfig([]map[string]any{
		{"event": "UserPromptSubmit", "command": `echo '{"decision":"deny","reason":"maintenance window"}'`},
	})
	ca := &CovoAgent{shellHooks: m, workDir: t.TempDir()}

	res := ca.shellHooks.Invoke("UserPromptSubmit", &HookEvent{
		EventName: "UserPromptSubmit",
		Prompt:    "run the deploy",
		SessionID: "",
		Cwd:       ca.workDir,
	})
	if res == nil || !res.Blocked {
		t.Fatalf("expected blocked, got %#v", res)
	}
	if res.Reason != "maintenance window" {
		t.Errorf("unexpected reason: %q", res.Reason)
	}
}

func TestClaudeHooksCheckUserPrompt_NoHooks(t *testing.T) {
	ca := &CovoAgent{shellHooks: NewShellHookManager(t.TempDir(), true)}
	if err := ca.claudeHooksCheckUserPrompt("anything"); err != nil {
		t.Fatalf("expected nil error without hooks, got %v", err)
	}
}

// --- matcher case-insensitivity ---

func TestMatchesTool_CaseInsensitive(t *testing.T) {
	m := NewShellHookManager(t.TempDir(), true)
	m.RegisterFromConfig([]map[string]any{
		{"event": "PreToolUse", "command": `echo '{"decision":"deny","reason":"no bash"}'`, "matcher": "Bash"},
	})

	// covo-agent's tool names are lowercase ("bash") but Claude Code matchers
	// are written capitalized ("Bash") — both must match.
	res := m.Invoke("PreToolUse", &HookEvent{EventName: "PreToolUse", ToolName: "bash"})
	if res == nil || !res.Blocked {
		t.Fatalf("expected capitalized matcher to match lowercase tool, got %#v", res)
	}

	// And the other direction.
	m2 := NewShellHookManager(t.TempDir(), true)
	m2.RegisterFromConfig([]map[string]any{
		{"event": "PreToolUse", "command": `echo '{"decision":"deny","reason":"no bash"}'`, "matcher": "bash"},
	})
	res2 := m2.Invoke("PreToolUse", &HookEvent{EventName: "PreToolUse", ToolName: "Bash"})
	if res2 == nil || !res2.Blocked {
		t.Fatalf("expected lowercase matcher to match capitalized tool, got %#v", res2)
	}
}

func TestMatchesTool_RegexpCaseInsensitive(t *testing.T) {
	m := NewShellHookManager(t.TempDir(), true)
	m.RegisterFromConfig([]map[string]any{
		{"event": "PreToolUse", "command": `echo '{"decision":"deny","reason":"no"}'`, "matcher": "^bash$"},
	})
	res := m.Invoke("PreToolUse", &HookEvent{EventName: "PreToolUse", ToolName: "Bash"})
	if res == nil || !res.Blocked {
		t.Fatalf("expected regexp matcher to match case-insensitively, got %#v", res)
	}
}

func TestMatchesTool_NonMatching(t *testing.T) {
	m := NewShellHookManager(t.TempDir(), true)
	m.RegisterFromConfig([]map[string]any{
		{"event": "PreToolUse", "command": `echo '{"decision":"deny","reason":"no"}'`, "matcher": "read"},
	})
	res := m.Invoke("PreToolUse", &HookEvent{EventName: "PreToolUse", ToolName: "bash"})
	if res != nil {
		t.Fatalf("expected no match for unrelated tool, got %#v", res)
	}
}

func TestLoadClaudeHooks_ExtraPathSurvivesHotReload(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	extra := t.TempDir()
	hooksPath := filepath.Join(proj, ".claude", "hooks.json")
	extraPath := filepath.Join(extra, "extra.json")

	mustWrite(t, hooksPath, `{"Hooks":[{"Hooks":[{"Type":"PreToolUse","Command":"echo a"}]}]}`)
	mustWrite(t, extraPath, `{"Hooks":[{"Hooks":[{"Type":"PostToolUse","Command":"echo b"}]}]}`)

	t.Setenv("COVO_CLAUDE_HOOKS_DISABLED", "")
	t.Setenv("COVO_CLAUDE_HOOKS_PATH", extraPath)

	m := NewShellHookManager(t.TempDir(), true)
	m.LoadClaudeHooks(proj, home)
	// Watch exactly what LoadClaudeHooks loaded (incl. the extra path).
	m.StartHotReload(proj, claudeHooksPaths(proj, home)...)
	defer m.Stop()

	if !m.HasEvent("PostToolUse") {
		t.Fatal("extra-path hook not loaded")
	}

	// Touch a watched file to force a hot reload.
	time.Sleep(50 * time.Millisecond)
	mustWrite(t, hooksPath, `{"Hooks":[{"Hooks":[{"Type":"PreToolUse","Command":"echo a"}]}]}`)

	deadline := time.Now().Add(3 * hotReloadInterval)
	for {
		m.mu.Lock()
		post := len(m.hooks["post_tool_use"])
		m.mu.Unlock()
		if post == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("extra-path hook lost after hot reload")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadClaudeHooks_HotReload(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	hooksPath := filepath.Join(proj, ".claude", "hooks.json")

	mustWrite(t, hooksPath, `{"Hooks":[{"Hooks":[{"Type":"PreToolUse","Command":"echo a"}]}]}`)

	t.Setenv("COVO_CLAUDE_HOOKS_DISABLED", "")

	m := NewShellHookManager(t.TempDir(), true)
	m.LoadClaudeHooks(proj, home)
	m.StartHotReload(proj, filepath.Join(home, ".claude", "hooks.json"), hooksPath)
	defer m.Stop()

	if n := len(m.hooks["pre_tool_use"]); n != 1 {
		t.Fatalf("expected 1 pre_tool_use hook after initial load, got %d", n)
	}

	// Modify the file, adding a PostToolUse hook.
	time.Sleep(50 * time.Millisecond)
	mustWrite(t, hooksPath, `{"Hooks":[{"Hooks":[{"Type":"PreToolUse","Command":"echo a"},{"Type":"PostToolUse","Command":"echo b"}]}]}`)

	deadline := time.Now().Add(3 * hotReloadInterval)
	for {
		m.mu.Lock()
		post := len(m.hooks["post_tool_use"])
		m.mu.Unlock()
		if post == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("PostToolUse hook not picked up by hot reload")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// --- real Claude Code settings.json nested shape ---

const claudeSettingsJSONFormat = `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": "echo block-bash", "timeout": 30 },
          { "type": "command", "command": "echo always", "async": true }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          { "type": "command", "command": "echo stop-gate" }
        ]
      }
    ]
  }
}`

func TestParseClaudeHooks_SettingsJSONNested(t *testing.T) {
	specs, err := parseClaudeHooks([]byte(claudeSettingsJSONFormat))
	if err != nil {
		t.Fatalf("parseClaudeHooks: %v", err)
	}
	if len(specs) != 3 {
		t.Fatalf("expected 3 specs, got %d: %#v", len(specs), specs)
	}
	byCommand := map[string]map[string]any{}
	for _, s := range specs {
		byCommand[s["command"].(string)] = s
	}
	pre, ok := byCommand["echo block-bash"]
	if !ok {
		t.Fatalf("missing PreToolUse spec: %#v", specs)
	}
	if pre["event"] != "PreToolUse" || pre["matcher"] != "Bash" {
		t.Errorf("unexpected pre spec: %#v", pre)
	}
	if tsec, ok := pre["timeout"].(float64); !ok || tsec != 30 {
		t.Errorf("expected timeout 30, got %#v", pre["timeout"])
	}
	asyncSpec, ok := byCommand["echo always"]
	if !ok {
		t.Fatalf("missing async PreToolUse spec: %#v", specs)
	}
	if asyncSpec["async"] != true {
		t.Errorf("expected async handler, got %#v", asyncSpec)
	}
	stop, ok := byCommand["echo stop-gate"]
	if !ok {
		t.Fatalf("missing Stop spec: %#v", specs)
	}
	if stop["event"] != "Stop" || stop["matcher"] != "" {
		t.Errorf("unexpected stop spec: %#v", stop)
	}
}

func TestParseHooksFile_MatchesStartupParsingForSettingsJSON(t *testing.T) {
	// The hot-reload parser must accept the same shapes the startup parser
	// does, in particular the nested Claude Code settings.json format.
	startup, err := parseClaudeHooks([]byte(claudeSettingsJSONFormat))
	if err != nil {
		t.Fatalf("parseClaudeHooks: %v", err)
	}
	reload, err := parseHooksFile([]byte(claudeSettingsJSONFormat))
	if err != nil {
		t.Fatalf("parseHooksFile: %v", err)
	}
	if len(startup) != len(reload) {
		t.Fatalf("startup parsed %d specs, hot reload parsed %d", len(startup), len(reload))
	}
	startCmds := map[string]bool{}
	for _, s := range startup {
		startCmds[s["command"].(string)] = true
	}
	for _, s := range reload {
		if !startCmds[s["command"].(string)] {
			t.Errorf("hot reload produced spec %v missing from startup parse", s)
		}
	}
}
