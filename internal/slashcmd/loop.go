package slashcmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/tools"
)

// handleLoop handles /loop — create, list, or stop recurring agent tasks.
//
// Usage:
//   /loop 5m <prompt>       — run <prompt> every 5 minutes
//   /loop 1h <prompt>       — run <prompt> every hour
//   /loop list              — list active loop jobs
//   /loop stop <job_id>     — stop and remove a loop job
//   /loop pause <job_id>    — pause a loop job
//   /loop resume <job_id>   — resume a paused loop job
func handleLoop(sctx *SlashContext, parts []string) bool {
	if sctx.Services.HomeDir == "" {
		sctx.UI.App.PrintSystem("(home directory not available)")
		return true
	}

	store := tools.NewCronStore(sctx.Services.HomeDir)
	if err := store.Load(); err != nil {
		sctx.UI.App.PrintSystem(fmt.Sprintf("warning: could not load existing jobs: %v", err))
	}

	// /loop list
	if len(parts) >= 2 && (parts[1] == "list" || parts[1] == "ls") {
		jobs := store.List()
		if len(jobs) == 0 {
			sctx.UI.App.PrintSystem("(no loop jobs)")
			return true
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("── Loop jobs (%d) ──", len(jobs)))
		for _, j := range jobs {
			status := "active"
			if !j.Enabled {
				status = "paused"
			}
			sctx.UI.App.PrintSystem(fmt.Sprintf("  %s [%s] every %s — runs: %d", j.ID[:8], status, j.Schedule, j.RunCount))
			sctx.UI.App.PrintSystem(fmt.Sprintf("    %s", truncate(j.Name, 80)))
		}
		return true
	}

	// /loop stop <id>
	if len(parts) >= 3 && (parts[1] == "stop" || parts[1] == "remove") {
		if err := store.Remove(parts[2]); err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("stop: %w", err))
			return true
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("stopped loop job %s", parts[2][:min(len(parts[2]), 8)]))
		return true
	}

	// /loop pause <id>
	if len(parts) >= 3 && parts[1] == "pause" {
		if err := store.Disable(parts[2]); err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("pause: %w", err))
			return true
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("paused loop job %s", parts[2][:min(len(parts[2]), 8)]))
		return true
	}

	// /loop resume <id>
	if len(parts) >= 3 && parts[1] == "resume" {
		if err := store.Enable(parts[2]); err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("resume: %w", err))
			return true
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("resumed loop job %s", parts[2][:min(len(parts[2]), 8)]))
		return true
	}

	// /loop <interval> <prompt>
	if len(parts) < 3 {
		sctx.UI.App.PrintSystem(strings.Join([]string{
			"Usage:",
			"  /loop <interval> <prompt>   — run prompt periodically (e.g. /loop 5m check CI status)",
			"  /loop list                  — list all loop jobs",
			"  /loop stop <id>             — stop and remove a job",
			"  /loop pause <id>            — pause a job",
			"  /loop resume <id>           — resume a paused job",
			"",
			"Interval examples: 5m, 30m, 1h, 2h, 1h30m",
		}, "\n"))
		return true
	}

	intervalStr := parts[1]
	prompt := strings.TrimSpace(strings.TrimPrefix(sctx.Input, parts[0]))
	prompt = strings.TrimSpace(strings.TrimPrefix(prompt, intervalStr))
	if prompt == "" {
		sctx.UI.App.PrintSystem("Usage: /loop <interval> <prompt>")
		return true
	}

	// Parse interval to validate
	dur, err := time.ParseDuration(intervalStr)
	if err != nil {
		sctx.UI.App.PrintSystem(fmt.Sprintf("invalid interval %q: %v (examples: 5m, 1h, 2h30m)", intervalStr, err))
		return true
	}
	if dur < 1*time.Minute {
		sctx.UI.App.PrintSystem("interval must be at least 1 minute")
		return true
	}

	schedule := fmt.Sprintf("@every %s", intervalStr)
	name := truncate(prompt, 60)
	job, err := store.Create(name, prompt, schedule)
	if err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("create loop: %w", err))
		return true
	}

	sctx.UI.App.PrintSystem(fmt.Sprintf("🔄 Loop job created (id: %s) — runs every %s", job.ID[:8], intervalStr))
	sctx.UI.App.PrintSystem(fmt.Sprintf("   Prompt: %s", truncate(prompt, 100)))
	sctx.UI.App.PrintSystem("   The scheduler will execute this prompt automatically. Use /loop list to check status, /loop stop <id> to stop.")

	return true
}
