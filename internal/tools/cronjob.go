package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/covoyage/covo-agent/internal/sandbox/ossandbox"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covonaut/agentcore"
)

const (
	cronRunHistoryCap    = 50
	cronBusySkipMax      = 3
	cronBusyRetryDelay   = time.Minute
	cronDefaultRunsLimit = 20
	// cronContextFromCap limits how much of a referenced job's last result is
	// injected as chained context.
	cronContextFromCap = 2000
	// cronPreRunTimeout bounds the per-job pre-run command.
	cronPreRunTimeout = 60 * time.Second
)

// ErrCronBusy is returned by a CronRunner when the interactive session is
// occupied and the job should be retried shortly instead of advancing to the
// next scheduled slot.
var ErrCronBusy = errors.New("busy")

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
	ToolsAllow []string  `json:"tools_allow,omitempty"`
	SkipCount  int       `json:"skip_count,omitempty"`

	// Model overrides the model used to run this job (the provider stays the
	// session's provider). Empty inherits the session model.
	Model string `json:"model,omitempty"`
	// Skills lists skill names the agent is asked to load before running the
	// job prompt. Empty inherits default skill behavior.
	Skills []string `json:"skills,omitempty"`
	// PreRun is an optional shell command executed before the prompt. A
	// non-zero exit aborts the run and records a prerun_error.
	PreRun string `json:"pre_run,omitempty"`
	// ContextFrom references another job ID whose last result is injected as
	// context above this job's prompt (task chaining).
	ContextFrom string `json:"context_from,omitempty"`
	// ConsecutiveErrors counts failed runs since the last success.
	ConsecutiveErrors int `json:"consecutive_errors,omitempty"`
}

// CronRunRecord is one execution (or skip) of a cron job.
type CronRunRecord struct {
	JobID     string    `json:"job_id"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Status    string    `json:"status"` // ok|error|skipped_busy|timeout
	Summary   string    `json:"summary,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
}

// CronStore manages persistent cron jobs.
type CronStore struct {
	mu           sync.RWMutex
	filePath     string
	runsFilePath string
	jobs         map[string]*CronJob
}

// NewCronStore creates a new cron job store backed by a JSON file.
func NewCronStore(homeDir string) *CronStore {
	dir := filepath.Join(homeDir, "cron")
	_ = os.MkdirAll(dir, 0755)
	return &CronStore{
		filePath:     filepath.Join(dir, "jobs.json"),
		runsFilePath: filepath.Join(dir, "runs.jsonl"),
		jobs:         make(map[string]*CronJob),
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

// Create adds a new job with the default tool set.
func (s *CronStore) Create(name, prompt, schedule string) (*CronJob, error) {
	return s.CreateWithAllow(name, prompt, schedule, nil)
}

// CreateWithAllow adds a job, optionally freezing a tool-name allow list.
func (s *CronStore) CreateWithAllow(name, prompt, schedule string, toolsAllow []string) (*CronJob, error) {
	return s.CreateWithOptions(CronJobOptions{
		Name: name, Prompt: prompt, Schedule: schedule, ToolsAllow: toolsAllow,
	})
}

// CronJobOptions carries the optional per-job configuration accepted at
// create time.
type CronJobOptions struct {
	Name        string
	Prompt      string
	Schedule    string
	ToolsAllow  []string
	Model       string
	Skills      []string
	PreRun      string
	ContextFrom string
}

// CreateWithOptions adds a job with full per-job overrides (model, skills,
// pre-run command, context chaining).
func (s *CronStore) CreateWithOptions(opts CronJobOptions) (*CronJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	allow, err := normalizeToolsAllow(opts.ToolsAllow)
	if err != nil {
		return nil, err
	}
	skills, err := normalizeSkills(opts.Skills)
	if err != nil {
		return nil, err
	}

	id := fmt.Sprintf("job_%d", time.Now().UnixNano())
	nextRun, err := nextCronRun(opts.Schedule, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid schedule %q: %w", opts.Schedule, err)
	}

	job := &CronJob{
		ID:          id,
		Name:        opts.Name,
		Prompt:      opts.Prompt,
		Schedule:    opts.Schedule,
		Enabled:     true,
		CreatedAt:   time.Now(),
		NextRun:     nextRun,
		ToolsAllow:  allow,
		Model:       strings.TrimSpace(opts.Model),
		Skills:      skills,
		PreRun:      strings.TrimSpace(opts.PreRun),
		ContextFrom: strings.TrimSpace(opts.ContextFrom),
	}
	s.jobs[id] = job
	return job, s.save()
}

func normalizeSkills(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(names))
	var out []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			return nil, fmt.Errorf("skills contains an empty name")
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out, nil
}

func normalizeToolsAllow(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(names))
	var out []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			return nil, fmt.Errorf("tools_allow contains an empty name")
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out, nil
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

// RecordRun updates a job after execution and appends a history row.
func (s *CronStore) RecordRun(id, result string) error {
	status := "ok"
	if strings.HasPrefix(result, "error:") {
		status = "error"
	}
	now := time.Now()
	return s.FinishRun(id, CronRunRecord{
		JobID:     id,
		StartedAt: now,
		EndedAt:   now,
		Status:    status,
		Summary:   result,
		SessionID: "cron-" + id,
	})
}

// FinishRun records a structured run, updates LastRun/RunCount, advances
// NextRun, and maintains the consecutive-error counter used by incidents.
func (s *CronStore) FinishRun(id string, rec CronRunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("job %q not found", id)
	}
	if rec.JobID == "" {
		rec.JobID = id
	}
	if rec.EndedAt.IsZero() {
		rec.EndedAt = time.Now()
	}
	if rec.StartedAt.IsZero() {
		rec.StartedAt = rec.EndedAt
	}
	if rec.SessionID == "" {
		rec.SessionID = "cron-" + id
	}
	j.LastRun = rec.EndedAt
	j.RunCount++
	j.LastResult = rec.Summary
	j.SkipCount = 0
	switch rec.Status {
	case "ok":
		j.ConsecutiveErrors = 0
	case "error", "timeout", "prerun_error":
		j.ConsecutiveErrors++
	}
	nextRun, _ := nextCronRun(j.Schedule, rec.EndedAt)
	j.NextRun = nextRun
	if err := s.appendRunLocked(rec); err != nil {
		return err
	}
	return s.save()
}

