package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covonaut/agentcore"
)

// CronJob represents a scheduled job.
type CronJob struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Prompt     string    `json:"prompt"`
	Schedule   string    `json:"schedule"` // cron expression: "*/5 * * * *" or "@daily", "@hourly", etc.
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	LastRun    time.Time `json:"last_run,omitempty"`
	NextRun    time.Time `json:"next_run,omitempty"`
	RunCount   int       `json:"run_count"`
	LastResult string    `json:"last_result,omitempty"`
}

// CronStore manages persistent cron jobs.
type CronStore struct {
	mu       sync.RWMutex
	filePath string
	jobs     map[string]*CronJob
}

// NewCronStore creates a new cron job store backed by a JSON file.
func NewCronStore(homeDir string) *CronStore {
	dir := filepath.Join(homeDir, "cron")
	_ = os.MkdirAll(dir, 0755)
	return &CronStore{
		filePath: filepath.Join(dir, "jobs.json"),
		jobs:     make(map[string]*CronJob),
	}
}

// Load reads jobs from disk.
func (s *CronStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var jobs []*CronJob
	if err := json.Unmarshal(data, &jobs); err != nil {
		return err
	}
	for _, j := range jobs {
		s.jobs[j.ID] = j
	}
	return nil
}

func (s *CronStore) save() error {
	var jobs []*CronJob
	for _, j := range s.jobs {
		jobs = append(jobs, j)
	}
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0644)
}

// Create adds a new job.
func (s *CronStore) Create(name, prompt, schedule string) (*CronJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := fmt.Sprintf("job_%d", time.Now().UnixNano())
	nextRun, err := nextCronRun(schedule, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid schedule %q: %w", schedule, err)
	}

	job := &CronJob{
		ID:        id,
		Name:      name,
		Prompt:    prompt,
		Schedule:  schedule,
		Enabled:   true,
		CreatedAt: time.Now(),
		NextRun:   nextRun,
	}
	s.jobs[id] = job
	return job, s.save()
}

// List returns all jobs.
func (s *CronStore) List() []*CronJob {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var jobs []*CronJob
	for _, j := range s.jobs {
		jobs = append(jobs, j)
	}
	return jobs
}

// Get returns a job by ID.
func (s *CronStore) Get(id string) (*CronJob, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.jobs[id]
	return j, ok
}

// Enable sets a job as enabled.
func (s *CronStore) Enable(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("job %q not found", id)
	}
	j.Enabled = true
	nextRun, _ := nextCronRun(j.Schedule, time.Now())
	j.NextRun = nextRun
	return s.save()
}

// Disable sets a job as disabled.
func (s *CronStore) Disable(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("job %q not found", id)
	}
	j.Enabled = false
	return s.save()
}

// Remove deletes a job.
func (s *CronStore) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return fmt.Errorf("job %q not found", id)
	}
	delete(s.jobs, id)
	return s.save()
}

// RecordRun updates a job after execution.
func (s *CronStore) RecordRun(id, result string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("job %q not found", id)
	}
	j.LastRun = time.Now()
	j.RunCount++
	j.LastResult = result
	nextRun, _ := nextCronRun(j.Schedule, time.Now())
	j.NextRun = nextRun
	return s.save()
}

// DueJobs returns enabled jobs that are past their next_run time.
func (s *CronStore) DueJobs() []*CronJob {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	var due []*CronJob
	for _, j := range s.jobs {
		if j.Enabled && !j.NextRun.IsZero() && now.After(j.NextRun) {
			due = append(due, j)
		}
	}
	return due
}

// CronRunner is a callback that executes a cron job's prompt.
type CronRunner func(ctx context.Context, jobID, prompt string) (string, error)

// CronScheduler runs due jobs in the background.
type CronScheduler struct {
	store  *CronStore
	runner CronRunner
	cancel context.CancelFunc
}

// NewCronScheduler creates a scheduler that checks for due jobs every 30s.
func NewCronScheduler(store *CronStore, runner CronRunner) *CronScheduler {
	return &CronScheduler{store: store, runner: runner}
}

