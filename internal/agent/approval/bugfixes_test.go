package approval

import (
	"log/slog"
	"testing"
	"unicode/utf8"
)

// resetCommandPolicy clears the global command-level policy rules between tests.
func resetCommandPolicy() {
	globalPolicy.mu.Lock()
	globalPolicy.rules = nil
	globalPolicy.mu.Unlock()
}

// --- Bug 1: allow must not override deny ---

func TestCheckPolicy_DenyPrecedenceOverAllow(t *testing.T) {
	enableExecPolicy(t)
	resetCommandPolicy()
	defer resetCommandPolicy()

	// "rm" matches both the allow pattern "*" and the deny pattern "rm".
	// Deny must take precedence.
	path := writeTestPolicy(t, `
allow:
  - "*"
deny:
  - rm
`)
	if err := LoadPolicy(path); err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}

	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}

	d := sys.CheckPolicy("rm -rf /tmp/foo")
	if d == nil {
		t.Fatal("expected non-nil Decision for command matching both allow and deny")
	}
	if d.Approved {
		t.Errorf("expected deny to take precedence over allow, but command was approved")
	}
}

func TestCheckPolicy_AllowWhenNoDenyMatch(t *testing.T) {
	enableExecPolicy(t)
	resetCommandPolicy()
	defer resetCommandPolicy()

	path := writeTestPolicy(t, `
allow:
  - "*"
deny:
  - rm
`)
	if err := LoadPolicy(path); err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}

	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}

	// "ls" matches allow "*" but not deny "rm" — should be approved.
	d := sys.CheckPolicy("ls -la")
	if d == nil {
		t.Fatal("expected non-nil Decision for allow-matching command")
	}
	if !d.Approved {
		t.Errorf("expected command matching only allow to be approved")
	}
}

// --- Bug 2: empty / whitespace command must not panic ---

func TestMatchPolicy_EmptyCommandNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("matchPolicy panicked on empty command: %v", r)
		}
	}()

	if matchPolicy("", "rm") {
		t.Errorf("expected no match for empty command")
	}
	if matchPolicy("   ", "rm") {
		t.Errorf("expected no match for whitespace-only command")
	}
	if matchPolicy("\t\n", "rm") {
		t.Errorf("expected no match for whitespace-only command")
	}
}

func TestCheckPolicy_EmptyCommandNoPanic(t *testing.T) {
	enableExecPolicy(t)
	resetCommandPolicy()
	defer resetCommandPolicy()

	path := writeTestPolicy(t, `
deny:
  - rm
`)
	if err := LoadPolicy(path); err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}

	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("CheckPolicy panicked on empty command: %v", r)
		}
	}()

	d := sys.CheckPolicy("")
	if d != nil {
		t.Errorf("expected nil Decision for empty command, got: %+v", d)
	}

	d = sys.CheckPolicy("   ")
	if d != nil {
		t.Errorf("expected nil Decision for whitespace command, got: %+v", d)
	}
}

// --- Bug 3: case-insensitive tool name matching ---

func TestExpandToolNames_CaseInsensitive(t *testing.T) {
	// Uppercase group aliases should still expand.
	result := expandToolNames([]string{"Edit", "BASH", "READ"})
	expected := map[string]bool{
		// edit group
		"write_file": true, "write": true, "edit_block": true, "edit": true,
		"apply_patch": true, "patch": true, "move": true, "delete_file": true,
		"str_replace_editor": true,
		// bash group
		"bash": true, "process": true,
		// read group
		"read": true, "read_file": true, "glob": true, "grep": true, "ls": true,
	}
	for tool := range expected {
		if !result[tool] {
			t.Errorf("expected tool %q to be in expanded set (case-insensitive)", tool)
		}
	}
}

func TestCheckToolPolicy_CaseInsensitiveDeny(t *testing.T) {
	enableExecPolicy(t)
	resetToolPolicy()
	defer resetToolPolicy()

	// Policy denies the "edit" group (lowercase in YAML).
	path := writeTestPolicy(t, `
tools:
  deny:
    - edit
`)
	if err := LoadPolicy(path); err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}

	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}

	// Uppercase / mixed-case tool names should still be denied.
	for _, tool := range []string{"EDIT_BLOCK", "Edit_Block", "Write_File", "APPLY_PATCH"} {
		d := sys.CheckToolPolicy(tool)
		if d == nil {
			t.Errorf("expected deny for tool %q (case-insensitive)", tool)
			continue
		}
		if d.Approved {
			t.Errorf("expected tool %q to be denied (case-insensitive)", tool)
		}
	}
}

func TestCheckToolPolicy_CaseInsensitiveAllow(t *testing.T) {
	enableExecPolicy(t)
	resetToolPolicy()
	defer resetToolPolicy()

	path := writeTestPolicy(t, `
tools:
  allow:
    - read
`)
	if err := LoadPolicy(path); err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}

	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}

	// Uppercase tool names in the read group should be allowed.
	for _, tool := range []string{"READ", "Read_File", "GLOB", "LS"} {
		d := sys.CheckToolPolicy(tool)
		if d == nil {
			t.Errorf("expected allow for tool %q (case-insensitive)", tool)
			continue
		}
		if !d.Approved {
			t.Errorf("expected tool %q to be approved (case-insensitive)", tool)
		}
	}
}

// --- Bug 4: truncate must not panic on small maxLen ---

func TestTruncate_SmallMaxLenNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("truncate panicked on small maxLen: %v", r)
		}
	}()

	for _, maxLen := range []int{0, 1, 2, -1, -5} {
		got := truncate("hello world", maxLen)
		if got != "hello world" {
			t.Errorf("truncate(\"hello world\", %d) = %q, want %q (should return original for small maxLen)", maxLen, got, "hello world")
		}
	}
}

func TestTruncate_ShortStringReturnedAsIs(t *testing.T) {
	// String shorter than maxLen should be returned unchanged.
	got := truncate("hi", 10)
	if got != "hi" {
		t.Errorf("truncate(\"hi\", 10) = %q, want %q", got, "hi")
	}
}

func TestTruncate_NormalTruncation(t *testing.T) {
	got := truncate("hello world", 8)
	want := "hello..."
	if got != want {
		t.Errorf("truncate(\"hello world\", 8) = %q, want %q", got, want)
	}
}

// --- CJK truncation must produce valid UTF-8 ---

func TestTruncate_CJKValidUTF8(t *testing.T) {
	// "你好世界你好世界" is 8 runes, 24 bytes. Each CJK char is 3 bytes.
	// With byte-based truncation at maxLen=5, s[:2] would split a 3-byte rune.
	cjk := "你好世界你好世界"

	for _, maxLen := range []int{3, 4, 5, 6, 7, 8, 10} {
		got := truncate(cjk, maxLen)
		if !utf8.ValidString(got) {
			t.Errorf("truncate(CJK, %d) produced invalid UTF-8: %q", maxLen, got)
		}
		// Truncated result should not be longer than maxLen runes.
		if runeCount := len([]rune(got)); runeCount > maxLen {
			t.Errorf("truncate(CJK, %d) has %d runes, expected <= %d", maxLen, runeCount, maxLen)
		}
	}
}

func TestTruncate_CJKExactTruncation(t *testing.T) {
	// 8 runes, maxLen=5 → runes[:2] + "..." = "你好..."
	got := truncate("你好世界你好世界", 5)
	want := "你好..."
	if got != want {
		t.Errorf("truncate(CJK, 5) = %q, want %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncate(CJK, 5) produced invalid UTF-8: %q", got)
	}
}
