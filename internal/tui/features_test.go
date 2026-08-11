package tui

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 11 个新功能的测试
// ---------------------------------------------------------------------------

// --- #1 Scrollback 全文搜索 ---

func TestScrollbackSearch(t *testing.T) {
	p := NewScrollbackPipeline()
	p.Append(&UserPromptBlock{Text: "hello world"})
	p.Append(&AgentMessageBlock{Text: "hi there"})
	p.Append(&UserPromptBlock{Text: "hello again"})

	idx := NewScrollbackSearchIndex()
	if !idx.Sync(p) {
		t.Fatal("expected sync to rebuild")
	}
	// Second sync should be no-op
	if idx.Sync(p) {
		t.Fatal("expected second sync to be no-op")
	}

	matches := idx.Find("hello")
	if len(matches) != 2 {
		t.Errorf("expected 2 matches for 'hello', got %d", len(matches))
	}

	// Regex search
	matches = idx.Find("/wor.d")
	if len(matches) != 1 {
		t.Errorf("expected 1 regex match, got %d", len(matches))
	}

	// No match
	matches = idx.Find("nonexistent")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matches))
	}
}

// --- #2 文本选择 ---

func TestTextSelection(t *testing.T) {
	ts := NewTextSelection()
	if ts.IsActive() {
		t.Fatal("should start inactive")
	}

	ts.StartDrag(SelectionAnchor{EntryID: 1, LineIdx: 0, Col: 0})
	if !ts.IsActive() {
		t.Fatal("should be active after start drag")
	}

	ts.UpdateDrag(SelectionAnchor{EntryID: 1, LineIdx: 2, Col: 5})
	r, ok := ts.Range()
	if !ok {
		t.Fatal("expected range")
	}
	if r.Start.Col != 0 || r.End.Col != 5 {
		t.Errorf("range start/end wrong: %+v", r)
	}

	ts.Clear()
	if ts.IsActive() {
		t.Fatal("should be inactive after clear")
	}
}

func TestComputeAutoScroll(t *testing.T) {
	// Near top
	s := ComputeAutoScroll(0, 20)
	if s == nil || s.Direction != AutoScrollUp {
		t.Error("expected up scroll at top")
	}
	// Near bottom
	s = ComputeAutoScroll(19, 20)
	if s == nil || s.Direction != AutoScrollDown {
		t.Error("expected down scroll at bottom")
	}
	// Middle
	s = ComputeAutoScroll(10, 20)
	if s != nil {
		t.Error("expected no scroll in middle")
	}
}

// --- #3 Sticky Header ---

func TestStickyHeader(t *testing.T) {
	prompts := []PromptDescriptor{
		{EntryIdx: 0, YVirtual: 0, FullHeight: 5, MinHeight: 4, Sticky: true},
		{EntryIdx: 5, YVirtual: 20, FullHeight: 3, MinHeight: 4, Sticky: true},
	}
	// Scroll past first prompt
	layout := ComputeStickyLayout(prompts, 10, 20)
	if layout.Pinned == nil {
		t.Fatal("expected pinned prompt")
	}
	if layout.Pinned.EntryIdx != 0 {
		t.Errorf("expected entry 0, got %d", layout.Pinned.EntryIdx)
	}
	// Pinned height should not exceed MinHeight when no push
	if layout.Pinned.RenderHeight > prompts[0].MinHeight {
		t.Errorf("renderHeight %d should not exceed MinHeight %d", layout.Pinned.RenderHeight, prompts[0].MinHeight)
	}
}