// CronIncident is a failed cron run kept for operational visibility.
type CronIncident struct {
	CronRunRecord
	Name string `json:"name,omitempty"`
}

// ListIncidents returns the most recent failed runs (error, timeout, or
// prerun_error) across all jobs, newest first.
func (s *CronStore) ListIncidents(limit int) ([]CronIncident, error) {
	if limit <= 0 {
		limit = cronDefaultRunsLimit
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	runs, err := s.readRunsLocked()
	if err != nil {
		return nil, err
	}
	var out []CronIncident
	for i := len(runs) - 1; i >= 0 && len(out) < limit; i-- {
		r := runs[i]
		switch r.Status {
		case "error", "timeout", "prerun_error":
			name := ""
			if j, ok := s.jobs[r.JobID]; ok {
				name = j.Name
			}
			out = append(out, CronIncident{CronRunRecord: r, Name: name})
		}
	}
	return out, nil
}

// RecordSkipBusy notes that a due job was skipped because the session is busy.
// NextRun is pushed by one minute, up to three times, then the regular schedule resumes.
func (s *CronStore) RecordSkipBusy(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("job %q not found", id)
	}
	now := time.Now()
	j.SkipCount++
	if j.SkipCount >= cronBusySkipMax {
		j.SkipCount = 0
		nextRun, _ := nextCronRun(j.Schedule, now)
		j.NextRun = nextRun
	} else {
		j.NextRun = now.Add(cronBusyRetryDelay)
	}
	j.LastResult = "skipped_busy"
	rec := CronRunRecord{
		JobID:     id,
		StartedAt: now,
		EndedAt:   now,
		Status:    "skipped_busy",
		Summary:   "session busy",
		SessionID: "cron-" + id,
	}
	if err := s.appendRunLocked(rec); err != nil {
		return err
	}
	return s.save()
}

