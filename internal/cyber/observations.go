package cyber

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type AgentObservation struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Task      string    `json:"task"`
	Skills    []string  `json:"skills,omitempty"`
	Tools     []string  `json:"tools,omitempty"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (c *Catalog) observationsPath() string { return filepath.Join(c.dir, "agent_observations.json") }

func (c *Catalog) RecordAgentObservation(item AgentObservation) error {
	if c == nil || strings.TrimSpace(item.ID) == "" {
		return nil
	}
	item.Role = strings.ToLower(strings.TrimSpace(item.Role))
	item.Status = strings.ToLower(strings.TrimSpace(item.Status))
	item.UpdatedAt = time.Now().UTC()
	catalogMu.Lock()
	defer catalogMu.Unlock()
	items, err := c.loadObservationsLocked()
	if err != nil {
		return err
	}
	replaced := false
	for i := range items {
		if items[i].ID == item.ID {
			if len(item.Tools) == 0 {
				item.Tools = items[i].Tools
			}
			if len(item.Skills) == 0 {
				item.Skills = items[i].Skills
			}
			if item.Role == "" {
				item.Role = items[i].Role
			}
			if item.Task == "" {
				item.Task = items[i].Task
			}
			items[i] = item
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.Before(items[j].UpdatedAt) })
	return c.writeJSON(c.observationsPath(), items)
}

func (c *Catalog) ListAgentObservations() ([]AgentObservation, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	return c.loadObservationsLocked()
}

func (c *Catalog) RecordAgentTool(id, role, tool string) error {
	id, role, tool = strings.TrimSpace(id), strings.ToLower(strings.TrimSpace(role)), strings.TrimSpace(tool)
	if c == nil || id == "" || tool == "" {
		return nil
	}
	catalogMu.Lock()
	defer catalogMu.Unlock()
	items, err := c.loadObservationsLocked()
	if err != nil {
		return err
	}
	found := false
	for i := range items {
		if items[i].ID != id {
			continue
		}
		found = true
		if !containsFold(items[i].Tools, tool) {
			items[i].Tools = append(items[i].Tools, tool)
			sort.Strings(items[i].Tools)
		}
		items[i].UpdatedAt = time.Now().UTC()
		break
	}
	if !found {
		items = append(items, AgentObservation{ID: id, Role: role, Status: "running", Tools: []string{tool}, UpdatedAt: time.Now().UTC()})
	}
	return c.writeJSON(c.observationsPath(), items)
}

func (c *Catalog) loadObservationsLocked() ([]AgentObservation, error) {
	var items []AgentObservation
	if err := c.readJSON(c.observationsPath(), &items); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return items, nil
}

func machineCoverageGaps(coverage []Coverage, observations []AgentObservation) []string {
	haystack := strings.ToLower(FormatCoverage(coverage))
	seen := map[string]bool{}
	var gaps []string
	for _, observation := range observations {
		if HandsOnRoles[observation.Role] && observation.Status == "completed" && len(observation.Tools) == 0 {
			gaps = append(gaps, observation.ID+" completed without observed tool calls")
		}
		for _, skill := range observation.Skills {
			if !strings.HasPrefix(skill, "security-testing-") {
				continue
			}
			risk := strings.TrimPrefix(skill, "security-testing-")
			words := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(risk))
			matched := true
			for _, word := range words {
				if !strings.Contains(haystack, word) {
					matched = false
					break
				}
			}
			if !matched && !seen[risk] {
				seen[risk] = true
				gaps = append(gaps, risk+" assigned to "+observation.ID+" but absent from coverage ledger")
			}
		}
	}
	sort.Strings(gaps)
	return gaps
}

func observationsComplete(items []AgentObservation) bool {
	for _, item := range items {
		switch item.Status {
		case "failed", "interrupted", "orphaned", "running", "waiting", "stopped":
			return false
		}
	}
	return true
}
