---
name: merge
description: "Use when implementation is complete, all tests pass, and you need to merge, PR, or clean up a development branch"
version: 1.0.0
author: Covo Agent
platforms: [linux, macos, windows]
metadata:
  tags: [git, merge, pr, cleanup, branch]
  requires:
    bins: [git, gh]
---

# Merge — Complete a Development Branch

Guide completion of development work with structured options.

## Process

### 1. Verify Tests

Run tests first. Do NOT proceed if tests fail.

```bash
go test ./...   # or: npm test, cargo test, pytest
```

### 2. Detect Branch State

```bash
CURRENT=$(git branch --show-current)
BASE=$(git merge-base HEAD main 2>/dev/null || git merge-base HEAD master)
```

### 3. Present 4 Options

| # | Option | Merge | Push | Keep Branch |
|---|--------|-------|------|-------------|
| 1 | Merge locally | ✅ git merge to base | — | Delete after |
| 2 | Create PR | — | ✅ git push + gh pr | Keep for reviews |
| 3 | Keep as-is | — | — | Keep |
| 4 | Discard | — | — | Delete (confirm) |

### 4. Execute

**Option 1 — Merge locally:**
```bash
git checkout $BASE && git pull
git merge $CURRENT
go test ./...  # verify merged result
git branch -d $CURRENT
```

**Option 2 — Create PR:**
```bash
git push -u origin $CURRENT
gh pr create --title "<title>" --body "## Summary\n\n## Test Plan\n"
```

**Option 3 — Keep:** Done. Report branch preserved.

**Option 4 — Discard:** Confirm first, then `git branch -D $CURRENT`.

## Never
- Merge with failing tests
- Delete without confirmation
- Force-push without explicit request
- `git worktree remove` from inside the worktree
