package logutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveRetainDays(t *testing.T) {
	t.Setenv("COVO_LOG_RETAIN_DAYS", "")
	if got := ResolveRetainDays(0); got != 7 {
		t.Fatalf("default: got %d", got)
	}
	if got := ResolveRetainDays(14); got != 14 {
		t.Fatalf("cfg: got %d", got)
	}
	t.Setenv("COVO_LOG_RETAIN_DAYS", "3")
	if got := ResolveRetainDays(14); got != 3 {
		t.Fatalf("env should win: got %d", got)
	}
	t.Setenv("COVO_LOG_RETAIN_DAYS", "0")
	if got := ResolveRetainDays(14); got != 14 {
		t.Fatalf("invalid env should fall back to cfg: got %d", got)
	}
}

func TestRotatingFile_WritesLiveLog(t *testing.T) {
	dir := t.TempDir()
	f, err := OpenAppLog(dir, 7)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if _, err := f.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, LiveLogName))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("live log = %q", data)
	}
}

func TestRotatingFile_RotatesAndPrunes(t *testing.T) {
	dir := t.TempDir()
	day := time.Date(2026, 9, 18, 10, 0, 0, 0, time.Local)
	now = func() time.Time { return day }
	t.Cleanup(func() { now = time.Now })

	f, err := OpenAppLog(dir, 7)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("day18\n")); err != nil {
		t.Fatal(err)
	}

	now = func() time.Time { return day.Add(24 * time.Hour) }
	if _, err := f.Write([]byte("day19\n")); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	rotated := filepath.Join(dir, "covo-agent-2026-09-18.log")
	got, err := os.ReadFile(rotated)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "day18\n" {
		t.Fatalf("rotated = %q", got)
	}
	live, err := os.ReadFile(filepath.Join(dir, LiveLogName))
	if err != nil {
		t.Fatal(err)
	}
	if string(live) != "day19\n" {
		t.Fatalf("live = %q", live)
	}

	old := filepath.Join(dir, "covo-agent-2026-09-10.log")
	if err := os.WriteFile(old, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "covo-agent-2026-09-13.log")
	if err := os.WriteFile(keep, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, err = OpenAppLog(dir, 7)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("expected 09-10 to be pruned, err=%v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("expected 09-13 to be kept: %v", err)
	}
}
