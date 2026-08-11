package crash

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
	"time"
)

func TestGenerateReport(t *testing.T) {
	h := New(t.TempDir())
	report := h.generateReport("test panic", debug.Stack())

	if report.PanicValue != "test panic" {
		t.Errorf("expected 'test panic', got %q", report.PanicValue)
	}
	if report.StackTrace == "" {
		t.Error("expected non-empty stack trace")
	}
	if report.OS == "" {
		t.Error("expected non-empty OS")
	}
	if report.PID == 0 {
		t.Error("expected non-zero PID")
	}
}

func TestWriteReport(t *testing.T) {
	dir := t.TempDir()
	h := New(dir)

	report := CrashReport{
		Timestamp:     time.Now(),
		PanicValue:    "test panic",
		StackTrace:    "goroutine 1 [running]:\nmain.main()",
		GoVersion:     "go1.25.0",
		OS:            "darwin",
		Arch:          "arm64",
		PID:           12345,
		NumGoroutines: 10,
		NumCPU:        8,
	}

	h.writeReport(report)

	// Verify the file was written
	crashDir := filepath.Join(dir, "crash-reports")
	entries, err := os.ReadDir(crashDir)
	if err != nil {
		t.Fatalf("read crash dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 report file, got %d", len(entries))
	}

	// Verify it's valid JSON
	data, _ := os.ReadFile(filepath.Join(crashDir, entries[0].Name()))
	var loaded CrashReport
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if loaded.PanicValue != "test panic" {
		t.Errorf("expected 'test panic', got %q", loaded.PanicValue)
	}
}

func TestListReports(t *testing.T) {
	dir := t.TempDir()
	h := New(dir)

	// Write two reports
	h.writeReport(CrashReport{Timestamp: time.Now(), PanicValue: "first"})
	h.writeReport(CrashReport{Timestamp: time.Now(), PanicValue: "second"})

	reports, err := ListReports(dir)
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("expected 2 reports, got %d", len(reports))
	}
}

func TestListReports_Empty(t *testing.T) {
	reports, err := ListReports(t.TempDir())
	if err != nil {
		t.Fatalf("ListReports on empty dir: %v", err)
	}
	if reports != nil {
		t.Fatalf("expected nil, got %v", reports)
	}
}

func TestLoadReport(t *testing.T) {
	dir := t.TempDir()
	h := New(dir)

	original := CrashReport{
		Timestamp:  time.Now(),
		PanicValue: "load test",
		StackTrace: "stack here",
		OS:         "linux",
	}
	h.writeReport(original)

	reports, _ := ListReports(dir)
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}

	loaded, err := LoadReport(reports[0].Path)
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}
	if loaded.PanicValue != "load test" {
		t.Errorf("expected 'load test', got %q", loaded.PanicValue)
	}
	if loaded.OS != "linux" {
		t.Errorf("expected 'linux', got %q", loaded.OS)
	}
}

func TestFormatReport(t *testing.T) {
	report := &CrashReport{
		Timestamp:  time.Now(),
		PanicValue: "format test",
		StackTrace: "stack trace here",
		OS:         "darwin",
		Arch:       "arm64",
		GoVersion:  "go1.25.0",
		PID:        42,
	}

	formatted := FormatReport(report)
	if formatted == "" {
		t.Error("expected non-empty formatted report")
	}
}

func TestSetExtra(t *testing.T) {
	h := New(t.TempDir())
	h.SetExtra("session", "abc123")
	h.SetExtra("model", "gpt-4")

	report := h.generateReport("test", debug.Stack())

	if report.Extra["session"] != "abc123" {
		t.Errorf("expected 'abc123', got %q", report.Extra["session"])
	}
	if report.Extra["model"] != "gpt-4" {
		t.Errorf("expected 'gpt-4', got %q", report.Extra["model"])
	}
}

func TestRecoverAndReport_NoPanic(t *testing.T) {
	h := New(t.TempDir())
	// Should not do anything if no panic occurs
	h.RecoverAndReport()

	// No crash report should be written
	reports, _ := ListReports(h.homeDir)
	if len(reports) != 0 {
		t.Errorf("expected 0 reports, got %d", len(reports))
	}
}
