---
name: skill-creator
description: "Use when creating new skills, editing existing skills, or verifying skills before use"
version: 2.0.0
author: Covo Agent
---

# Creating Skills — TDD for Process Documentation

Write the skill. Test it with a subagent. Close loopholes.

If you didn't watch an agent fail WITHOUT the skill, you don't know if the skill teaches the right thing.

## What is a Skill?

A reference guide for proven techniques, patterns, or tools. Helps future agent instances find and apply effective approaches.

**Skills are:** reusable techniques, patterns, reference guides.

**Skills are NOT:** narratives about how you solved a problem once.

## TDD Cycle for Skills

| TDD | Skill Creation |
|-----|---------------|
| Test case | Pressure scenario with subagent |
| Test fails (RED) | Agent violates rule without skill (baseline) |
| Test passes (GREEN) | Agent complies with skill present |
| Refactor | Close loopholes while maintaining compliance |

### RED: Run baseline without skill
Dispatch subagent with task → document what fails → capture rationalizations verbatim.

### GREEN: Write minimal skill
Address ONLY the failures seen. Don't add content for hypothetical cases.

### REFACTOR: Close loopholes
Agent found new rationalization? Add explicit counter. Re-test.

## Skill Structure

```markdown
---
name: skill-name
description: Use when [triggering conditions]
---

# Title

## Overview
Core principle in 1-2 sentences.

## When to Use
Bullet list with symptoms and use cases. When NOT to use.

## Quick Reference
Table or bullets for quick scanning.

## Implementation
Code examples or patterns.

## Common Mistakes
What goes wrong + fixes.

## Red Flags — STOP
List of warning signs.
```

## CSO — Agent Search Optimization

**Critical: description = when to use, NOT what the skill does.**

```yaml
# ❌ BAD: Summarizes workflow — agent skips reading skill
description: Use for TDD — write test first, watch it fail, write minimal code, refactor

# ✅ GOOD: Just triggering conditions
description: Use when implementing any feature or bugfix, before writing implementation code
```

**Why:** When description summarizes workflow, agent follows the description shortcut and never reads the full skill body. This was empirically proven.

### Description Rules
1. Start with "Use when..."
2. Describe triggering conditions ONLY — never the process
3. Use concrete triggers (symptoms, situations, error messages)
4. Keep under 500 characters
5. Write in third person

```yaml
# ✅ GOOD
description: Use when tests have race conditions, timing dependencies, or pass/fail inconsistently

# ✅ GOOD
description: Use when using React Router and handling authentication redirects

# ❌ BAD
description: Helps with async testing when tests are flaky
```

### Keyword Coverage
Use words agent would search for: error messages, symptoms ("flaky", "hanging"), tool names, file types, library names.

### Token Efficiency Targets
- Frequently-loaded skills: <200 words
- Other skills: <500 words
- Don't repeat cross-referenced skills
- Link heavy reference to separate files

## Anti-Patterns

| Pattern | Why Bad |
|---------|---------|
| Narrative examples | Too specific, not reusable |
| Multi-language dilution | Maintenance burden |
| Description with workflow | Agent skips reading skill body |
| Generic labels (step1, helper2) | No semantic meaning |

## Iron Law

**No skill without a failing test first.** Applies to NEW skills AND EDITS. No exceptions — not for "simple additions", "just a section", or "documentation updates".
