package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNextCronRun(t *testing.T) {
	base := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)

	t.Run("@every 5m", func(t *testing.T) {
		next, err := nextCronRun("@every 5m", base)
		if err != nil {
			t.Fatal(err)
		}
		want := base.Add(5 * time.Minute)
		if !next.Equal(want) {
			t.Errorf("got %v, want %v", next, want)
		}
	})

	t.Run("@hourly", func(t *testing.T) {
		next, err := nextCronRun("@hourly", base)
		if err != nil {
			t.Fatal(err)
		}
		want := base.Add(1 * time.Hour)
		if !next.Equal(want) {
			t.Errorf("got %v, want %v", next, want)
		}
	})

	t.Run("@daily", func(t *testing.T) {
		next, err := nextCronRun("@daily", base)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		if !next.Equal(want) {
			t.Errorf("got %v, want %v", next, want)
		}
	})

	t.Run("@weekly", func(t *testing.T) {
		// 2026-05-31 is a Sunday
		next, err := nextCronRun("@weekly", base)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
		if !next.Equal(want) {
			t.Errorf("got %v, want %v", next, want)
		}
	})

	t.Run("@monthly", func(t *testing.T) {
		// From May 31, next month is July 1 (June 31 doesn't exist)
		next, err := nextCronRun("@monthly", base)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
		if !next.Equal(want) {
			t.Errorf("got %v, want %v", next, want)
		}
	})

	t.Run("invalid duration", func(t *testing.T) {
		_, err := nextCronRun("@every bad", base)
		if err == nil {
			t.Error("expected error for invalid duration")
		}
	})
}

func TestMatchesCronField(t *testing.T) {
	t.Run("wildcard", func(t *testing.T) {
		if !matchesCronField("*", 5, 0, 59) {
			t.Error("expected '*' to match any value")
		}
	})

	t.Run("exact", func(t *testing.T) {
		if !matchesCronField("5", 5, 0, 59) {
			t.Error("expected '5' to match 5")
		}
		if matchesCronField("5", 6, 0, 59) {
			t.Error("expected '5' not to match 6")
		}
	})

	t.Run("step", func(t *testing.T) {
		if !matchesCronField("*/5", 5, 0, 59) {
			t.Error("expected '*/5' to match 5")
		}
		if matchesCronField("*/5", 3, 0, 59) {
			t.Error("expected '*/5' not to match 3")
		}
	})

	t.Run("comma", func(t *testing.T) {
		if !matchesCronField("1,3,5", 3, 0, 59) {
			t.Error("expected '1,3,5' to match 3")
		}
		if matchesCronField("1,3,5", 2, 0, 59) {
			t.Error("expected '1,3,5' not to match 2")
		}
	})

	t.Run("range", func(t *testing.T) {
		if !matchesCronField("3-7", 5, 0, 59) {
			t.Error("expected '3-7' to match 5")
		}
		if matchesCronField("3-7", 8, 0, 59) {
			t.Error("expected '3-7' not to match 8")
		}
	})
}

