package cyber

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type IntakeConfig struct {
	Enabled bool     `json:"enabled"`
	Hosts   []string `json:"hosts"`
}

type IntakeJob struct {
	Key        string    `json:"key"`
	ExchangeID string    `json:"exchange_id"`
	Endpoint   string    `json:"endpoint"`
	Parameters []string  `json:"parameters,omitempty"`
	Identity   string    `json:"identity,omitempty"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (c *Catalog) intakeConfigPath() string { return filepath.Join(c.dir, "intake_config.json") }
func (c *Catalog) intakeJobsPath() string   { return filepath.Join(c.dir, "intake_jobs.json") }

func (c *Catalog) SetIntake(config IntakeConfig) (IntakeConfig, error) {
	seen := map[string]bool{}
	hosts := config.Hosts
	config.Hosts = nil
	for _, raw := range hosts {
		host := normalizeIntakeHost(raw)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		config.Hosts = append(config.Hosts, host)
	}
	if config.Enabled && len(config.Hosts) == 0 {
		return IntakeConfig{}, fmt.Errorf("at least one allowed host is required to enable request intake")
	}
	sort.Strings(config.Hosts)
	catalogMu.Lock()
	defer catalogMu.Unlock()
	return config, c.writeJSON(c.intakeConfigPath(), config)
}

func normalizeIntakeHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || strings.ContainsAny(parsed.Hostname(), "* ") {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func (c *Catalog) IntakeConfig() (IntakeConfig, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var config IntakeConfig
	return config, c.readJSON(c.intakeConfigPath(), &config)
}

func (c *Catalog) IntakeJobs() ([]IntakeJob, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	return c.loadIntakeJobsLocked()
}

func (c *Catalog) loadIntakeJobsLocked() ([]IntakeJob, error) {
	var jobs []IntakeJob
	return jobs, c.readJSON(c.intakeJobsPath(), &jobs)
}

// QueueExchange only creates a job for an explicitly enabled, in-scope, novel request shape.
func (c *Catalog) QueueExchange(ex Exchange) (IntakeJob, bool, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	return c.queueExchangeLocked(ex)
}

func (c *Catalog) queueExchangeLocked(ex Exchange) (IntakeJob, bool, error) {
	var config IntakeConfig
	if err := c.readJSON(c.intakeConfigPath(), &config); err != nil || !config.Enabled {
		return IntakeJob{}, false, err
	}
	parsed, err := url.Parse(ex.URL)
	if err != nil || parsed.Hostname() == "" {
		return IntakeJob{}, false, fmt.Errorf("invalid exchange url")
	}
	host := strings.ToLower(parsed.Hostname())
	allowed := false
	for _, candidate := range config.Hosts {
		if host == candidate {
			allowed = true
			break
		}
	}
	if !allowed {
		return IntakeJob{}, false, nil
	}
	job := intakeJobForExchange(ex, parsed)
	jobs, err := c.loadIntakeJobsLocked()
	if err != nil {
		return IntakeJob{}, false, err
	}
	for _, existing := range jobs {
		if existing.Key == job.Key {
			return existing, false, nil
		}
	}
	if len(jobs) >= 500 {
		for i, job := range jobs {
			if job.Status == "completed" || job.Status == "failed" {
				jobs = append(jobs[:i], jobs[i+1:]...)
				break
			}
		}
		if len(jobs) >= 500 {
			return IntakeJob{}, false, fmt.Errorf("request intake queue is full (500 pending jobs)")
		}
	}
	jobs = append(jobs, job)
	return job, true, c.writeJSON(c.intakeJobsPath(), jobs)
}

func intakeJobForExchange(ex Exchange, parsed *url.URL) IntakeJob {
	parameters := exchangeParameterNames(ex, parsed)
	endpoint := ex.Method + " " + strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host) + parsed.EscapedPath()
	sum := sha256.Sum256([]byte(endpoint + "|" + strings.Join(parameters, ",") + "|" + ex.Identity))
	return IntakeJob{Key: hex.EncodeToString(sum[:12]), ExchangeID: ex.ID, Endpoint: endpoint, Parameters: parameters, Identity: ex.Identity, Status: "pending", UpdatedAt: time.Now().UTC()}
}

func (c *Catalog) syncIntakeLabelLocked(ex Exchange) error {
	jobs, err := c.loadIntakeJobsLocked()
	if err != nil {
		return err
	}
	for i, job := range jobs {
		if job.ExchangeID != ex.ID || job.Status != "pending" {
			continue
		}
		if ex.BaselineID != "" {
			jobs = append(jobs[:i], jobs[i+1:]...)
			return c.writeJSON(c.intakeJobsPath(), jobs)
		}
		parsed, err := url.Parse(ex.URL)
		if err != nil {
			return err
		}
		updated := intakeJobForExchange(ex, parsed)
		for other, candidate := range jobs {
			if other != i && candidate.Key == updated.Key {
				jobs = append(jobs[:i], jobs[i+1:]...)
				return c.writeJSON(c.intakeJobsPath(), jobs)
			}
		}
		jobs[i] = updated
		return c.writeJSON(c.intakeJobsPath(), jobs)
	}
	return nil
}

func (c *Catalog) ClaimIntake() (IntakeJob, bool, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	jobs, err := c.loadIntakeJobsLocked()
	if err != nil {
		return IntakeJob{}, false, err
	}
	for i := range jobs {
		if jobs[i].Status == "pending" {
			jobs[i].Status, jobs[i].UpdatedAt = "running", time.Now().UTC()
			return jobs[i], true, c.writeJSON(c.intakeJobsPath(), jobs)
		}
	}
	return IntakeJob{}, false, nil
}

func (c *Catalog) RecoverIntake() error {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	jobs, err := c.loadIntakeJobsLocked()
	if err != nil {
		return err
	}
	changed := false
	for i := range jobs {
		if jobs[i].Status == "running" {
			jobs[i].Status = "pending"
			jobs[i].UpdatedAt = time.Now().UTC()
			changed = true
		}
	}
	if changed {
		return c.writeJSON(c.intakeJobsPath(), jobs)
	}
	return nil
}

func (c *Catalog) CompleteIntake(key string, runErr error) error {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	jobs, err := c.loadIntakeJobsLocked()
	if err != nil {
		return err
	}
	for i := range jobs {
		if jobs[i].Key != key {
			continue
		}
		jobs[i].Status, jobs[i].Error = "completed", ""
		if runErr != nil {
			jobs[i].Status, jobs[i].Error = "failed", clip(runErr.Error(), 500)
		}
		jobs[i].UpdatedAt = time.Now().UTC()
		return c.writeJSON(c.intakeJobsPath(), jobs)
	}
	return fmt.Errorf("intake job %q not found", key)
}
