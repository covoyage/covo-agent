package tui

import (
	"testing"

	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// 10 项新功能的测试
// ---------------------------------------------------------------------------

// --- #1 工具特化 Block ---

func TestNewToolBlock_Edit(t *testing.T) {
	block := NewToolBlock("edit_file", `{"file":"main.go"}`)
	editBlock, ok := block.(*EditToolBlock)
	if !ok {
		t.Fatalf("expected EditToolBlock, got %T", block)
	}
	if editBlock.FilePath != "main.go" {
		t.Errorf("expected FilePath=main.go, got %s", editBlock.FilePath)
	}
}

func TestNewToolBlock_Execute(t *testing.T) {
	block := NewToolBlock("bash", "ls -la")
	execBlock, ok := block.(*ExecuteToolBlock)
	if !ok {
		t.Fatalf("expected ExecuteToolBlock, got %T", block)
	}
	if execBlock.Command != "ls -la" {
		t.Errorf("expected Command=ls -la, got %s", execBlock.Command)
	}
}

func TestNewToolBlock_Read(t *testing.T) {
	block := NewToolBlock("read_file", `{"path":"test.go"}`)
	readBlock, ok := block.(*ReadToolBlock)
	if !ok {
		t.Fatalf("expected ReadToolBlock, got %T", block)
	}
	if readBlock.FilePath != "test.go" {
		t.Errorf("expected FilePath=test.go, got %s", readBlock.FilePath)
	}
}

func TestNewToolBlock_Search(t *testing.T) {
	block := NewToolBlock("grep", "TODO")
	searchBlock, ok := block.(*SearchToolBlock)
	if !ok {
		t.Fatalf("expected SearchToolBlock, got %T", block)
	}
	if searchBlock.Pattern != "TODO" {
		t.Errorf("expected Pattern=TODO, got %s", searchBlock.Pattern)
	}
}

func TestNewToolBlock_Fallback(t *testing.T) {
	block := NewToolBlock("unknown_tool", "args")
	if _, ok := block.(*ToolCallBlock); !ok {
		t.Fatalf("expected ToolCallBlock fallback, got %T", block)
	}
}

func TestEditToolBlock_Render(t *testing.T) {
	pal := theme.CurrentPalette()
	b := &EditToolBlock{
		FilePath: "main.go",
		DiffText: "+new line\n-old line\n context",
	}
	lines := b.RenderLines(80, pal)
	if len(lines) < 4 {
		t.Errorf("expected at least 4 lines, got %d", len(lines))
	}
}

func TestExecuteToolBlock_Render(t *testing.T) {
	pal := theme.CurrentPalette()
	b := &ExecuteToolBlock{
		Command: "go build ./...",
		Output:  "ok",
	}
	lines := b.RenderLines(80, pal)
	if len(lines) < 2 {
		t.Errorf("expected at least 2 lines, got %d", len(lines))
	}
}

// --- #2 Context Bar ---

func TestFormatTokensCompact(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{999, "999"},
		{1500, "1.5K"},
		{12000, "12K"},
		{1500000, "1.5M"},
		{12000000, "12M"},
	}
	for _, tt := range tests {
		got := FormatTokensCompact(tt.input)
		if got != tt.want {
			t.Errorf("FormatTokensCompact(%d) = %s, want %s", tt.input, got, tt.want)
		}
	}
}

func TestRenderContextBar(t *testing.T) {
	pal := theme.CurrentPalette()
	cfg := DefaultContextBarConfig()

	// 0% usage
	bar := RenderContextBar(0, 128000, cfg, pal)
	if bar == "" {
		t.Error("expected non-empty bar for 0% usage")
	}

	// 50% usage
	bar = RenderContextBar(64000, 128000, cfg, pal)
	if bar == "" {
		t.Error("expected non-empty bar for 50% usage")
	}

	// 100% usage
	bar = RenderContextBar(128000, 128000, cfg, pal)
	if bar == "" {
		t.Error("expected non-empty bar for 100% usage")
	}

	// total=0 should return empty
	bar = RenderContextBar(100, 0, cfg, pal)
	if bar != "" {
		t.Error("expected empty bar for total=0")
	}
}

func TestContextBarColorGradient(t *testing.T) {
	pal := theme.CurrentPalette()
	cfg := DefaultContextBarConfig()

	// Low usage should use Dim style
	bar1 := RenderContextBar(1000, 128000, cfg, pal)
	// High usage should use Error style
	bar2 := RenderContextBar(127000, 128000, cfg, pal)
	if bar1 == bar2 {
		t.Error("expected different rendering for low vs high usage")
	}
}

// --- #5 Finish Flash ---

func TestFinishFlashTracker(t *testing.T) {
	tracker := NewFinishFlashTracker()

	// Not flashing initially
	if tracker.IsFlashing(1) {
		t.Error("should not be flashing initially")
	}

	// Mark as finished
	tracker.OnFinish(1)
	if !tracker.IsFlashing(1) {
		t.Error("should be flashing after OnFinish")
	}

	// Intensity should be > 0
	intensity := tracker.FlashIntensity(1)
	if intensity <= 0 {
		t.Error("expected positive intensity")
	}

	// Tick should clean up expired entries (but not immediately)
	tracker.Tick()
	if !tracker.IsFlashing(1) {
		t.Error("should still be flashing after immediate tick")
	}
}

