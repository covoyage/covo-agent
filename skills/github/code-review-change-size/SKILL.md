---
name: code-review-change-size
description: "Enforce change size limits during code review: 800 lines mechanical, 500 lines complex. Suggest staging splits."
version: 1.0.0
author: Covo Agent
---

# Code Review — Change Size

## Limits

- Mechanical changes (renames, formatting): ≤800 lines
- Complex logic changes: ≤500 lines

## When Exceeded

1. Explain whether the change can be split into reviewable stages
2. Identify the smallest coherent stage to land first
3. Base staging on actual diff, dependencies, and affected call sites