func (s *CronStore) appendRunLocked(rec CronRunRecord) error {
	f, err := os.OpenFile(s.runsFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return s.pruneRunsLocked(rec.JobID)
}

func (s *CronStore) pruneRunsLocked(jobID string) error {
	runs, err := s.readRunsLocked()
	if err != nil {
		return err
	}
	var kept []CronRunRecord
	count := 0
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if r.JobID != jobID {
			kept = append(kept, r)
			continue
		}
		count++
		if count <= cronRunHistoryCap {
			kept = append(kept, r)
		}
	}
	if count <= cronRunHistoryCap {
		return nil
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	f, err := os.Create(s.runsFilePath)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range kept {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}

func (s *CronStore) readRunsLocked() ([]CronRunRecord, error) {
	f, err := os.Open(s.runsFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var runs []CronRunRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec CronRunRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		runs = append(runs, rec)
	}
	return runs, sc.Err()
}

// ListRuns returns the most recent runs for a job, newest first.
func (s *CronStore) ListRuns(jobID string, limit int) ([]CronRunRecord, error) {
	if limit <= 0 {
		limit = cronDefaultRunsLimit
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	runs, err := s.readRunsLocked()
	if err != nil {
		return nil, err
	}
	var matched []CronRunRecord
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].JobID != jobID {
			continue
		}
		matched = append(matched, runs[i])
		if len(matched) >= limit {
			break
		}
	}
	return matched, nil
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

// CronRunner is a callback that executes a cron job. The job passed in is a
// per-run copy: the scheduler has already composed ContextFrom chaining into
// Prompt, so the runner only needs to honor ToolsAllow, Model, and Skills.
type CronRunner func(ctx context.Context, job *CronJob) (string, error)

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

		// Pre-run gate: a configured shell command runs first; a non-zero
		// exit aborts the run before any prompt is sent to the agent.
		if job.PreRun != "" {
			if err := RunPreRun(ctx, job.PreRun); err != nil {
				_ = s.store.FinishRun(job.ID, CronRunRecord{
					JobID:     job.ID,
					StartedAt: time.Now(),
					EndedAt:   time.Now(),
					Status:    "prerun_error",
					Summary:   fmt.Sprintf("pre-run failed: %v", err),
					SessionID: "cron-" + job.ID,
				})
				continue
			}
		}

		runJob := s.store.ComposeRun(job)

		started := time.Now()
		result, err := s.runner(ctx, runJob)
		ended := time.Now()
		if err != nil && errors.Is(err, ErrCronBusy) {
			_ = s.store.RecordSkipBusy(job.ID)
			continue
		}
		status := "ok"
		summary := result
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
				status = "timeout"
			} else {
				status = "error"
			}
			summary = err.Error()
		} else if len(summary) > 500 {
			summary = summary[:500] + "..."
		}
		_ = s.store.FinishRun(job.ID, CronRunRecord{
			JobID:     job.ID,
			StartedAt: started,
			EndedAt:   ended,
			Status:    status,
			Summary:   summary,
			SessionID: "cron-" + job.ID,
		})
	}
}

// ComposeRun returns a per-run copy of the job with ContextFrom chaining
// applied: the referenced job's last result is prepended to the prompt as
// bounded context. Dangling references are tolerated (the prompt runs bare).
// Shared by the background scheduler and `cron run-due`.
func (s *CronStore) ComposeRun(job *CronJob) *CronJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	runJob := *job // shallow copy; slices are read-only downstream
	if job.ContextFrom != "" {
		if src, ok := s.jobs[job.ContextFrom]; ok && src.LastResult != "" && src.ID != job.ID {
			ctxText := src.LastResult
			if len(ctxText) > cronContextFromCap {
				ctxText = ctxText[:cronContextFromCap] + "..."
			}
			when := ""
			if !src.LastRun.IsZero() {
				when = " @ " + src.LastRun.Format(time.RFC3339)
			}
			runJob.Prompt = fmt.Sprintf("[Context from job %s (%s)%s]:\n%s\n\n%s",
				src.ID, src.Name, when, ctxText, job.Prompt)
		}
	}
	return &runJob
}

