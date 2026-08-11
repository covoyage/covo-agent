---
name: tdd
description: "Use when implementing any feature or bugfix, before writing implementation code"
version: 1.0.0
author: Covo Agent
platforms: [linux, macos, windows]
metadata:
  tags: [tdd, testing, red-green-refactor, quality]
---

# Test-Driven Development (TDD)

Write the test first. Watch it fail. Write minimal code to pass.

## Core Principle

If you didn't watch the test fail, you don't know if it tests the right thing.

## Iron Law

**NO production code without a failing test first.**

Write code before the test? Delete it. Start over.
- Don't keep it as "reference"
- Don't "adapt" it while writing tests
- Delete means delete

## Red-Green-Refactor

### RED — Write Failing Test

Write one minimal test showing what should happen.

```go
func TestRetryOnFailure(t *testing.T) {
    attempts := 0
    op := func() error {
        attempts++
        if attempts < 3 { return errors.New("fail") }
        return nil
    }
    err := Retry(op, 3)
    assert.NoError(t, err)
    assert.Equal(t, 3, attempts)
}
```

### Verify RED — Watch It Fail

```bash
go test -run TestRetry ./...
```

Test must fail because feature is missing, not because of typos.

### GREEN — Minimal Code

Write simplest code. Don't add features, YAGNI, or "improve" beyond the test.

### Verify GREEN — Watch It Pass

```bash
go test -run TestRetry ./...
```

### REFACTOR — Clean Up

After green only: remove duplication, improve names, extract helpers. Keep tests green.

## When to Use

**Always:** new features, bug fixes, refactoring, behavior changes.

**Exceptions:** throwaway prototypes, generated code, config files.

## Rationalizations — Rejected

| Excuse | Reality |
|--------|---------|
| "Too simple to test" | Simple code breaks. 30 seconds to test. |
| "I'll test after" | Tests passing immediately prove nothing. |
| "Already manually tested" | Ad-hoc ≠ systematic. No record, can't re-run. |
| "Deleting X hours is wasteful" | Keeping unverified code = technical debt. |

## Red Flags — STOP

- Code before test
- Test passes immediately
- "Just this once"
- "Tests after achieve same purpose"
- "Delete means delete" — means delete