func TestCronStoreToolsAllowAndRuns(t *testing.T) {
	dir := t.TempDir()
	store := NewCronStore(dir)
	job, err := store.CreateWithAllow("nightly", "summarize inbox", "@hourly", []string{"read", "web_fetch"})
	if err != nil {
		t.Fatal(err)
	}
	if len(job.ToolsAllow) != 2 {
		t.Fatalf("tools_allow=%v", job.ToolsAllow)
	}

	data, err := os.ReadFile(filepath.Join(dir, "cron", "jobs.json"))
	if err != nil {
		t.Fatal(err)
	}
	rawJobs := string(data)
	if !strings.Contains(rawJobs, `"tools_allow"`) || !strings.Contains(rawJobs, `"read"`) || !strings.Contains(rawJobs, `"web_fetch"`) {
		t.Fatalf("jobs.json missing tools_allow: %s", data)
	}

	now := time.Now()
	if err := store.FinishRun(job.ID, CronRunRecord{
		StartedAt: now,
		EndedAt:   now,
		Status:    "ok",
		Summary:   "done",
		SessionID: "cron-" + job.ID,
	}); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListRuns(job.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "ok" || runs[0].Summary != "done" {
		t.Fatalf("runs=%+v", runs)
	}

	for i := 0; i < 55; i++ {
		if err := store.FinishRun(job.ID, CronRunRecord{Status: "ok", Summary: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	runs, err = store.ListRuns(job.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != cronRunHistoryCap {
		t.Fatalf("expected cap %d, got %d", cronRunHistoryCap, len(runs))
	}
}

func TestCronCreateRejectsEmptyToolsAllowName(t *testing.T) {
	store := NewCronStore(t.TempDir())
	if _, err := store.CreateWithAllow("n", "p", "@hourly", []string{"read", ""}); err == nil {
		t.Fatal("expected error")
	}
}

func TestCronjobToolRunsAction(t *testing.T) {
	store := NewCronStore(t.TempDir())
	tool := buildCronjobTool(store)
	raw, _ := json.Marshal(map[string]any{
		"action":      "create",
		"name":        "n",
		"prompt":      "p",
		"schedule":    "@hourly",
		"tools_allow": []string{"read"},
	})
	got, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	id := m["job_id"].(string)
	_ = store.FinishRun(id, CronRunRecord{Status: "ok", Summary: "hello"})
	raw, _ = json.Marshal(map[string]any{"action": "runs", "job_id": id})
	got, err = tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	out := got.(map[string]any)
	if out["count"].(int) != 1 {
		t.Fatalf("count=%v", out["count"])
	}
}

func TestCronCreateWithOptions(t *testing.T) {
	store := NewCronStore(t.TempDir())
	job, err := store.CreateWithOptions(CronJobOptions{
		Name:        "nightly",
		Prompt:      "summarize",
		Schedule:    "@daily",
		Model:       "glm-5",
		Skills:      []string{" github-pr-review ", "tdd", "tdd"},
		PreRun:      "echo hi",
		ContextFrom: "job_other",
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.Model != "glm-5" || job.PreRun != "echo hi" || job.ContextFrom != "job_other" {
		t.Fatalf("overrides not stored: %+v", job)
	}
	if len(job.Skills) != 2 || job.Skills[0] != "github-pr-review" || job.Skills[1] != "tdd" {
		t.Fatalf("skills not normalized: %v", job.Skills)
	}
}

func TestCronSchedulerContextFromAndPreRun(t *testing.T) {
	store := NewCronStore(t.TempDir())
	src, err := store.Create("upstream", "do work", "@daily")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRun(src.ID, "upstream finished with 3 files changed"); err != nil {
		t.Fatal(err)
	}

	// Pre-run that fails must abort the run before the runner is invoked.
	failing, err := store.CreateWithOptions(CronJobOptions{
		Name: "blocked", Prompt: "never runs", Schedule: "@daily", PreRun: "exit 7",
	})
	if err != nil {
		t.Fatal(err)
	}

	var gotPrompt string
	var gotModel string
	scheduler := NewCronScheduler(store, func(ctx context.Context, job *CronJob) (string, error) {
		gotPrompt = job.Prompt
		gotModel = job.Model
		return "done", nil
	})

	chained, err := store.CreateWithOptions(CronJobOptions{
		Name: "chained", Prompt: "review the changes", Schedule: "@daily",
		Model: "glm-air", ContextFrom: src.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Force all three due in one tick by backdating NextRun.
	for _, j := range []*CronJob{failing, chained} {
		store.mu.Lock()
		j.NextRun = time.Now().Add(-time.Minute)
		store.mu.Unlock()
	}

	scheduler.runDueJobs(context.Background())

	if gotPrompt == "review the changes" || !strings.Contains(gotPrompt, "upstream finished with 3 files changed") {
		t.Fatalf("context_from not composed into prompt: %q", gotPrompt)
	}
	if gotModel != "glm-air" {
		t.Fatalf("model override not passed to runner: %q", gotModel)
	}

	incidents, err := store.ListIncidents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 1 || incidents[0].Status != "prerun_error" || incidents[0].JobID != failing.ID {
		t.Fatalf("expected one prerun_error incident for %s, got %+v", failing.ID, incidents)
	}
	blocked, _ := store.Get(failing.ID)
	if blocked.ConsecutiveErrors != 1 {
		t.Fatalf("expected consecutive_errors=1, got %d", blocked.ConsecutiveErrors)
	}

	// A success resets the counter.
	if err := store.FinishRun(failing.ID, CronRunRecord{Status: "ok", Summary: "fine"}); err != nil {
		t.Fatal(err)
	}
	blocked, _ = store.Get(failing.ID)
	if blocked.ConsecutiveErrors != 0 {
		t.Fatalf("expected consecutive_errors reset, got %d", blocked.ConsecutiveErrors)
	}
}

func TestCronSchedulerDanglingContextFrom(t *testing.T) {
	store := NewCronStore(t.TempDir())
	job, err := store.CreateWithOptions(CronJobOptions{
		Name: "solo", Prompt: "standalone", Schedule: "@daily", ContextFrom: "job_missing",
	})
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	job.NextRun = time.Now().Add(-time.Minute)
	store.mu.Unlock()

	var gotPrompt string
	scheduler := NewCronScheduler(store, func(ctx context.Context, job *CronJob) (string, error) {
		gotPrompt = job.Prompt
		return "ok", nil
	})
	scheduler.runDueJobs(context.Background())
	if gotPrompt != "standalone" {
		t.Fatalf("dangling context_from should run bare prompt, got %q", gotPrompt)
	}
}