func TestFinishFlashRenderAccent(t *testing.T) {
	pal := theme.CurrentPalette()
	tracker := NewFinishFlashTracker()
	entry := &ScrollbackEntry{
		ID:    42,
		Kind:  BlockKindUserPrompt,
		Block: &UserPromptBlock{Text: "test"},
	}
	tracker.OnFinish(42)

	// Should render with flash accent
	line := tracker.RenderFlashAccent(entry, pal)
	if line == "" {
		t.Error("expected non-empty accent line")
	}
}

// --- #6 Search Highlight + Navigation ---

func TestSearchState(t *testing.T) {
	state := NewSearchState()
	if state.IsActive() {
		t.Error("should not be active initially")
	}

	matches := []ScrollbackMatch{
		{EntryID: 1, LineIndex: 0, ByteRange: [2]int{0, 5}},
		{EntryID: 2, LineIndex: 1, ByteRange: [2]int{3, 8}},
		{EntryID: 3, LineIndex: 0, ByteRange: [2]int{10, 15}},
	}
	state.Start("hello", matches)

	if !state.IsActive() {
		t.Error("should be active after Start")
	}
	if state.MatchCount() != 3 {
		t.Errorf("expected 3 matches, got %d", state.MatchCount())
	}
	if state.CurrentIndex() != 1 {
		t.Errorf("expected current index 1, got %d", state.CurrentIndex())
	}

	// Next
	state.Next()
	if state.CurrentIndex() != 2 {
		t.Errorf("expected current index 2 after Next, got %d", state.CurrentIndex())
	}

	// Next again (still within range)
	state.Next()
	if state.CurrentIndex() != 3 {
		t.Errorf("expected current index 3 after 2nd Next, got %d", state.CurrentIndex())
	}

	// Next again (wrap around to 1)
	state.Next()
	if state.CurrentIndex() != 1 {
		t.Errorf("expected current index 1 after wrap, got %d", state.CurrentIndex())
	}

	// Prev (wrap to 3)
	state.Prev()
	if state.CurrentIndex() != 3 {
		t.Errorf("expected current index 3 after Prev, got %d", state.CurrentIndex())
	}

	// Close
	state.Close()
	if state.IsActive() {
		t.Error("should not be active after Close")
	}
}

func TestSearchHighlightLine(t *testing.T) {
	pal := theme.CurrentPalette()
	state := NewSearchState()
	matches := []ScrollbackMatch{
		{EntryID: 1, LineIndex: 0, ByteRange: [2]int{0, 5}},
	}
	state.Start("hello", matches)

	line := "hello world"
	highlighted := state.HighlightLine(line, 1, 0, pal)
	// Should return a non-empty string (highlighting may or may not change
	// the text depending on whether the terminal supports colors)
	if highlighted == "" {
		t.Error("expected non-empty highlighted line")
	}
}

func TestSearchStatusLine(t *testing.T) {
	pal := theme.CurrentPalette()
	state := NewSearchState()

	// Not active
	if line := state.SearchStatusLine(pal); line != "" {
		t.Error("expected empty status when not active")
	}

	// Active with matches
	state.Start("test", []ScrollbackMatch{{EntryID: 1}})
	line := state.SearchStatusLine(pal)
	if line == "" {
		t.Error("expected non-empty status when active")
	}
}

// --- #7 Shortcuts Bar ---

func TestShortcutsBar(t *testing.T) {
	pal := theme.CurrentPalette()
	sb := NewShortcutsBar()

	// Default context (prompt)
	sb.SetContext(ShortcutCtxPrompt)
	hints := sb.Hints()
	if len(hints) == 0 {
		t.Error("expected hints for prompt context")
	}

	// Render
	line := sb.Render(80, pal)
	if line == "" {
		t.Error("expected non-empty render")
	}

	// Switch to search context
	sb.SetContext(ShortcutCtxSearch)
	hints = sb.Hints()
	if len(hints) == 0 {
		t.Error("expected hints for search context")
	}

	// Busy context
	sb.SetContext(ShortcutCtxBusy)
	hints = sb.Hints()
	if len(hints) == 0 {
		t.Error("expected hints for busy context")
	}
}

// --- #8 Subagent/BgTask/Workflow Block ---

func TestSubagentBlock(t *testing.T) {
	pal := theme.CurrentPalette()
	b := &SubagentBlock{
		AgentName:   "coder",
		KindExt:     SubagentKindSpawn,
		SummaryText: "spawning sub-agent",
	}
	if b.Kind() != BlockKindSessionEvent {
		t.Error("expected session event kind")
	}
	lines := b.RenderLines(80, pal)
	if len(lines) < 2 {
		t.Errorf("expected at least 2 lines, got %d", len(lines))
	}
}