// Start begins the background scheduler.
func (s *CronScheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	safego.SafeGo(func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runDueJobs(ctx)
			}
		}
	}, nil)
}

// Stop halts the scheduler.
func (s *CronScheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *CronScheduler) runDueJobs(ctx context.Context) {
	due := s.store.DueJobs()
	for _, job := range due {
		if s.runner == nil {
			continue
		}
		result, err := s.runner(ctx, job.ID, job.Prompt)
		status := "ok"
		if err != nil {
			status = fmt.Sprintf("error: %v", err)
		} else if len(result) > 500 {
			status = result[:500] + "..."
		} else {
			status = result
		}
		_ = s.store.RecordRun(job.ID, status)
	}
}

// nextCronRun calculates the next run time for a cron expression.
// Supports: "@every 5m", "@hourly", "@daily", "@weekly", "@monthly",
// 5-field cron, and RFC 5545 RRULE.
func nextCronRun(expr string, from time.Time) (time.Time, error) {
	expr = strings.TrimSpace(expr)

	// Handle @every duration
	if strings.HasPrefix(expr, "@every ") {
		dur, err := time.ParseDuration(strings.TrimPrefix(expr, "@every "))
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid duration: %w", err)
		}
		return from.Add(dur), nil
	}

	// Handle shorthand macros
	switch expr {
	case "@hourly":
		return from.Add(1 * time.Hour), nil
	case "@daily", "@midnight":
		tomorrow := from.AddDate(0, 0, 1)
		return time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 0, 0, 0, 0, from.Location()), nil
	case "@weekly":
		daysUntilSunday := (7 - int(from.Weekday())) % 7
		if daysUntilSunday == 0 {
			daysUntilSunday = 7
		}
		return from.AddDate(0, 0, daysUntilSunday).Truncate(24 * time.Hour), nil
	case "@monthly":
		next := from.AddDate(0, 1, 0)
		return time.Date(next.Year(), next.Month(), 1, 0, 0, 0, 0, from.Location()), nil
	}

	// Parse RFC 5545 RRULE: FREQ=DAILY;INTERVAL=1;BYDAY=MO,WE,FR
	if strings.HasPrefix(strings.ToUpper(expr), "FREQ=") {
		return nextRRuleRun(expr, from)
	}

	// Parse 5-field cron: minute hour day month weekday
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return time.Time{}, fmt.Errorf("expected 5-field cron expression, got %d fields", len(parts))
	}

	// Simple next-run: walk forward minute by minute (max 2 days)
	t := from.Add(1 * time.Minute).Truncate(time.Minute)
	for i := 0; i < 2880; i++ { // max 2 days
		if matchesCronField(parts[0], t.Minute(), 0, 59) &&
			matchesCronField(parts[1], t.Hour(), 0, 23) &&
			matchesCronField(parts[2], t.Day(), 1, 31) &&
			matchesCronField(parts[3], int(t.Month()), 1, 12) &&
			matchesCronField(parts[4], int(t.Weekday()), 0, 6) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("could not find next run time within 48 hours")
}

func matchesCronField(field string, value, min, max int) bool {
	if field == "*" {
		return true
	}
	// Handle */N step
	if strings.HasPrefix(field, "*/") {
		step := 0
		fmt.Sscanf(field, "*/%d", &step)
		if step <= 0 {
			return false
		}
		return (value-min)%step == 0
	}
	// Handle comma-separated values
	for _, part := range strings.Split(field, ",") {
		// Handle range N-M
		if strings.Contains(part, "-") {
			rangeParts := strings.SplitN(part, "-", 2)
			var lo, hi int
			fmt.Sscanf(rangeParts[0], "%d", &lo)
			fmt.Sscanf(rangeParts[1], "%d", &hi)
			if value >= lo && value <= hi {
				return true
			}
			continue
		}
		var v int
		if _, err := fmt.Sscanf(part, "%d", &v); err == nil && v == value {
			return true
		}
	}
	return false
}

// --- RFC 5545 RRULE ---

