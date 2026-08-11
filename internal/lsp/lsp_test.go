package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewManager(t *testing.T) {
	m := NewManager(ManagerConfig{Enabled: false})
	if m == nil {
		t.Fatal("expected non-nil Manager")
	}
	if m.IsActive() {
		t.Error("expected inactive when Enabled=false")
	}

	m2 := NewManager(ManagerConfig{Enabled: true})
	if !m2.IsActive() {
		t.Error("expected active when Enabled=true")
	}
}

func TestManager_EnableDisable(t *testing.T) {
	m := NewManager(ManagerConfig{Enabled: false})
	m.Enable()
	if !m.IsActive() {
		t.Error("expected active after Enable()")
	}
	m.Disable()
	if m.IsActive() {
		t.Error("expected inactive after Disable()")
	}
}

func TestManager_GetDiagnosticsForFile_Disabled(t *testing.T) {
	m := NewManager(ManagerConfig{Enabled: false})
	diags, err := m.GetDiagnosticsForFile("/tmp/test.go", 0)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if diags != nil {
		t.Errorf("expected nil diags, got %v", diags)
	}
}

func TestManager_GetDiagnosticsForFile_NoServer(t *testing.T) {
	m := NewManager(ManagerConfig{Enabled: true})
	// .unknown extension has no server registered
	diags, err := m.GetDiagnosticsForFile("/tmp/test.unknownext", 0)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if diags != nil {
		t.Errorf("expected nil diags, got %v", diags)
	}
}

func TestManager_Shutdown(t *testing.T) {
	m := NewManager(ManagerConfig{Enabled: true})
	m.Shutdown()
	// Should not panic
}

func TestManager_ReapIdle(t *testing.T) {
	m := NewManager(ManagerConfig{Enabled: true, IdleTimeout: 1})
	m.ReapIdle()
	// Should not panic with no clients
}

func TestManager_DefaultIdleTimeout(t *testing.T) {
	m := NewManager(ManagerConfig{Enabled: true})
	if m.idleTimeout != defaultIdleTimeout {
		t.Fatalf("idleTimeout = %s, want %s", m.idleTimeout, defaultIdleTimeout)
	}
}

func TestFormatDiagnostic(t *testing.T) {
	d := Diagnostic{
		Severity: 1,
		Range:    Range{Start: Position{Line: 5, Character: 10}},
		Message:  "undefined variable",
		Source:   "gopls",
		Code:     "x",
	}
	got := FormatDiagnostic(d)
	if got == "" {
		t.Error("expected non-empty formatted diagnostic")
	}
	// Line should be 6 (0-indexed + 1)
	if !containsStr(got, "[6:11]") {
		t.Errorf("expected [6:11] in output, got %q", got)
	}
}

func TestFormatDiagnostic_DefaultSeverity(t *testing.T) {
	d := Diagnostic{
		Severity: 0,
		Range:    Range{Start: Position{Line: 0, Character: 0}},
		Message:  "error",
	}
	got := FormatDiagnostic(d)
	if !containsStr(got, "ERROR") {
		t.Errorf("expected ERROR for severity 0, got %q", got)
	}
}

func TestReportForFile_Empty(t *testing.T) {
	got := ReportForFile("/tmp/test.go", nil, nil)
	if got != "" {
		t.Errorf("expected empty string for no diagnostics, got %q", got)
	}
}

func TestReportForFile_WithDiagnostics(t *testing.T) {
	diags := []Diagnostic{
		{Severity: 1, Range: Range{Start: Position{Line: 0, Character: 0}}, Message: "error 1"},
		{Severity: 2, Range: Range{Start: Position{Line: 1, Character: 5}}, Message: "warning 1"},
	}
	got := ReportForFile("/tmp/test.go", diags, nil)
	if got == "" {
		t.Error("expected non-empty report")
	}
	if !containsStr(got, "error 1") {
		t.Errorf("expected 'error 1' in report, got %q", got)
	}
}

