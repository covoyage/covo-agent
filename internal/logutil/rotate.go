package logutil

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	LiveLogName       = "covo-agent.log"
	datedLogLayout    = "covo-agent-2006-01-02.log"
	DefaultRetainDays = 7
)

// now is replaced in tests.
var now = time.Now

// File appends to ~/.covo-agent/covo-agent.log and rotates it to a dated
// sibling at local midnight. Dated files older than retainDays are deleted.
type File struct {
	dir        string
	retainDays int
	mu         sync.Mutex
	file       *os.File
	day        string
}

// ResolveRetainDays returns the log retention in days.
// Priority: COVO_LOG_RETAIN_DAYS, then cfgDays, then 7. Values < 1 fall back to 7.
func ResolveRetainDays(cfgDays int) int {
	if v := strings.TrimSpace(os.Getenv("COVO_LOG_RETAIN_DAYS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if cfgDays > 0 {
		return cfgDays
	}
	return DefaultRetainDays
}

// AppLogPath is the live log file under homeDir.
func AppLogPath(homeDir string) string {
	return filepath.Join(homeDir, LiveLogName)
}

// OpenAppLog opens a rotating writer for the application log.
func OpenAppLog(homeDir string, retainDays int) (*File, error) {
	if retainDays < 1 {
		retainDays = DefaultRetainDays
	}
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	f := &File{dir: homeDir, retainDays: retainDays}
	if err := f.reopenLocked(now()); err != nil {
		return nil, err
	}
	f.pruneLocked(now())
	return f, nil
}

func (f *File) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := now()
	if f.file == nil || t.Format("2006-01-02") != f.day {
		if err := f.reopenLocked(t); err != nil {
			return 0, err
		}
		f.pruneLocked(t)
	}
	return f.file.Write(p)
}

func (f *File) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.file == nil {
		return nil
	}
	err := f.file.Close()
	f.file = nil
	return err
}

func (f *File) reopenLocked(t time.Time) error {
	today := t.Format("2006-01-02")
	live := filepath.Join(f.dir, LiveLogName)
	if f.file != nil {
		_ = f.file.Close()
		f.file = nil
		if f.day != "" && f.day != today {
			_ = os.Rename(live, filepath.Join(f.dir, timeFromDay(f.day).Format(datedLogLayout)))
		}
	} else if info, err := os.Stat(live); err == nil && !info.IsDir() {
		if day := info.ModTime().In(t.Location()).Format("2006-01-02"); day != today {
			_ = os.Rename(live, filepath.Join(f.dir, timeFromDay(day).Format(datedLogLayout)))
		}
	}
	file, err := os.OpenFile(live, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	f.file = file
	f.day = today
	return nil
}

func (f *File) pruneLocked(t time.Time) {
	y, m, d := t.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, t.Location())
	cutoff := today.AddDate(0, 0, 1-f.retainDays)
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		name := ent.Name()
		day, ok := parseDatedLogName(name)
		if !ok {
			continue
		}
		if !day.Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(f.dir, name))
	}
}

func timeFromDay(day string) time.Time {
	t, err := time.ParseInLocation("2006-01-02", day, time.Local)
	if err != nil {
		return now()
	}
	return t
}

func parseDatedLogName(name string) (time.Time, bool) {
	const prefix = "covo-agent-"
	const suffix = ".log"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return time.Time{}, false
	}
	day := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	t, err := time.ParseInLocation("2006-01-02", day, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

var _ io.WriteCloser = (*File)(nil)