// RunPreRun executes a job's pre-run shell command with a hard timeout.
func RunPreRun(ctx context.Context, cmdLine string) error {
	runCtx, cancel := context.WithTimeout(ctx, cronPreRunTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(runCtx, "cmd", "/C", cmdLine)
	} else {
		cmd = exec.CommandContext(runCtx, "sh", "-c", cmdLine)
	}
	if err := ossandbox.PrepareChildCommand(runCtx, cmd); err != nil {
		return err
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		detail := strings.TrimSpace(string(out))
		if len(detail) > 300 {
			detail = detail[:300] + "..."
		}
		if detail == "" {
			return err
		}
		return fmt.Errorf("%v: %s", err, detail)
	}
	return nil
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
			"- runs: Show recent execution history for a job (requires job_id)",
			"- incidents: Show recent failed runs across all jobs",
			"",
			"Optional create.tools_allow freezes the tool names this job may use.",
			"Empty tools_allow inherits the agent's default tools.",
			"Optional create.model overrides the model for this job (provider unchanged).",
			"Optional create.skills lists skill names the agent should load for the run.",
			"Optional create.pre_run is a shell command executed before each run;",
			"a non-zero exit aborts the run (status prerun_error).",
			"Optional create.context_from chains jobs: the referenced job's last result",
			"is injected as context above this job's prompt.",
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
					"description": "Action: create, list, pause, resume, remove, run, runs, incidents",
					"enum":        []string{"create", "list", "pause", "resume", "remove", "run", "runs", "incidents"},
				},
				"job_id": map[string]any{
					"type":        "string",
					"description": "Job ID (required for pause, resume, remove, run, runs).",
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
				"tools_allow": map[string]any{
					"type":        "array",
					"description": "Optional tool names this job may use. Frozen at create time. Empty = default tools.",
					"items":       map[string]any{"type": "string"},
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Optional model override for this job (provider unchanged).",
				},
				"skills": map[string]any{
					"type":        "array",
					"description": "Optional skill names the agent should load for this job's run.",
					"items":       map[string]any{"type": "string"},
				},
				"pre_run": map[string]any{
					"type":        "string",
					"description": "Optional shell command run before each execution; non-zero exit aborts the run.",
				},
				"context_from": map[string]any{
					"type":        "string",
					"description": "Optional job ID whose last result is injected as context above this job's prompt.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum history rows for action=runs (default 20).",
				},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Action      string   `json:"action"`
				JobID       string   `json:"job_id"`
				Name        string   `json:"name"`
				Prompt      string   `json:"prompt"`
				Schedule    string   `json:"schedule"`
				ToolsAllow  []string `json:"tools_allow"`
				Model       string   `json:"model"`
				Skills      []string `json:"skills"`
				PreRun      string   `json:"pre_run"`
				ContextFrom string   `json:"context_from"`
				Limit       int      `json:"limit"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			switch strings.ToLower(params.Action) {
			case "create":
				if params.Name == "" || params.Prompt == "" || params.Schedule == "" {
					return nil, fmt.Errorf("name, prompt, and schedule are required for create")
				}
				job, err := store.CreateWithOptions(CronJobOptions{
					Name:        params.Name,
					Prompt:      params.Prompt,
					Schedule:    params.Schedule,
					ToolsAllow:  params.ToolsAllow,
					Model:       params.Model,
					Skills:      params.Skills,
					PreRun:      params.PreRun,
					ContextFrom: params.ContextFrom,
				})
				if err != nil {
					return nil, err
				}
				out := map[string]any{
					"status":   "created",
					"job_id":   job.ID,
					"name":     job.Name,
					"schedule": job.Schedule,
					"next_run": job.NextRun.Format(time.RFC3339),
				}
				if len(job.ToolsAllow) > 0 {
					out["tools_allow"] = job.ToolsAllow
				}
				if job.Model != "" {
					out["model"] = job.Model
				}
				if len(job.Skills) > 0 {
					out["skills"] = job.Skills
				}
				if job.PreRun != "" {
					out["pre_run"] = job.PreRun
				}
				if job.ContextFrom != "" {
					out["context_from"] = job.ContextFrom
				}
				return out, nil

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
					if len(j.ToolsAllow) > 0 {
						item["tools_allow"] = j.ToolsAllow
					}
					if j.Model != "" {
						item["model"] = j.Model
					}
					if len(j.Skills) > 0 {
						item["skills"] = j.Skills
					}
					if j.PreRun != "" {
						item["pre_run"] = j.PreRun
					}
					if j.ContextFrom != "" {
						item["context_from"] = j.ContextFrom
					}
					if j.ConsecutiveErrors > 0 {
						item["consecutive_errors"] = j.ConsecutiveErrors
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

			case "runs":
				if params.JobID == "" {
					return nil, fmt.Errorf("job_id is required for runs")
				}
				if _, ok := store.Get(params.JobID); !ok {
					return nil, fmt.Errorf("job %q not found", params.JobID)
				}
				runs, err := store.ListRuns(params.JobID, params.Limit)
				if err != nil {
					return nil, err
				}
				items := make([]map[string]any, 0, len(runs))
				for _, r := range runs {
					items = append(items, map[string]any{
						"job_id":     r.JobID,
						"started_at": r.StartedAt.Format(time.RFC3339),
						"ended_at":   r.EndedAt.Format(time.RFC3339),
						"status":     r.Status,
						"summary":    r.Summary,
						"session_id": r.SessionID,
					})
				}
				return map[string]any{"job_id": params.JobID, "count": len(items), "runs": items}, nil

			case "incidents":
				incidents, err := store.ListIncidents(params.Limit)
				if err != nil {
					return nil, err
				}
				items := make([]map[string]any, 0, len(incidents))
				for _, inc := range incidents {
					items = append(items, map[string]any{
						"job_id":     inc.JobID,
						"job_name":   inc.Name,
						"started_at": inc.StartedAt.Format(time.RFC3339),
						"ended_at":   inc.EndedAt.Format(time.RFC3339),
						"status":     inc.Status,
						"summary":    inc.Summary,
					})
				}
				return map[string]any{"count": len(items), "incidents": items}, nil

			default:
				return nil, fmt.Errorf("unknown action %q", params.Action)
			}
		},
	}
}
