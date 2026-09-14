package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	core "github.com/covoyage/covonaut/tui/core"
)

func TestToolStatusKnownTool(t *testing.T) {
	if got := toolStatus("read_file", "{}"); got != "reading file..." {
		t.Fatalf("toolStatus(read_file) = %q", got)
	}
}

func TestToolStatusUnknownToolIncludesName(t *testing.T) {
	const toolName = "custom_tool"
	if got := toolStatus(toolName, "{}"); !strings.Contains(got, toolName) {
		t.Fatalf("toolStatus(%q) = %q, want tool name", toolName, got)
	}
}

// TestMergeThinkingTailWhitespace 验证思考 delta 的空白折叠与 ANSI 剥离。
func TestMergeThinkingTailWhitespace(t *testing.T) {
	got := mergeThinkingTail("", "\x1b[2m先检查\n\n  socket\t超时设置\x1b[0m")
	want := "先检查 socket 超时设置"
	if got != want {
		t.Fatalf("mergeThinkingTail = %q, want %q", got, want)
	}
	// 追加时空白不重复。
	got = mergeThinkingTail(got, "\n然后看重试逻辑")
	if got != want+" 然后看重试逻辑" {
		t.Fatalf("append = %q, want %q", got, want+" 然后看重试逻辑")
	}
}

// TestMergeThinkingTailTruncate 验证超限从头丢弃且不切半个 rune。
func TestMergeThinkingTailTruncate(t *testing.T) {
	// 用 CJK（每 rune 3 字节）撑爆缓冲，截断点必须落在 rune 边界。
	long := strings.Repeat("思", thinkingTailLimit) // 远超上限
	got := mergeThinkingTail("", long)
	if len(got) > thinkingTailLimit {
		t.Fatalf("buffer not truncated: %d bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncated buffer is not valid UTF-8")
	}
	// 头部必须从完整 rune 开始。
	if r, _ := utf8.DecodeRuneInString(got); r == utf8.RuneError {
		t.Fatalf("truncation landed mid-rune: %q", got[:6])
	}
	if core.StripAnsi(long) != long {
		t.Fatalf("StripAnsi unexpectedly modified plain CJK text")
	}
}

// TestMergeThinkingTailAnsiOnly 验证纯 ANSI/空白 delta 不改变缓冲。
func TestMergeThinkingTailAnsiOnly(t *testing.T) {
	buf := "已有内容"
	got := mergeThinkingTail(buf, "\x1b[0m\n\n  ")
	if got != buf {
		t.Fatalf("whitespace-only delta must not grow buffer: %q -> %q", buf, got)
	}
}
