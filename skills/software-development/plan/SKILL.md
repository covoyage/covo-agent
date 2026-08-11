---
name: plan
description: "Plan mode: write markdown plan, no execution."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [planning, plan-mode, implementation, workflow]
  related_skills: [writing-plans, subagent-driven-development]
---

# Plan Mode

Use this skill when the user wants a plan instead of execution.

## Core behavior

For this turn, you are planning only.

- Do not implement code.
- Do not edit project files except the plan markdown file.
- Do not run mutating commands, commit, push, or perform external actions.
- You may inspect the repo or other context with read-only tools when needed.
- Your deliverable is a markdown plan saved to the workspace.

## Output requirements

Write a markdown plan that is concrete and actionable.

Include, when relevant:
- Goal
- Current context / assumptions
- Proposed approach
- Step-by-step plan
- Files likely to change
- Tests / validation
- Risks, tradeoffs, and open questions

If the task is code-related, include exact file paths, likely test targets, and verification steps.

## Save location

Save the plan under:
- `docs/plans/YYYY-MM-DD-<slug>.md`

If the directory does not exist, create it first. Use a descriptive slug derived from the task.

## Interaction style

- If the request is clear enough, write the plan directly.
- If no explicit instruction accompanies the plan request, infer the task from the current conversation context.
- If it is genuinely underspecified, ask a brief clarifying question instead of guessing.
- After saving the plan, reply briefly with what you planned and the saved path.

## When to suggest execution

After saving the plan, offer to execute it:

**"Plan complete and saved to `docs/plans/YYYY-MM-DD-<slug>.md`. Ready to execute using subagent-driven-development — I'll dispatch a fresh subagent per task with two-stage review (spec compliance then code quality). Shall I proceed?"**