func TestReportForFile_SeverityFilter(t *testing.T) {
	diags := []Diagnostic{
		{Severity: 1, Range: Range{Start: Position{Line: 0, Character: 0}}, Message: "error"},
		{Severity: 2, Range: Range{Start: Position{Line: 1, Character: 0}}, Message: "warning"},
	}
	// Filter to only severity 1
	got := ReportForFile("/tmp/test.go", diags, []int{1})
	if !containsStr(got, "error") {
		t.Errorf("expected 'error' in report, got %q", got)
	}
	if containsStr(got, "warning") {
		t.Errorf("expected 'warning' to be filtered out, got %q", got)
	}
}

func TestReportForFile_Truncation(t *testing.T) {
	var diags []Diagnostic
	for i := 0; i < 500; i++ {
		diags = append(diags, Diagnostic{
			Severity: 1,
			Range:    Range{Start: Position{Line: i, Character: 0}},
			Message:  strings.Repeat("x", 250),
		})
	}
	got := ReportForFile("/tmp/test.go", diags, nil)
	if len(got) > maxTotalChars {
		t.Errorf("expected report <= %d chars, got %d", maxTotalChars, len(got))
	}
	if !containsStr(got, "truncated") {
		t.Errorf("expected truncation marker, got %q", got[:min(200, len(got))])
	}
}

func TestTruncate(t *testing.T) {
	short := "hello"
	if got := Truncate(short, 100); got != short {
		t.Errorf("expected %q, got %q", short, got)
	}

	long := string(make([]byte, 5000))
	got := Truncate(long, 1000)
	if len(got) > 1000 {
		t.Errorf("expected <= 1000 chars, got %d", len(got))
	}
}

func TestResolveWorkspaceForFile(t *testing.T) {
	// Create a temp dir with a go.mod
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module test"), 0644); err != nil {
		t.Fatal(err)
	}

	ClearWorkspaceCache()
	root := ResolveWorkspaceForFile(testFile, []string{"go.mod"})
	if root != tmpDir {
		t.Errorf("expected %q, got %q", tmpDir, root)
	}
}

func TestResolveWorkspaceForFile_NoRootPattern(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	ClearWorkspaceCache()
	root := ResolveWorkspaceForFile(testFile, []string{"nonexistent.pattern"})
	// Should fall back to file's parent dir
	if root == "" {
		t.Error("expected non-empty workspace root")
	}
}

func TestEncodeMessage(t *testing.T) {
	req := Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	}
	data, err := encodeMessage(req)
	if err != nil {
		t.Fatalf("encodeMessage() error: %v", err)
	}
	s := string(data)
	if !containsStr(s, "Content-Length:") {
		t.Error("expected Content-Length header")
	}
	if !containsStr(s, "initialize") {
		t.Error("expected method in body")
	}
}

func TestClassifyMessage(t *testing.T) {
	// Request
	req := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	kind, id, method, err := classifyMessage([]byte(req))
	if err != nil || kind != "request" || id != 1 || method != "initialize" {
		t.Errorf("classifyMessage(request) = %q,%d,%q,%v", kind, id, method, err)
	}

	// Notification
	notif := `{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{}}`
	kind, id, method, err = classifyMessage([]byte(notif))
	if err != nil || kind != "notification" || method != "textDocument/publishDiagnostics" {
		t.Errorf("classifyMessage(notif) = %q,%d,%q,%v", kind, id, method, err)
	}

	// Response
	resp := `{"jsonrpc":"2.0","id":2,"result":{}}`
	kind, id, method, err = classifyMessage([]byte(resp))
	if err != nil || kind != "response" || id != 2 {
		t.Errorf("classifyMessage(response) = %q,%d,%q,%v", kind, id, method, err)
	}

	// Error
	errResp := `{"jsonrpc":"2.0","id":3,"error":{"code":-32601,"message":"method not found"}}`
	kind, id, method, err = classifyMessage([]byte(errResp))
	if err != nil || kind != "error" || id != 3 || method != "method not found" {
		t.Errorf("classifyMessage(error) = %q,%d,%q,%v", kind, id, method, err)
	}
}

