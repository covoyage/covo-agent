---
name: batch
description: "Execute batch operations on multiple files in parallel. Automatically discovers files, splits into chunks, and processes with parallel worker agents."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos]
metadata:
  tags: [batch, parallel, automation, refactoring, code-generation]
  related_skills: [subagent-driven-development, coding-agent]
---

# Batch — Parallel Batch Operations

Execute batch operations across multiple files in parallel. Discover files, chunk them, and launch parallel worker agents.

## When to Use

- User asks to apply the same change across many files (e.g., "add copyright headers to all `.go` files")
- Large-scale refactoring (e.g., "migrate all API endpoints from v1 to v2")
- Code generation across multiple targets (e.g., "generate DTOs for all entity types")
- Systematic cleanup (e.g., "remove deprecated imports from all files")

**Skip for:** single-file changes, targeted edits of 1-3 files (use direct edit tools).

## Step 1 — Parse Intent and Discover Files

Parse the user's request to identify:
- **Target pattern**: glob pattern for files
- **Operation**: what to do with each file

Use `glob` tool to discover matching files.

Auto-exclude these patterns:
- `node_modules/**`, `dist/**`, `build/**`, `.git/**`
- `**/*.test.*`, `**/*.spec.*`, `**/__tests__/**`
- `**/package-lock.json`, `**/yarn.lock`, `**/*.min.*`
- Binary files (images, fonts, etc.)
- Files larger than 500KB

### File count thresholds

| Count | Action |
|-------|--------|
| 0 | Inform user; suggest broader pattern |
| 1-5 | Process sequentially (no parallelization needed) |
| 6-50 | Proceed with parallel chunks |
| 51-100 | Warn user about count; proceed |
| 100+ | Suggest narrower pattern; ask user to confirm |

## Step 2 — Chunk Files for Parallel Processing

| Total Files | Chunk Count | Files Per Chunk |
|-------------|-------------|-----------------|
| 1-5         | 1           | All files       |
| 6-15        | 2           | 3-8 each        |
| 16-30       | 3           | ~10 each        |
| 31-50       | 4           | ~12 each        |
| 51-75       | 5           | ~10-15 each      |
| 76-100      | 5           | ~15-20 each      |

Rules:
- Minimum chunk size: 3 files
- Maximum chunk size: 15 files
- Maximum parallel workers: 5

## Step 3 — Launch Parallel Workers

Use `sessions_spawn` to launch worker agents **in parallel** (all calls in a single response for concurrent execution).

### Worker Prompt Template

```
You are processing files in a batch operation. Process EACH file in this list:

<files>
<file_path_1>
<file_path_2>
...
</files>

<operation>
<description of what to do>
</operation>

Rules:
1. Process each file independently
2. Report success/failure per file
3. Do NOT cross-reference between files
4. Do NOT modify files not in the list

Return results in this format:
- FILE: <path>
- STATUS: PASS | FAIL
- SUMMARY: <one-line description of changes or reason for failure>
```

## Step 4 — Aggregate Results

After all workers complete, present a summary:

```
## Batch Operation Summary

Operation: <description>
Files processed: <N>
Chunks: <M>

| Worker | Files | Status | Details |
|--------|-------|--------|---------|
| 1      | 8     | PASS   | All 8 files updated |
| 2      | 8     | PASS   | All 8 files updated |
| 3      | 8     | FAIL   | 7 passed, 1 failed (src/broken.go: parse error) |

### Failures
- src/broken.go: parse error — Go syntax error at line 42

### Total
- Passed: 23/24
- Failed: 1/24

### Next Steps
<recommend fixing failures individually>
```

## Best Practices

1. **Preview first**: For destructive operations, run a dry-run or show a sample before full execution
2. **Idempotent**: Operations should be safe to re-run without side effects
3. **Rollback plan**: When possible, note the `git` commands to revert
4. **Consistency**: All files should receive the same treatment — no per-file variations

## Pitfalls

- **Pattern too broad**: "all files" usually isn't the right answer — guide users to the right glob
- **Missing context**: Workers only see their chunk; ensure the operation description is self-contained
- **Race conditions**: Files that depend on each other should be in the same chunk
- **Partial completion**: Always report which files succeeded and which failed