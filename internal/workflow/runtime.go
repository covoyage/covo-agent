package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ScriptExecOptions drives compile + execute (or resume) of a checkable script.
type ScriptExecOptions struct {
	Source     string
	HomeDir    string
	WorkflowID string
	Answers    map[string]string
	DryRun     bool
	RunPhase   func(ctx context.Context, phase Phase) (*PhaseResult, error)
}

// ScriptRun is the outcome of ExecuteScript.
type ScriptRun struct {
	Status     string        `json:"status"`
	WorkflowID string        `json:"workflow_id,omitempty"`
	Analysis   Analysis      `json:"analysis"`
	Results    []PhaseResult `json:"results,omitempty"`
	PendingAsk *Site         `json:"pending_ask,omitempty"`
	Journal    string        `json:"journal,omitempty"`
}

// ExecuteScript compiles a script, then walks sites. ask() without an
// answer pauses the journal so the caller can resume later.
func ExecuteScript(ctx context.Context, opts ScriptExecOptions) (*ScriptRun, error) {
	src := opts.Source
	var journal *Journal
	var wfID string
	journalDir := filepath.Join(opts.HomeDir, "workflows")

	if opts.WorkflowID != "" {
		path := filepath.Join(journalDir, opts.WorkflowID+".json")
		j, err := NewJournal(path, opts.WorkflowID)
		if err != nil {
			return nil, err
		}
		if j.Script == "" {
			return nil, fmt.Errorf("journal %s has no script to resume", opts.WorkflowID)
		}
		journal = j
		src = j.Script
		wfID = opts.WorkflowID
		if journal.Answers == nil {
			journal.Answers = map[string]string{}
		}
		for k, v := range opts.Answers {
			journal.Answers[k] = v
		}
		journal.SetPaused(false)
		_ = journal.Save()
	}

	analysis := Analyze(src)
	out := &ScriptRun{Analysis: analysis}
	if len(analysis.Diagnostics) > 0 {
		out.Status = "invalid"
		return out, nil
	}
	if opts.DryRun {
		out.Status = "ok"
		return out, nil
	}
	if opts.RunPhase == nil {
		out.Status = "ok"
		return out, nil
	}

	if journal == nil {
		wfID = fmt.Sprintf("script-%d", time.Now().UnixMilli())
		if err := os.MkdirAll(journalDir, 0o755); err != nil {
			return nil, err
		}
		j, err := NewJournal(filepath.Join(journalDir, wfID+".json"), wfID)
		if err != nil {
			return nil, err
		}
		j.Script = src
		if j.Answers == nil {
			j.Answers = map[string]string{}
		}
		for k, v := range opts.Answers {
			j.Answers[k] = v
		}
		journal = j
		_ = journal.Save()
	}

	out.WorkflowID = wfID
	out.Journal = journal.path

	start := journal.SiteIndex
	if start < 0 {
		start = 0
	}
	for i := start; i < len(analysis.Sites); i++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		site := analysis.Sites[i]
		journal.SetSiteIndex(i)
		switch site.Kind {
		case "ask":
			if ans, ok := journal.Answers[site.ID]; ok && ans != "" {
				journal.RecordResult(PhaseResult{
					PhaseID:   "ask:" + site.ID,
					Status:    PhaseCompleted,
					Output:    ans,
					StartedAt: time.Now(),
					EndedAt:   time.Now(),
				})
				journal.SetSiteIndex(i + 1)
				journal.PendingAsk = ""
				_ = journal.Save()
				continue
			}
			journal.PendingAsk = site.ID
			journal.SetPaused(true)
			_ = journal.Save()
			s := site
			out.Status = "paused"
			out.PendingAsk = &s
			out.Results = journal.GetResults()
			return out, nil
		case "phase", "spawn":
			phase := Phase{ID: site.ID, Name: site.ID, Prompt: site.Label, Role: site.Label}
			if site.Kind == "phase" && site.Label != "" {
				phase.Prompt = site.Label
				phase.Role = ""
			}
			if site.Kind == "spawn" {
				phase.Prompt = site.ID
				phase.Role = site.Label
			}
			result, err := opts.RunPhase(ctx, phase)
			if err != nil {
				return out, fmt.Errorf("site %s: %w", site.ID, err)
			}
			if result != nil {
				journal.RecordResult(*result)
			}
			journal.SetSiteIndex(i + 1)
			_ = journal.Save()
		}
	}
	out.Status = "ok"
	out.Results = journal.GetResults()
	_ = journal.Save()
	return out, nil
}