func TestStickyHeaderNegativeDistance(t *testing.T) {
	// Regression: next prompt above scrollOffset → distanceToNext negative
	// should NOT inflate renderHeight
	prompts := []PromptDescriptor{
		{EntryIdx: 0, YVirtual: 0, FullHeight: 5, MinHeight: 4, Sticky: true},
		{EntryIdx: 3, YVirtual: 5, FullHeight: 3, MinHeight: 4, Sticky: true},
	}
	// scrollOffset=10: both prompts are above viewport
	// prompt[1].YVirtual=5 < 10 → distanceToNext = 5-10 = -5
	layout := ComputeStickyLayout(prompts, 10, 20)
	if layout.Pinned != nil && layout.Pinned.RenderHeight > prompts[0].MinHeight {
		t.Errorf("negative distance inflated renderHeight to %d (max should be %d)",
			layout.Pinned.RenderHeight, prompts[0].MinHeight)
	}
}

func TestCollectPromptDescriptors(t *testing.T) {
	p := NewScrollbackPipeline()
	p.Append(&UserPromptBlock{Text: "first question"})
	p.Append(&AgentMessageBlock{Text: "answer"})
	p.Append(&UserPromptBlock{Text: "second question"})

	descs := p.CollectPromptDescriptors()
	if len(descs) != 2 {
		t.Fatalf("expected 2 prompt descriptors, got %d", len(descs))
	}
	if descs[0].EntryIdx != 0 {
		t.Errorf("expected first at idx 0, got %d", descs[0].EntryIdx)
	}
}

// --- #4 Turn 导航 ---

func TestTurnNavigation(t *testing.T) {
	p := NewScrollbackPipeline()
	p.Append(&UserPromptBlock{Text: "hello"})
	p.Append(&AgentMessageBlock{Text: "hi"})
	p.Append(&UserPromptBlock{Text: "second question"})
	p.Append(&AgentMessageBlock{Text: "second answer"})

	tm := NewTurnModel()
	tm.Rebuild(p)
	turns := tm.Turns()
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}

	entries := tm.TimelineEntries(p)
	if len(entries) != 2 {
		t.Fatalf("expected 2 timeline entries, got %d", len(entries))
	}
	if entries[0].Preview != "hello" {
		t.Errorf("expected preview 'hello', got '%s'", entries[0].Preview)
	}
}

// --- #5 Verb Group ---

func TestVerbGroup(t *testing.T) {
	p := NewScrollbackPipeline()
	p.Append(&ToolCallBlock{ToolName: "read_file", Args: "a.txt"})
	p.Append(&ToolCallBlock{ToolName: "read_file", Args: "b.txt"})
	p.Append(&ToolCallBlock{ToolName: "read_file", Args: "c.txt"})
	p.Append(&AgentMessageBlock{Text: "done"})

	gm := NewGroupModel()
	spans := gm.Rebuild(p, 10)
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Kind != GroupKindVerbRun {
		t.Error("expected VerbRun")
	}
	if spans[0].Members != 3 {
		t.Errorf("expected 3 members, got %d", spans[0].Members)
	}
	// Bug fix: EndIdx should be 3 (not 2), covering all 3 tool calls
	if spans[0].EndIdx != 3 {
		t.Errorf("expected EndIdx=3, got %d (skip entry bug)", spans[0].EndIdx)
	}

	// Check hidden entries
	if !gm.IsEntryHidden(2) {
		t.Error("entry 2 should be hidden (folded)")
	}
	if gm.IsEntryHidden(0) {
		t.Error("entry 0 should be visible (group header)")
	}

	// Toggle expand
	gm.ToggleExpand(p.Entries()[0].ID)
	if !gm.IsExpanded(p.Entries()[0].ID) {
		t.Error("expected expanded after toggle")
	}
}