func TestFileURI(t *testing.T) {
	got := fileURI("/tmp/test.go")
	if !containsStr(got, "file://") {
		t.Errorf("expected file:// URI, got %q", got)
	}
	if !containsStr(got, "/tmp/test.go") {
		t.Errorf("expected path in URI, got %q", got)
	}
}

func TestURIToPath(t *testing.T) {
	got := uriToPath("file:///tmp/test.go")
	if got != "/tmp/test.go" {
		t.Errorf("expected /tmp/test.go, got %q", got)
	}
}

func TestLSPError_Error(t *testing.T) {
	e := &LSPError{Code: -32601, Message: "method not found"}
	got := e.Error()
	if !containsStr(got, "-32601") {
		t.Errorf("expected code in error, got %q", got)
	}
	if !containsStr(got, "method not found") {
		t.Errorf("expected message in error, got %q", got)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func containsStr(s, substr string) bool {
	return strings.Contains(s, substr)
}

// --- filterByBaseline tests ---

func TestFilterByBaseline_NilBaseline(t *testing.T) {
	diags := []Diagnostic{
		{Severity: 1, Range: Range{Start: Position{Line: 0, Character: 0}}, Message: "error 1"},
		{Severity: 1, Range: Range{Start: Position{Line: 1, Character: 0}}, Message: "error 2"},
	}
	result := filterByBaseline(diags, nil)
	if len(result) != 2 {
		t.Errorf("nil baseline should return all diags, got %d (want 2)", len(result))
	}
}

func TestFilterByBaseline_EmptyBaseline(t *testing.T) {
	diags := []Diagnostic{
		{Severity: 1, Range: Range{Start: Position{Line: 0, Character: 0}}, Message: "error 1"},
	}
	result := filterByBaseline(diags, map[string][]Diagnostic{})
	if len(result) != 1 {
		t.Errorf("empty baseline should return all diags, got %d (want 1)", len(result))
	}
}

func TestFilterByBaseline_AllMatch(t *testing.T) {
	diags := []Diagnostic{
		{Severity: 1, Range: Range{Start: Position{Line: 0, Character: 0}}, Message: "old error"},
	}
	baseline := map[string][]Diagnostic{
		"0:0:old error": {diags[0]},
	}
	result := filterByBaseline(diags, baseline)
	if len(result) != 0 {
		t.Errorf("all matching should return empty, got %d", len(result))
	}
}

func TestFilterByBaseline_PartialMatch(t *testing.T) {
	diags := []Diagnostic{
		{Severity: 1, Range: Range{Start: Position{Line: 0, Character: 0}}, Message: "old error"},
		{Severity: 1, Range: Range{Start: Position{Line: 5, Character: 10}}, Message: "new error"},
	}
	baseline := map[string][]Diagnostic{
		"0:0:old error": {diags[0]},
	}
	result := filterByBaseline(diags, baseline)
	if len(result) != 1 {
		t.Fatalf("partial match should return 1, got %d", len(result))
	}
	if result[0].Message != "new error" {
		t.Errorf("expected 'new error', got %q", result[0].Message)
	}
}

func TestFilterByBaseline_ShiftedLineIsNew(t *testing.T) {
	// If an old error's line changed (e.g. code inserted above), the key
	// changes and it should appear as "new" — this is correct behavior
	// because the agent needs to know the error moved.
	diags := []Diagnostic{
		{Severity: 1, Range: Range{Start: Position{Line: 3, Character: 0}}, Message: "shifted error"},
	}
	baseline := map[string][]Diagnostic{
		"0:0:shifted error": {diags[0]},
	}
	result := filterByBaseline(diags, baseline)
	if len(result) != 1 {
		t.Errorf("shifted line should be treated as new, got %d (want 1)", len(result))
	}
}
