package cyber

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type Retest struct {
	ID              string         `json:"id"`
	SourceSessionID string         `json:"source_session_id"`
	BranchID        string         `json:"branch_id,omitempty"`
	ChildSessionID  string         `json:"child_session_id,omitempty"`
	Finding         Finding        `json:"finding_snapshot"`
	Status          string         `json:"status"`
	Results         []RetestResult `json:"results,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

type RetestResult struct {
	Outcome string    `json:"outcome"`
	Notes   string    `json:"notes"`
	At      time.Time `json:"at"`
}

func (c *Catalog) retestsPath() string { return filepath.Join(c.dir, "retests.json") }

func (c *Catalog) BeginRetest(findingID, source string) (Retest, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	findings, err := c.loadFindingsLocked()
	if err != nil {
		return Retest{}, err
	}
	var snapshot Finding
	for _, finding := range findings {
		if finding.ID == findingID {
			snapshot = finding
			break
		}
	}
	if snapshot.ID == "" || strings.TrimSpace(source) == "" {
		return Retest{}, fmt.Errorf("retest requires an existing finding and source session")
	}
	var records []Retest
	if err := c.readJSON(c.retestsPath(), &records); err != nil {
		return Retest{}, err
	}
	if len(records) >= 1000 {
		return Retest{}, fmt.Errorf("retest ledger capacity reached")
	}
	record := Retest{ID: "retest-" + rand.Text(), SourceSessionID: source, Finding: snapshot, Status: "running", CreatedAt: time.Now().UTC()}
	records = append(records, record)
	return record, c.writeJSON(c.retestsPath(), records)
}

func (c *Catalog) FinishRetest(id, branch, outcome, notes string, childSessions ...string) (Retest, error) {
	if outcome != "completed" && outcome != "failed" && outcome != "cancelled" {
		return Retest{}, fmt.Errorf("invalid retest outcome")
	}
	if len(notes) > 20000 {
		return Retest{}, fmt.Errorf("retest notes exceed limit")
	}
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var records []Retest
	if err := c.readJSON(c.retestsPath(), &records); err != nil {
		return Retest{}, err
	}
	for index := range records {
		record := &records[index]
		if record.ID != id {
			continue
		}
		if record.Status != "running" {
			return Retest{}, fmt.Errorf("retest is already terminal")
		}
		record.BranchID, record.Status = branch, outcome
		if len(childSessions) > 0 {
			record.ChildSessionID = childSessions[0]
		}
		record.Results = append(record.Results, RetestResult{Outcome: outcome, Notes: notes, At: time.Now().UTC()})
		return *record, c.writeJSON(c.retestsPath(), records)
	}
	return Retest{}, fmt.Errorf("retest not found")
}

func (c *Catalog) ListRetests() ([]Retest, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var records []Retest
	err := c.readJSON(c.retestsPath(), &records)
	return records, err
}

type retestContextKey struct{}

func WithRetest(ctx context.Context, sessionStarted func(string)) context.Context {
	return context.WithValue(ctx, retestContextKey{}, sessionStarted)
}

func IsRetest(ctx context.Context) bool { return ctx.Value(retestContextKey{}) != nil }

func RecordRetestSession(ctx context.Context, id string) {
	if callback, ok := ctx.Value(retestContextKey{}).(func(string)); ok && callback != nil {
		callback(id)
	}
}