func TestVerbGroupNoSkip(t *testing.T) {
	// Regression: ensure entries after a verb run are not skipped
	p := NewScrollbackPipeline()
	p.Append(&ToolCallBlock{ToolName: "read_file", Args: "a.txt"})
	p.Append(&ToolCallBlock{ToolName: "read_file", Args: "b.txt"})
	p.Append(&AgentMessageBlock{Text: "between"})
	p.Append(&ToolCallBlock{ToolName: "read_file", Args: "c.txt"})
	p.Append(&ToolCallBlock{ToolName: "read_file", Args: "d.txt"})

	gm := NewGroupModel()
	spans := gm.Rebuild(p, 10)
	// Should have 2 VerbRun spans (idx 0-1 and idx 3-4)
	verbRuns := 0
	for _, s := range spans {
		if s.Kind == GroupKindVerbRun {
			verbRuns++
		}
	}
	if verbRuns != 2 {
		t.Errorf("expected 2 verb runs, got %d (skip entry bug)", verbRuns)
	}
}

// --- #6 对话导出 Markdown ---

func TestExportMarkdown(t *testing.T) {
	p := NewScrollbackPipeline()
	p.Append(&UserPromptBlock{Text: "hello"})
	p.Append(&AgentMessageBlock{Text: "hi there"})
	p.Append(&ToolCallBlock{ToolName: "read_file", Args: "a.txt"})
	p.Append(&ThinkingBlock{Text: "hmm"})
	p.Append(&ErrorBlock{Text: "something went wrong"})
	p.Append(&AgentMessageBlock{Text: "result here"})

	md := ExportToMarkdown(p)
	if !strings.Contains(md, "## User") {
		t.Error("missing ## User section")
	}
	if !strings.Contains(md, "## Assistant") {
		t.Error("missing ## Assistant section")
	}
	if !strings.Contains(md, "## Tools") {
		t.Error("missing ## Tools section")
	}
	if !strings.Contains(md, "## Error") {
		t.Error("missing ## Error section")
	}
	if !strings.Contains(md, "hello") {
		t.Error("missing user text")
	}
	if !strings.Contains(md, "something went wrong") {
		t.Error("missing error text")
	}
	// Thinking should be skipped
	if strings.Contains(md, "hmm") {
		t.Error("thinking block should be skipped")
	}
}

// --- #7 macOS 修饰键 (非 darwin 平台测试) ---

func TestModifierSnapshot(t *testing.T) {
	s := SnapshotModifiers()
	// On non-macOS, all false
	if s.Command || s.Option || s.Shift || s.Control {
		t.Error("expected all false on non-macOS")
	}
}

func TestIsNewlineModifierHeld(t *testing.T) {
	ctx := &TerminalContext{Brand: BrandAppleTerminal}
	// On non-macOS, always false
	if IsNewlineModifierHeld(ctx) {
		t.Error("expected false on non-macOS")
	}
}

// --- #8 多主题 ---

func TestThemeManager(t *testing.T) {
	tm := NewThemeManager()
	presets := tm.List()
	if len(presets) < 2 {
		t.Fatalf("expected at least 2 presets, got %d", len(presets))
	}

	if tm.Active() != "dark" {
		t.Errorf("expected active='dark', got '%s'", tm.Active())
	}

	if !tm.Apply("light") {
		t.Error("apply light failed")
	}
	if tm.Active() != "light" {
		t.Error("expected active='light' after apply")
	}

	// Apply non-existent
	if tm.Apply("nonexistent") {
		t.Error("apply nonexistent should fail")
	}

	// ApplyNext
	next := tm.ApplyNext()
	if next == "" {
		t.Error("expected non-empty next theme")
	}

	// 恢复默认主题，避免污染其他测试的全局 palette 状态
	tm.Apply("dark")
}

// --- #9 水平布局 ---

func TestHorizontalLayout(t *testing.T) {
	config := DefaultLayoutConfig()
	layout := NewHorizontalLayout(80, config)
	if layout.Accent != 1 {
		t.Errorf("expected accent=1, got %d", layout.Accent)
	}
	if layout.LeftPadding != 2 {
		t.Errorf("expected leftPad=2, got %d", layout.LeftPadding)
	}
	if layout.RightPadding != 1 {
		t.Errorf("expected rightPad=1, got %d", layout.RightPadding)
	}
	if layout.Content != 76 {
		t.Errorf("expected content=76, got %d", layout.Content)
	}
	if layout.ContentStart() != 3 {
		t.Errorf("expected contentStart=3, got %d", layout.ContentStart())
	}
	if ChromeWidth(config) != 4 {
		t.Errorf("expected chromeWidth=4, got %d", ChromeWidth(config))
	}
}

