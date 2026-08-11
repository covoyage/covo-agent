---
name: babysit-pr
description: "Autonomously monitor a GitHub pull request: poll CI, diagnose failures, address review comments, auto-push fixes, and continue until merge."
version: 1.0.0
author: Covo Agent
metadata:
  tags: [github, pr, ci, review, merge]
  requires:
    bins: [gh, git]
---

# Babysit PR — Autonomous PR Monitoring

Monitor a PR continuously until merged, closed, or blocked.

## Trigger

When the user says: "monitor", "babysit", "watch the PR", "keep an eye on".

## Input

Accept: PR number, PR URL, or `--pr auto` (from current branch).

## Loop

```
1. gh pr view <pr> --json state,mergeable,headRefName,baseRefName
2. gh pr checks <pr> --watch --interval 0
   → If CI pending → poll 1min
   → If CI green → poll 90s for new reviews
3. gh pr view <pr> --json reviews,reviewRequests
4. gh api repos/<owner>/<repo>/pulls/<n>/comments
```

## CI Actions

| Situation | Action |
|-----------|--------|
| Branch-related failure (compile/test/lint) | Fix locally, commit `codex: fix CI`, push |
| Flaky/unrelated (timeout, infra) | `gh run rerun <run-id> --failed` (max 3 retries) |
| Failure unclear | `gh run view <id> --log-failed`, classify → branch or flaky |

## Review Actions

| Situation | Action |
|-----------|--------|
| Actionable review comment | Patch, commit, push, resolve thread (own PRs only) |
| Non-actionable / needs response | Report to user, do NOT reply automatically |
| Human reviewer comment | NEVER reply without user confirmation |

## State Rules

- NEVER modify other people's review threads
- NEVER close/reopen PR, mark as draft/ready
- Push to PR head branch only
- Prefix auto-commits: `codex: fix CI` or `codex: address review`

## Stop Conditions

- PR merged or closed → stop and report
- User help needed (permissions, unclear request) → stop and report
- 3+ flaky retries on same SHA → stop and report

## Keep Going When

- CI pending/running/queued
- CI green but PR open (new reviews may come)
- Review approval pending (REVIEW_REQUIRED)