type rruleParams struct {
	freq       string // DAILY, WEEKLY, MONTHLY, YEARLY
	interval   int
	byDay      []time.Weekday
	byMonthDay []int
	count      int
	until      *time.Time
}

func parseRRule(expr string) (*rruleParams, error) {
	p := &rruleParams{interval: 1, count: -1}
	expr = strings.TrimSpace(expr)

	for _, part := range strings.Split(expr, ";") {
		part = strings.TrimSpace(part)
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(kv[0]))
		val := strings.TrimSpace(kv[1])

		switch key {
		case "FREQ":
			p.freq = val
		case "INTERVAL":
			fmt.Sscanf(val, "%d", &p.interval)
			if p.interval < 1 {
				p.interval = 1
			}
		case "BYDAY":
			for _, d := range strings.Split(val, ",") {
				d = strings.TrimSpace(strings.ToUpper(d))
				switch d {
				case "MO":
					p.byDay = append(p.byDay, time.Monday)
				case "TU":
					p.byDay = append(p.byDay, time.Tuesday)
				case "WE":
					p.byDay = append(p.byDay, time.Wednesday)
				case "TH":
					p.byDay = append(p.byDay, time.Thursday)
				case "FR":
					p.byDay = append(p.byDay, time.Friday)
				case "SA":
					p.byDay = append(p.byDay, time.Saturday)
				case "SU":
					p.byDay = append(p.byDay, time.Sunday)
				}
			}
		case "BYMONTHDAY":
			for _, d := range strings.Split(val, ",") {
				var day int
				fmt.Sscanf(strings.TrimSpace(d), "%d", &day)
				if day >= 1 && day <= 31 {
					p.byMonthDay = append(p.byMonthDay, day)
				}
			}
		case "COUNT":
			fmt.Sscanf(val, "%d", &p.count)
		case "UNTIL":
			t, err := time.Parse("20060102T150405Z", val)
			if err != nil {
				t, err = time.Parse("20060102", val)
				if err != nil {
					return nil, fmt.Errorf("invalid UNTIL format: %s (use 20261231T235959Z)", val)
				}
			}
			p.until = &t
		}
	}

	if p.freq == "" {
		return nil, fmt.Errorf("FREQ is required in RRULE")
	}
	return p, nil
}

func nextRRuleRun(expr string, from time.Time) (time.Time, error) {
	p, err := parseRRule(expr)
	if err != nil {
		return time.Time{}, err
	}

	// Walk forward, find first match
	t := from.Add(1 * time.Minute)
	for dayCount := 0; dayCount < 366; dayCount++ {
		if p.until != nil && t.After(*p.until) {
			return time.Time{}, fmt.Errorf("past UNTIL date")
		}

		if matchesRRule(p, t, from) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}

	return time.Time{}, fmt.Errorf("could not find next RRULE match within 366 days")
}