// --- #10 Block 生命周期 ---

func TestNextDisplayMode(t *testing.T) {
	if NextDisplayMode(DisplayNormal) != DisplayCollapsed {
		t.Error("Normal → Collapsed failed")
	}
	if NextDisplayMode(DisplayCollapsed) != DisplayExpanded {
		t.Error("Collapsed → Expanded failed")
	}
	if NextDisplayMode(DisplayExpanded) != DisplayNormal {
		t.Error("Expanded → Normal failed")
	}
}

func TestBlockFoldManager(t *testing.T) {
	m := NewBlockFoldManager()
	id := EntryID(42)
	if m.GetDisplayMode(id) != DisplayNormal {
		t.Error("expected default Normal")
	}
	m.ToggleFold(id, false)
	if m.GetDisplayMode(id) != DisplayCollapsed {
		t.Error("expected Collapsed after toggle")
	}
	m.SetDisplayMode(id, DisplayExpanded)
	if m.GetDisplayMode(id) != DisplayExpanded {
		t.Error("expected Expanded after set")
	}
	if m.IsRawMode(id) {
		t.Error("expected raw off by default")
	}
	m.ToggleRaw(id)
	if !m.IsRawMode(id) {
		t.Error("expected raw on after toggle")
	}
}

// --- #11 状态栏块 ---

func TestQueueBlock(t *testing.T) {
	b := &QueueBlock{Prompts: []string{"first", "second", "third"}}
	if b.Kind() != BlockKindSystem {
		t.Error("expected system kind")
	}
	lines := b.RenderLines(80, nil)
	if len(lines) != 4 {
		t.Errorf("expected 4 lines, got %d", len(lines))
	}

	empty := &QueueBlock{}
	if empty.Summary() != "Queue is empty." {
		t.Error("empty queue summary wrong")
	}
}

func TestTasksBlock(t *testing.T) {
	b := &TasksBlock{Tasks: []TaskEntry{
		{Name: "compile", Status: "running"},
		{Name: "test", Status: "completed"},
		{Name: "lint", Status: "failed"},
	}}
	if b.Summary() != "Tasks: 1 running" {
		t.Errorf("summary wrong: %s", b.Summary())
	}
	lines := b.RenderLines(80, nil)
	if len(lines) != 4 {
		t.Errorf("expected 4 lines, got %d", len(lines))
	}
}

func TestUsageBlock(t *testing.T) {
	b := &UsageBlock{
		InputTokens:  1234,
		OutputTokens: 5678,
		TotalTokens:  6912,
		Model:        "gpt-4o",
		Provider:     "openai",
		TurnCount:    5,
	}
	lines := b.RenderLines(80, nil)
	if len(lines) != 6 {
		t.Errorf("expected 6 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[3], "1,234") {
		t.Errorf("expected grouped thousands, got: %s", lines[3])
	}
}

func TestFormatDuration(t *testing.T) {
	if formatDuration(50000000) != "50ms" {
		t.Error("50ms format wrong")
	}
	if formatDuration(2500000000) != "2.5s" {
		t.Error("2.5s format wrong")
	}
}

func TestGroupThousands(t *testing.T) {
	if groupThousands(0) != "0" {
		t.Error("0 wrong")
	}
	if groupThousands(999) != "999" {
		t.Error("999 wrong")
	}
	if groupThousands(1000) != "1,000" {
		t.Error("1000 wrong")
	}
	if groupThousands(1234567) != "1,234,567" {
		t.Error("1234567 wrong")
	}
}
