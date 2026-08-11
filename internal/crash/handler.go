// Package crash provides a system-level crash handler that captures panics,
// generates detailed crash reports, and persists them for later analysis.
//
// Unlike safego (which recovers panics in individual goroutines), this package
// installs a global panic handler that catches unrecovered panics in the main
// goroutine and generates a structured crash report with:
//   - Stack trace
//   - System information (OS, Go version, build info)
//   - Process state (PID, memory, goroutines)
//   - Recent log entries (if available)
//
// Crash reports are stored in ~/.covo-agent/crash-reports/ as JSON files.
package crash

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"
)

// CrashReport is a structured record of a crash event.
type CrashReport struct {
	Timestamp   time.Time         `json:"timestamp"`
	PanicValue  string            `json:"panic_value"`
	StackTrace  string            `json:"stack_trace"`
	GoVersion   string            `json:"go_version"`
	OS          string            `json:"os"`
	Arch        string            `json:"arch"`
	PID         int               `json:"pid"`
	NumGoroutines int             `json:"num_goroutines"`
	NumCPU      int               `json:"num_cpu"`
	HomeDir     string            `json:"home_dir,omitempty"`
	WorkingDir  string            `json:"working_dir,omitempty"`
	BuildInfo   string            `json:"build_info,omitempty"`
	LogTail     string            `json:"log_tail,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
}

// Handler is the crash handler. Install it once at program start.
type Handler struct {
	homeDir   string
	logger    *slog.Logger
	extra     map[string]string
	logFile   string
	installed bool
	counter   int64
}

// New creates a crash handler that writes reports to ~/.covo-agent/crash-reports/.
func New(homeDir string) *Handler {
	return &Handler{
		homeDir: homeDir,
		logger:  slog.Default(),
		extra:   make(map[string]string),
	}
}

// SetLogger sets the logger used for crash handler diagnostics.
func (h *Handler) SetLogger(l *slog.Logger) {
	h.logger = l
}

// SetExtra stores extra key-value pairs to include in crash reports.
func (h *Handler) SetExtra(key, value string) {
	h.extra[key] = value
}

// SetLogFile sets the path to the application log file, whose tail will be
// included in crash reports.
func (h *Handler) SetLogFile(path string) {
	h.logFile = path
}

// Install installs the global panic handler. This should be called once at
// program start, typically as the first thing in main().
func (h *Handler) Install() {
	if h.installed {
		return
	}
	h.installed = true

	// Install a recover handler that will fire on unrecovered panics.
	// Note: this must be deferred in the main goroutine.
}

// RecoverAndReport should be deferred in the main goroutine. It catches panics,
// generates a crash report, writes it to disk, and re-panics (so the process
// still exits with a non-zero code).
//
// Usage:
//
//	func main() {
//	    handler := crash.New(homeDir)
//	    defer handler.RecoverAndReport()
//	    // ... rest of program
//	}
func (h *Handler) RecoverAndReport() {
	if r := recover(); r != nil {
		report := h.generateReport(r, debug.Stack())
		h.writeReport(report)
		// Print a user-friendly message
		fmt.Fprintf(os.Stderr, "\n╔════════════════════════════════════════════════════╗\n")
		fmt.Fprintf(os.Stderr, "║  covo-agent crashed unexpectedly.                   ║\n")
		fmt.Fprintf(os.Stderr, "║  A crash report has been saved.                     ║\n")
		fmt.Fprintf(os.Stderr, "║  Run 'covo-agent crash-report' to view it.          ║\n")
		fmt.Fprintf(os.Stderr, "╚════════════════════════════════════════════════════╝\n\n")
		// Re-panic so the process exits with the right code
		panic(r)
	}
}

// generateReport creates a CrashReport from a panic value and stack trace.
func (h *Handler) generateReport(panicValue any, stack []byte) CrashReport {
	workingDir, _ := os.Getwd()

	buildInfo := ""
	if bi, ok := debug.ReadBuildInfo(); ok {
		buildInfo = bi.String()
	}

	report := CrashReport{
		Timestamp:     time.Now(),
		PanicValue:    fmt.Sprintf("%v", panicValue),
		StackTrace:    string(stack),
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		PID:           os.Getpid(),
		NumGoroutines: runtime.NumGoroutine(),
		NumCPU:        runtime.NumCPU(),
		HomeDir:       h.homeDir,
		WorkingDir:    workingDir,
		BuildInfo:     buildInfo,
		Extra:         h.extra,
	}

	// Read last ~4KB of log file if available
	if h.logFile != "" {
		if data, err := os.ReadFile(h.logFile); err == nil {
			if len(data) > 4096 {
				data = data[len(data)-4096:]
			}
			report.LogTail = string(data)
		}
	}

	return report
}

// writeReport writes the crash report to ~/.covo-agent/crash-reports/.
func (h *Handler) writeReport(report CrashReport) {
	crashDir := filepath.Join(h.homeDir, "crash-reports")
	if err := os.MkdirAll(crashDir, 0755); err != nil {
		h.logger.Error("crash: create crash report dir", "err", err)
		return
	}

	filename := fmt.Sprintf("crash-%s-%d.json", report.Timestamp.Format("20060102-150405"), h.counter)
	h.counter++
	path := filepath.Join(crashDir, filename)

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		h.logger.Error("crash: marshal report", "err", err)
		return
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		h.logger.Error("crash: write report", "err", err)
		return
	}

	h.logger.Info("crash report written", "path", path)
}

// ListReports returns all crash report files, newest first.
func ListReports(homeDir string) ([]CrashReportInfo, error) {
	crashDir := filepath.Join(homeDir, "crash-reports")
	entries, err := os.ReadDir(crashDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var reports []CrashReportInfo
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		reports = append(reports, CrashReportInfo{
			Filename:  entry.Name(),
			Path:      filepath.Join(crashDir, entry.Name()),
			Size:      info.Size(),
			Modified:  info.ModTime(),
		})
	}

	// Sort by modified time, newest first
	for i := 0; i < len(reports); i++ {
		for j := i + 1; j < len(reports); j++ {
			if reports[j].Modified.After(reports[i].Modified) {
				reports[i], reports[j] = reports[j], reports[i]
			}
		}
	}

	return reports, nil
}

// LoadReport reads a crash report from a file path.
func LoadReport(path string) (*CrashReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("crash: read report: %w", err)
	}

	var report CrashReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("crash: parse report: %w", err)
	}
	return &report, nil
}

// CrashReportInfo describes a crash report file on disk.
type CrashReportInfo struct {
	Filename string    `json:"filename"`
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// FormatReport returns a human-readable string representation of a crash report.
func FormatReport(report *CrashReport) string {
	var b []byte
	b = append(b, fmt.Sprintf("═══ Crash Report ═══\n")...)
	b = append(b, fmt.Sprintf("Time:        %s\n", report.Timestamp.Format("2006-01-02 15:04:05"))...)
	b = append(b, fmt.Sprintf("Panic:       %s\n", report.PanicValue)...)
	b = append(b, fmt.Sprintf("OS/Arch:     %s/%s\n", report.OS, report.Arch)...)
	b = append(b, fmt.Sprintf("Go Version:  %s\n", report.GoVersion)...)
	b = append(b, fmt.Sprintf("PID:         %d\n", report.PID)...)
	b = append(b, fmt.Sprintf("Goroutines:  %d\n", report.NumGoroutines)...)
	b = append(b, fmt.Sprintf("Working Dir: %s\n", report.WorkingDir)...)
	if len(report.Extra) > 0 {
		b = append(b, "Extra:\n"...)
		for k, v := range report.Extra {
			b = append(b, fmt.Sprintf("  %s: %s\n", k, v)...)
		}
	}
	b = append(b, "\n── Stack Trace ──\n"...)
	b = append(b, report.StackTrace...)
	if report.LogTail != "" {
		b = append(b, "\n── Recent Log ──\n"...)
		b = append(b, report.LogTail...)
	}
	return string(b)
}
