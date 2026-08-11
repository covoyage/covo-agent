---
name: github-issue-autofix
description: "Automatically fix GitHub issues by spawning background coding agents that open PRs."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [git, gh]
metadata:
  tags: [github, issue, autofix, pr, automation, background, agent]
  related_skills: [github-issues, github-pr-workflow, github-code-review, coding-agent]
---

# GitHub Issue Autofix — Automated Issue-to-PR Pipeline

Select GitHub issues, spawn background coding agents to fix them, and
automatically open pull requests. Use this for batch issue resolution
and automated fix pipelines.

## When to Use

- "fix these issues"
- "auto-fix issues labeled bug"
- "triage and fix the top 3 issues"
- "fix issue #42"
- "watch the repo for new issues and auto-fix"

## Process

### Phase 1 — Select Issues

Use `github-issues` to find candidates:
```bash
# List open issues with labels
gh issue list --label "bug" --state open --limit 20

# Filter by specific criteria
gh issue list --label "good first issue" --limit 10
```

Selection criteria:
- Has clear reproduction steps
- Scope is well-defined (not a feature request)
- Can be verified with tests
- No existing PR already open

### Phase 2 — Triage and Classify

For each candidate, assess:
1. **Complexity:** Can a sub-agent fix this autonomously?
2. **Dependencies:** Does it need changes across multiple repos?
3. **Verification:** Can the fix be tested automatically?
4. **Priority:** Label-based prioritization

Skip issues that:
- Require design decisions
- Need user input or clarification
- Are too large (estimate > 200 lines changed)

### Phase 3 — Spawn Background Fixers

For each selected issue, use `coding-agent` in background mode:

```bash
# Clone/fork the repo, then:
bash pty:true workdir:/tmp/issue-N background:true command:"
  git checkout -b fix/issue-N &&
  codex --yolo 'Fix issue #N: <description>.
    Follow the issue closely.
    Add tests for the fix.
    Commit with message: fix: <summary> (closes #N)
    Push and create a PR with gh pr create.
    Send completion message with sessions_send.'
"
```

Key directives for each fixer:
- **Understand first:** Read the issue thoroughly before writing code
- **Minimal change:** Fix only what's broken, no refactoring
- **Add tests:** Every fix must include a regression test
- **Self-contain:** Don't touch unrelated files
- **Report back:** Use `sessions_send` to notify completion

### Phase 4 — Review and Merge

When fixers complete:

1. **Review the fix:**
```bash
gh pr list --label "autofix"
gh pr view <number>
gh pr diff <number>
```

2. **Verify CI passes:**
```bash
gh pr checks <number>
```

3. **Request human review for complex fixes:**
```bash
gh pr review <number> --comment --body "
Auto-fix for issue #N.
Summary of changes:
- ...
Please verify the fix addresses the issue."
```

4. **Merge when ready:**
```bash
gh pr merge <number> --squash --auto
gh issue close <issue-number> --comment "Fixed by #<number>"
```

### Phase 5 — Batch Watch Mode

For continuous monitoring:
```bash
# Cron-based polling
bash background:true command:"
  while true; do
    issues=$(gh issue list --label 'good first issue' --state open --json number,title -q '.[].number')
    for n in $issues; do
      if ! gh pr list --head fix/issue-$n --state open | grep -q .; then
        # Spawn fixer for issue $n
        ...
      fi
    done
    sleep 300  # Poll every 5 minutes
  done
"
```

## Key Patterns

### Pattern: Single Issue Fix
1. Read the issue with `gh issue view <N>`
2. Spawn a coding agent to fix it
3. Agent opens PR automatically
4. Review and merge

### Pattern: Batch Triage
1. List issues with `gh issue list`
2. Filter by label/age/priority
3. Spawn one fixer per issue
4. Track with kanban tool

### Pattern: Labelled Autofix
1. Watch for issues labeled "autofix"
2. Automatically spawn fixer
3. Add "in-progress" label
4. On completion: add "fixed" label, open PR

## Integration

- **kanban:** Track fixes on a board with status flow
- **sessions_spawn_batch:** Run multiple fixers in parallel
- **sessions_send:** Get completion notifications
