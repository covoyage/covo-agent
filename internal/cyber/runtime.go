package cyber

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"time"
)

type EngagementRuntime struct {
	ID           string    `json:"id"`
	Backend      string    `json:"backend"`
	Workspace    string    `json:"workspace"`
	Status       string    `json:"status"`
	StartedAt    time.Time `json:"started_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	PreviousStop string    `json:"previous_stop,omitempty"`
}

func EngagementID(workDir string) string {
	absolute, err := filepath.Abs(workDir)
	if err != nil {
		absolute = workDir
	}
	sum := sha256.Sum256([]byte(filepath.Clean(absolute)))
	return "cyber-" + hex.EncodeToString(sum[:8])
}

func (c *Catalog) runtimePath() string { return filepath.Join(c.dir, "runtime.json") }

func (c *Catalog) EnsureRuntime(backend, id, workspace string) (EngagementRuntime, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var runtime EngagementRuntime
	if err := c.readJSON(c.runtimePath(), &runtime); err != nil {
		return EngagementRuntime{}, err
	}
	now := time.Now().UTC()
	if runtime.ID == "" || runtime.ID != id {
		runtime = EngagementRuntime{ID: id, StartedAt: now}
	} else if runtime.Status == "interrupted" || runtime.Status == "failed" || runtime.Status == "orphaned" || runtime.Status == "stopped" {
		runtime.PreviousStop = runtime.Status
	}
	runtime.Backend = strings.TrimSpace(backend)
	runtime.Workspace = strings.TrimSpace(workspace)
	runtime.Status = "active"
	runtime.UpdatedAt = now
	if err := c.writeJSON(c.runtimePath(), runtime); err != nil {
		return EngagementRuntime{}, err
	}
	return runtime, nil
}

func (c *Catalog) LoadRuntime() (EngagementRuntime, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var runtime EngagementRuntime
	if err := c.readJSON(c.runtimePath(), &runtime); err != nil {
		return EngagementRuntime{}, err
	}
	return runtime, nil
}

func (c *Catalog) UpdateRuntimeStatus(status string) error {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var runtime EngagementRuntime
	if err := c.readJSON(c.runtimePath(), &runtime); err != nil {
		return err
	}
	if runtime.ID == "" {
		return nil
	}
	runtime.Status = strings.ToLower(strings.TrimSpace(status))
	runtime.UpdatedAt = time.Now().UTC()
	return c.writeJSON(c.runtimePath(), runtime)
}
