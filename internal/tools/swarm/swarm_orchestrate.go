package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/safego"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
)

type OrchestrationPlan struct {
	Goal         string              `json:"goal"`
	Tasks        []OrchestrationTask `json:"tasks"`
	Dependencies map[string][]string `json:"dependencies,omitempty"` // taskID -> []dependencyTaskID
}

type OrchestrationTask struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Status       string   `json:"status"` // queued, running, completed, failed, blocked
	DependsOn    []string `json:"depends_on,omitempty"`
	Result       string   `json:"result,omitempty"`
	Error        string   `json:"error,omitempty"`
	AgentID      string   `json:"agent_id,omitempty"`
	OutputFormat string   `json:"output_format,omitempty"` // "", "text", "json" — validates output
	OutputJSON   string   `json:"output_json,omitempty"`   // validated structured output (if output_format="json")
	Phase        string   `json:"phase,omitempty"`         // named pipeline stage for grouping
}

type SwarmOrchestrator struct {
	mu        sync.Mutex
	plans     map[string]*OrchestrationPlan // planID -> plan
	runner    toolssubagent.SpawnRunner
	subagent  *toolssubagent.SubagentRunner
	toolsetFn func() []string
	journal   *WorkflowJournal
}

func NewSwarmOrchestrator(runner toolssubagent.SpawnRunner, subagent *toolssubagent.SubagentRunner, toolsetFn func() []string) *SwarmOrchestrator {
	return &SwarmOrchestrator{
		plans:     make(map[string]*OrchestrationPlan),
		runner:    runner,
		subagent:  subagent,
		toolsetFn: toolsetFn,
		journal:   nil, // set via SetJournal
	}
}

// SetJournal wires a persistent journal for crash-recovery resume.
func (o *SwarmOrchestrator) SetJournal(j *WorkflowJournal) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.journal = j
}

