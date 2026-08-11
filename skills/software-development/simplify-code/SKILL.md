---
name: simplify-code
description: "Parallel 3-agent cleanup of recent code changes."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [git]
metadata:
  tags: [code-review, cleanup, refactor, delegation, subagent, parallel, simplify]
  related_skills: [requesting-code-review, test-driven-development, plan]
---

# Simplify Code — Parallel Review & Cleanup

Review recent code changes with three focused reviewers running in parallel,
aggregate findings, and apply fixes worth applying.

**Core principle:** Three narrow reviewers beat one broad reviewer. Each
deep-searches the codebase for a single class of problem — reuse, quality,
efficiency — without diluting attention. They run concurrently via
`sessions_spawn_batch`, so you pay the latency of one review, not three.

## When to Use

Trigger when user says: "simplify", "review my code", "clean up changes",
"review my recent changes", or any variation with optional scoping:

- **Focus:** "focus on efficiency" → run only that reviewer
  Recognized: `reuse`, `quality`, `efficiency`
- **Dry run:** "just report" → run reviewers, present findings, apply NOTHING
- **Scope:** "the last commit" / "staged" / "src/foo.py"

Do NOT auto-run after every edit. Costs three subagents' tokens.

## Process

### Phase 1 — Identify Changes

```bash
# Default: uncommitted changes
git diff

# If empty, include staged
git diff HEAD

# Scoped variants per user request:
git diff --staged
git diff HEAD~1
git diff main...HEAD
git diff -- src/foo.py
```

If all empty and no git repo, check recently edited files in session.

### Phase 2 — Launch Three Reviewers in Parallel

Use `sessions_spawn_batch` with three tasks:

**Reviewer 1 — Reuse** (role: leaf, toolsets: coding)
```markdown
Search the diff for opportunities to eliminate duplication and reuse
existing abstractions. For each finding report: what's duplicated, where
the existing abstraction lives, and the recommended action (refactor/extract).
Be thorough — grep the full codebase, not just the diff.
```

**Reviewer 2 — Quality** (role: leaf, toolsets: coding)
```markdown
Review the diff for correctness and readability issues. Flag: missing
error handling, edge cases, unclear variable names, missing comments on
complex logic, safety issues (null/empty/race conditions). Classify each
finding: critical, warning, or style.
```

**Reviewer 3 — Efficiency** (role: leaf, toolsets: coding)
```markdown
Review the diff for performance and resource efficiency. Flag: N+1 queries,
unnecessary allocations, blocking I/O on hot paths, missing caching,
redundant loops, large memory footprints. Estimate the impact of each finding.
```

### Phase 3 — Aggregate and Act

1. Collect all three results
2. Categorize: critical (apply immediately), warning (consider applying), style (optional)
3. Present summary with counts and top findings
4. If dry-run: stop here
5. Apply the agreed-upon fixes one at a time
6. Verify nothing broke (run tests if available)