func TestBgTaskBlock(t *testing.T) {
	pal := theme.CurrentPalette()
	b := &BgTaskBlock{
		TaskName: "compile",
		Status:   "running",
		Output:   "building...",
	}
	if b.Kind() != BlockKindSessionEvent {
		t.Error("expected session event kind")
	}
	lines := b.RenderLines(80, pal)
	if len(lines) < 2 {
		t.Errorf("expected at least 2 lines, got %d", len(lines))
	}
}

func TestWorkflowBlock(t *testing.T) {
	pal := theme.CurrentPalette()
	b := &WorkflowBlock{
		Name:  "deploy",
		Phase: WorkflowPhaseExecuting,
		Steps: []WorkflowStep{
			{Name: "build", Status: "done"},
			{Name: "test", Status: "running"},
			{Name: "deploy", Status: "pending"},
		},
	}
	if b.Kind() != BlockKindSessionEvent {
		t.Error("expected session event kind")
	}
	lines := b.RenderLines(80, pal)
	if len(lines) < 5 {
		t.Errorf("expected at least 5 lines, got %d", len(lines))
	}
}

// --- #9 Notifications ---

func TestNotificationService(t *testing.T) {
	ns := NewNotificationService("test-app")
	ns.SetFocused(true) // focused → should not send

	ns.Notify(NotificationEvent{
		Kind:  NotifyTurnComplete,
		Title: "test",
		Body:  "done",
	})

	// Unfocused → should send (but we can't easily test OSC output)
	ns.SetFocused(false)
	ns.SetEnabled(false) // disabled → should not send
	ns.Notify(NotificationEvent{
		Kind:  NotifyTurnComplete,
		Title: "test",
		Body:  "done",
	})
}

func TestFormatNotificationBody(t *testing.T) {
	body := FormatNotificationBody(NotifyTurnComplete, "response ready")
	if body == "" {
		t.Error("expected non-empty body")
	}

	body = FormatNotificationBody(NotifyError, "something failed")
	if body == "" {
		t.Error("expected non-empty body")
	}
}

func TestSanitizeNotificationText(t *testing.T) {
	input := "hello\x1b[0m\nworld\r"
	result := sanitizeNotificationText(input)
	if result != "hello[0m world" {
		t.Errorf("unexpected sanitization: %q", result)
	}
}

// --- #10 Modal Window ---

func TestModalWindow(t *testing.T) {
	config := ModalWindowConfig{
		Title:     "Test Modal",
		Shortcuts: DefaultModalShortcuts(),
		WidthPct:  80,
		HeightPct: 70,
	}

	// Create with a simple component (SettingsList)
	entries := BuildDefaultSettingsEntries()
	modal := NewSettingsModal(entries)
	window := NewModalWindow(modal, config)

	if window == nil {
		t.Fatal("expected non-nil modal window")
	}

	// Render
	lines := window.Render(80)
	if len(lines) == 0 {
		t.Error("expected non-empty render")
	}

	// Should have title bar lines
	if len(lines) < 3 {
		t.Error("expected at least title + content + shortcut bar")
	}
}

func TestDefaultModalShortcuts(t *testing.T) {
	shortcuts := DefaultModalShortcuts()
	if len(shortcuts) != 3 {
		t.Errorf("expected 3 shortcuts, got %d", len(shortcuts))
	}
}

// --- Settings Modal Filter Mode ---

func TestSettingsModalFilterMode(t *testing.T) {
	entries := BuildDefaultSettingsEntries()
	modal := NewSettingsModal(entries)

	if modal.mode != SettingsModeBrowse {
		t.Error("expected browse mode initially")
	}

	// The modal should have all entries stored
	if len(modal.allEntries) != len(entries) {
		t.Errorf("expected %d allEntries, got %d", len(entries), len(modal.allEntries))
	}
}

func TestIsPrintableKey(t *testing.T) {
	if !isPrintableKey("a") {
		t.Error("'a' should be printable")
	}
	if !isPrintableKey("z") {
		t.Error("'z' should be printable")
	}
	if isPrintableKey("enter") {
		t.Error("'enter' should not be printable")
	}
	if isPrintableKey("esc") {
		t.Error("'esc' should not be printable")
	}
	if isPrintableKey("") {
		t.Error("empty string should not be printable")
	}
	if !isPrintableKey("中") {
		t.Error("multi-byte UTF-8 '中' should be printable")
	}
	if !isPrintableKey("中文") {
		t.Error("multi-byte UTF-8 '中文' should be printable")
	}
	if isPrintableKey("\x1b") {
		t.Error("escape byte should not be printable")
	}
}

func TestTrimLastRune(t *testing.T) {
	if got := trimLastRune("模式"); got != "模" {
		t.Errorf("expected '模', got %q", got)
	}
	if got := trimLastRune("ab"); got != "a" {
		t.Errorf("expected 'a', got %q", got)
	}
	if got := trimLastRune(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}