func BuildSwarmOrchestrateTool(orch *SwarmOrchestrator) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "swarm_orchestrate",
		Description: strings.Join([]string{
			"Orchestrate a swarm of sub-agents to work on tasks in parallel.",
			"",
			"Actions:",
			"- plan:    Create an orchestration plan with task breakdown and dependencies",
			"- run:     Execute the plan by dispatching tasks to parallel sub-agents",
			"- status:  Check the current status of a running plan",
			"- results: Get results from a completed plan",
			"",
			"After creating a plan, run it to dispatch tasks to parallel sub-agents.",
			"Sub-agents are spawned as independent sessions with their own tool access.",
			"",
			"Example:",
			`  {"action":"plan","goal":"Implement user authentication module"}`,
			`  {"action":"run","plan_id":"plan_1"}`,
			`  {"action":"status","plan_id":"plan_1"}`,
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Operation: plan, run, status, results",
					"enum":        []string{"plan", "run", "status", "results"},
				},
				"plan_id": map[string]any{
					"type":        "string",
					"description": "Plan ID (required for run, status, results)",
				},
				"goal": map[string]any{
					"type":        "string",
					"description": "High-level goal for the plan (required for plan action)",
				},
				"tasks": map[string]any{
					"type":        "array",
					"description": "Optional task breakdown for the plan action. Each task runs as a parallel sub-agent; tasks only start once their dependencies have completed. Dependent tasks receive their dependencies' outputs as input. If omitted, a single task equal to the goal is created.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":            map[string]any{"type": "string", "description": "Stable task id referenced by depends_on (auto-assigned if omitted)"},
							"title":         map[string]any{"type": "string", "description": "Short task title"},
							"description":   map[string]any{"type": "string", "description": "What the sub-agent should do"},
							"depends_on":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "IDs of tasks that must complete before this one runs. Their outputs are passed as input to this task."},
							"output_format": map[string]any{"type": "string", "description": "Validate output format: 'json' (validates and extracts JSON) or 'text' (default, no validation).", "enum": []string{"text", "json"}},
							"phase":         map[string]any{"type": "string", "description": "Named pipeline stage for grouping (e.g. 'design', 'implement', 'test'). Tasks in the same phase run in parallel."},
						},
						"required": []string{"title"},
					},
				},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Action string `json:"action"`
				PlanID string `json:"plan_id"`
				Goal   string `json:"goal"`
				Tasks  []struct {
					ID           string   `json:"id"`
					Title        string   `json:"title"`
					Description  string   `json:"description"`
					DependsOn    []string `json:"depends_on"`
					OutputFormat string   `json:"output_format"`
					Phase        string   `json:"phase"`
				} `json:"tasks"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			switch params.Action {
			case "plan":
				if params.Goal == "" {
					return nil, fmt.Errorf("goal is required for plan action")
				}
				tasks := make([]OrchestrationTask, 0, len(params.Tasks))
				for _, t := range params.Tasks {
					desc := t.Description
					if desc == "" {
						desc = t.Title
					}
					tasks = append(tasks, OrchestrationTask{
						ID:           t.ID,
						Title:        t.Title,
						Description:  desc,
						DependsOn:    t.DependsOn,
						OutputFormat: t.OutputFormat,
						Phase:        t.Phase,
					})
				}
				return orch.createPlan(params.Goal, tasks)
			case "run":
				if params.PlanID == "" {
					return nil, fmt.Errorf("plan_id is required for run action")
				}
				return orch.runPlan(ctx, params.PlanID)
			case "status":
				return orch.planStatus(params.PlanID)
			case "results":
				return orch.planResults(params.PlanID)
			default:
				return nil, fmt.Errorf("unknown action: %s", params.Action)
			}
		},
	}
}

func (o *SwarmOrchestrator) createPlan(goal string, tasks []OrchestrationTask) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	planID := fmt.Sprintf("plan_%d", len(o.plans)+1)

	if len(tasks) == 0 {
		// Backward-compatible default: a single task equal to the goal.
		tasks = []OrchestrationTask{{
			ID:          fmt.Sprintf("%s_task_1", planID),
			Title:       goal,
			Description: goal,
			Status:      "queued",
		}}
	} else {
		// Pass 1: ensure every task has a stable id and collect the id set.
		// Duplicate explicit IDs are rejected (auto-assigned ids are unique by
		// construction) so statusOf/outputOf lookups always resolve to a single
		// task instead of silently matching the first duplicate.
		ids := make(map[string]bool, len(tasks))
		for i := range tasks {
			if tasks[i].ID == "" {
				tasks[i].ID = fmt.Sprintf("%s_task_%d", planID, i+1)
			}
			if ids[tasks[i].ID] {
				return nil, fmt.Errorf("duplicate task ID: %q", tasks[i].ID)
			}
			ids[tasks[i].ID] = true
		}
		// Pass 2: normalize status and drop dangling dependency references so a
		// task can never deadlock waiting on a non-existent dependency.
		for i := range tasks {
			tasks[i].Status = "queued"
			tasks[i].Result = ""
			tasks[i].Error = ""
			var valid []string
			for _, d := range tasks[i].DependsOn {
				if d != tasks[i].ID && ids[d] {
					valid = append(valid, d)
				}
			}
			tasks[i].DependsOn = valid
		}
	}

	plan := &OrchestrationPlan{
		Goal:  goal,
		Tasks: tasks,
	}
	o.plans[planID] = plan

	return map[string]any{
		"plan_id":    planID,
		"goal":       goal,
		"task_count": len(plan.Tasks),
		"status":     "created",
		"note":       "Use the run action to execute the plan. Tasks with no pending dependencies run in parallel; dependent tasks start once their dependencies complete.",
	}, nil
}

func (o *SwarmOrchestrator) runPlan(ctx context.Context, planID string) (any, error) {
	o.mu.Lock()
	plan, ok := o.plans[planID]
	o.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("plan %q not found", planID)
	}

	// --- Journal resume: load saved state to skip already-completed tasks ---
	if o.journal != nil {
		if saved, err := o.journal.Load(planID); err == nil && saved != nil {
			o.mu.Lock()
			// Merge: copy completed/failed/blocked statuses and results from
			// the saved journal. Queued/running tasks are reset to queued
			// (they were interrupted mid-execution).
			savedByID := make(map[string]*OrchestrationTask, len(saved.Tasks))
			for i := range saved.Tasks {
				savedByID[saved.Tasks[i].ID] = &saved.Tasks[i]
			}
			resumed := 0
			for i := range plan.Tasks {
				if st, ok := savedByID[plan.Tasks[i].ID]; ok {
					if st.Status == "completed" || st.Status == "failed" || st.Status == "blocked" {
						plan.Tasks[i].Status = st.Status
						plan.Tasks[i].Result = st.Result
						plan.Tasks[i].OutputJSON = st.OutputJSON
						plan.Tasks[i].Error = st.Error
						resumed++
					}
				}
			}
			o.mu.Unlock()
			if resumed > 0 {
				// Log resume — non-fatal, continues with remaining tasks.
				_ = resumed
			}
		}
	}

	toolsetNames := o.toolsetFn()

	// Execute in dependency waves. Each pass (under lock) selects every queued
	// task whose dependencies are all completed and launches them in parallel;
	// tasks with a failed/blocked dependency are marked blocked. The loop
	// repeats until no task makes further progress, so DependsOn is actually
	// honoured instead of immediately blocking on not-yet-finished deps.
	for {
		o.mu.Lock()
		// statusOf reads a task status; caller already holds o.mu.
		statusOf := func(id string) string {
			for i := range plan.Tasks {
				if plan.Tasks[i].ID == id {
					return plan.Tasks[i].Status
				}
			}
			return ""
		}
		// outputOf reads a task's validated output for passing to dependents.
		// Caller must hold o.mu.
		outputOf := func(id string) string {
			for i := range plan.Tasks {
				if plan.Tasks[i].ID == id {
					if plan.Tasks[i].OutputJSON != "" {
						return plan.Tasks[i].OutputJSON
					}
					return plan.Tasks[i].Result
				}
			}
			return ""
		}
		var wave []int
		// depInputs[taskIdx] = map[depID]output — collected under lock to avoid races.
		depInputs := make(map[int]map[string]string)
		progressed := false
		for i := range plan.Tasks {
			t := &plan.Tasks[i]
			if t.Status != "queued" {
				continue
			}
			depReady := true
			depBlocked := false
			for _, depID := range t.DependsOn {
				switch statusOf(depID) {
				case "completed":
					// satisfied
				case "failed", "blocked":
					depBlocked = true
				default:
					depReady = false
				}
			}
			if depBlocked {
				t.Status = "blocked"
				t.Error = "a dependency failed or was blocked"
				progressed = true
				continue
			}
			if !depReady {
				continue
			}
			t.Status = "running"
			wave = append(wave, i)
			progressed = true

			// Collect dependency outputs under lock for this task.
			inputs := make(map[string]string, len(t.DependsOn))
			for _, depID := range t.DependsOn {
				inputs[depID] = outputOf(depID)
			}
			depInputs[i] = inputs
		}
		o.mu.Unlock()

		if len(wave) == 0 {
			if !progressed {
				// No task can make further progress. Any task still queued is
				// stuck on a cyclic or unsatisfiable dependency (e.g. A depends
				// on B while B depends on A). Mark them blocked so they are
				// surfaced in the final tally instead of being silently left in
				// "queued" forever.
				o.mu.Lock()
				for i := range plan.Tasks {
					if plan.Tasks[i].Status == "queued" {
						plan.Tasks[i].Status = "blocked"
						plan.Tasks[i].Error = "cyclic or unsatisfiable dependency"
					}
				}
				o.mu.Unlock()
				break
			}
			continue // only blocking happened this pass; propagate to dependents
		}

		var wg sync.WaitGroup
		for _, idx := range wave {
			idx := idx
			inputs := depInputs[idx]
			wg.Add(1)
			safego.SafeGo(func() {
				defer wg.Done()
				o.runTask(ctx, plan, idx, toolsetNames, inputs)
			}, nil)
		}
		wg.Wait()

		// --- Journal checkpoint: persist state after each wave ---
		if o.journal != nil {
			o.mu.Lock()
			planCopy := *plan
			planCopy.Tasks = make([]OrchestrationTask, len(plan.Tasks))
			copy(planCopy.Tasks, plan.Tasks)
			o.mu.Unlock()
			_ = o.journal.Save(&planCopy, planID)
		}
	}

	// --- Journal cleanup: plan is fully done, remove the checkpoint ---
	if o.journal != nil {
		o.journal.Delete(planID)
	}

	var completed, failed, blocked int
	o.mu.Lock()
	total := len(plan.Tasks)
	for i := range plan.Tasks {
		switch plan.Tasks[i].Status {
		case "completed":
			completed++
		case "failed":
			failed++
		case "blocked":
			blocked++
		}
	}
	o.mu.Unlock()

	return map[string]any{
		"plan_id":   planID,
		"status":    "completed",
		"completed": completed,
		"failed":    failed,
		"blocked":   blocked,
		"total":     total,
		"next":      "Use results action to get task outputs, or status to check again.",
	}, nil
}

// runTask executes a single orchestration task and records its outcome. All
// reads/writes of the shared task are done under o.mu to avoid data races with
// concurrent siblings and with planStatus/planResults.
//
// depInputs maps dependency task ID -> that task's output, collected under lock
// before this goroutine started. This enables pipeline-style data flow.
func (o *SwarmOrchestrator) runTask(ctx context.Context, plan *OrchestrationPlan, idx int, toolsetNames []string, depInputs map[string]string) {
	o.mu.Lock()
	t := plan.Tasks[idx]
	goal := plan.Goal
	childCount := len(plan.Tasks)
	o.mu.Unlock()

	// Build the task prompt, injecting dependency outputs for pipeline data flow.
	subTask := buildTaskPromptWithInputs(t, goal, depInputs)

	var result string
	var runErr error
	if o.subagent != nil {
		result, runErr = o.subagent.Run(ctx, o.runner, subTask, toolsetNames, toolssubagent.SubagentRunOptions{
			Goal:       t.Title,
			ChildIndex: idx,
			ChildCount: childCount,
			MaxTurns:   50,
		})
	} else {
		result, runErr = o.runner(ctx, subTask, toolsetNames, 50)
	}

	o.mu.Lock()
	if runErr != nil {
		plan.Tasks[idx].Status = "failed"
		plan.Tasks[idx].Error = runErr.Error()
	} else {
		// Validate structured output if the task declares a format.
		validated, valErr := validateStructuredOutput(result, t.OutputFormat)
		if valErr != nil {
			// Validation failure: mark as failed with the validation error.
			plan.Tasks[idx].Status = "failed"
			plan.Tasks[idx].Error = fmt.Sprintf("output validation failed: %v", valErr)
			plan.Tasks[idx].Result = result
		} else {
			plan.Tasks[idx].Status = "completed"
			plan.Tasks[idx].Result = result
			if t.OutputFormat == "json" {
				plan.Tasks[idx].OutputJSON = validated
			}
		}
	}
	o.mu.Unlock()
}

func (o *SwarmOrchestrator) planStatus(planID string) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	plan, ok := o.plans[planID]
	if !ok {
		return nil, fmt.Errorf("plan %q not found", planID)
	}

	var queued, running, completed, failed, blocked int
	for _, t := range plan.Tasks {
		switch t.Status {
		case "queued":
			queued++
		case "running":
			running++
		case "completed":
			completed++
		case "failed":
			failed++
		case "blocked":
			blocked++
		}
	}

	return map[string]any{
		"plan_id":   planID,
		"goal":      plan.Goal,
		"status":    "running",
		"queued":    queued,
		"running":   running,
		"completed": completed,
		"failed":    failed,
		"blocked":   blocked,
		"total":     len(plan.Tasks),
	}, nil
}

func (o *SwarmOrchestrator) planResults(planID string) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	plan, ok := o.plans[planID]
	if !ok {
		return nil, fmt.Errorf("plan %q not found", planID)
	}

	tasks := make([]OrchestrationTask, len(plan.Tasks))
	copy(tasks, plan.Tasks)

	return map[string]any{
		"plan_id": planID,
		"goal":    plan.Goal,
		"tasks":   tasks,
	}, nil
}