func matchesRRule(p *rruleParams, t, from time.Time) bool {
	switch p.freq {
	case "DAILY":
		days := int(t.Sub(from).Hours() / 24)
		if days%p.interval != 0 {
			return false
		}
	case "WEEKLY":
		weeks := int(t.Sub(from).Hours()) / (24 * 7)
		if weeks%p.interval != 0 {
			return false
		}
		if len(p.byDay) > 0 {
			matched := false
			for _, d := range p.byDay {
				if t.Weekday() == d {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		} else if t.Weekday() != from.Weekday() {
			return false
		}
	case "MONTHLY":
		months := (int(t.Month()) - int(from.Month())) + 12*(t.Year()-from.Year())
		if months%p.interval != 0 {
			return false
		}
		if len(p.byMonthDay) > 0 {
			matched := false
			for _, d := range p.byMonthDay {
				if t.Day() == d {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
	case "YEARLY":
		if (t.Year()-from.Year())%p.interval != 0 {
			return false
		}
	}
	return true
}

func buildCronjobTool(store *CronStore) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "cronjob",
		Description: strings.Join([]string{
			"Manage scheduled cron jobs that run agent prompts on a recurring schedule.",
			"Jobs persist across sessions in ~/.covo-agent/cron/jobs.json.",
			"",
			"Actions:",
			"- create: Schedule a new recurring job (requires name, prompt, schedule)",
			"- list: Show all jobs with status and next run time",
			"- pause: Disable a job (keeps it but stops execution)",
			"- resume: Re-enable a paused job",
			"- remove: Permanently delete a job",
			"- run: Trigger a job immediately (ignoring schedule)",
			"",
			"Schedule formats: '@every 30m', '@hourly', '@daily', '@weekly', '@monthly',",
			"standard 5-field cron (e.g. '*/5 * * * *', '0 9 * * 1-5'),",
			"or RFC 5545 RRULE (e.g. 'FREQ=WEEKLY;BYDAY=MO,WE,FR;INTERVAL=1').",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Action: create, list, pause, resume, remove, run",
					"enum":        []string{"create", "list", "pause", "resume", "remove", "run"},
				},
				"job_id": map[string]any{
					"type":        "string",
					"description": "Job ID (required for pause, resume, remove, run).",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Human-readable job name (required for create).",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The agent prompt to execute (required for create).",
				},
				"schedule": map[string]any{
					"type":        "string",
					"description": "Cron schedule (required for create). E.g. '@daily', '*/30 * * * *'.",
				},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Action   string `json:"action"`
				JobID    string `json:"job_id"`
				Name     string `json:"name"`
				Prompt   string `json:"prompt"`
				Schedule string `json:"schedule"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			switch strings.ToLower(params.Action) {
			case "create":
				if params.Name == "" || params.Prompt == "" || params.Schedule == "" {
					return nil, fmt.Errorf("name, prompt, and schedule are required for create")
				}
				job, err := store.Create(params.Name, params.Prompt, params.Schedule)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"status":   "created",
					"job_id":   job.ID,
					"name":     job.Name,
					"schedule": job.Schedule,
					"next_run": job.NextRun.Format(time.RFC3339),
				}, nil

			case "list":
				jobs := store.List()
				var items []map[string]any
				for _, j := range jobs {
					item := map[string]any{
						"job_id":    j.ID,
						"name":      j.Name,
						"schedule":  j.Schedule,
						"enabled":   j.Enabled,
						"run_count": j.RunCount,
					}
					if !j.NextRun.IsZero() {
						item["next_run"] = j.NextRun.Format(time.RFC3339)
					}
					if !j.LastRun.IsZero() {
						item["last_run"] = j.LastRun.Format(time.RFC3339)
					}
					if j.LastResult != "" {
						item["last_result"] = j.LastResult
					}
					items = append(items, item)
				}
				return map[string]any{"count": len(items), "jobs": items}, nil

			case "pause":
				if params.JobID == "" {
					return nil, fmt.Errorf("job_id is required for pause")
				}
				if err := store.Disable(params.JobID); err != nil {
					return nil, err
				}
				return map[string]any{"status": "paused", "job_id": params.JobID}, nil

			case "resume":
				if params.JobID == "" {
					return nil, fmt.Errorf("job_id is required for resume")
				}
				if err := store.Enable(params.JobID); err != nil {
					return nil, err
				}
				return map[string]any{"status": "resumed", "job_id": params.JobID}, nil

			case "remove":
				if params.JobID == "" {
					return nil, fmt.Errorf("job_id is required for remove")
				}
				if err := store.Remove(params.JobID); err != nil {
					return nil, err
				}
				return map[string]any{"status": "removed", "job_id": params.JobID}, nil

			case "run":
				if params.JobID == "" {
					return nil, fmt.Errorf("job_id is required for run")
				}
				job, ok := store.Get(params.JobID)
				if !ok {
					return nil, fmt.Errorf("job %q not found", params.JobID)
				}
				return map[string]any{
					"status":  "triggered",
					"job_id":  job.ID,
					"prompt":  job.Prompt,
					"message": "Job will be executed in the next scheduler tick",
				}, nil

			default:
				return nil, fmt.Errorf("unknown action %q", params.Action)
			}
		},
	}
}
