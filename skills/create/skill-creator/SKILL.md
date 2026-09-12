---
name: skill-creator
description: "Use when creating or editing user-local skills; propose via skill_workshop or skill_manage, never write live SKILL.md."
version: 2.1.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [skills, authoring, workshop]
  related_skills: [skill-authoring]
---

# Creating user-local skills

Durable skill create/edit/patch/delete goes through `skill_workshop` or `skill_manage` as a **pending proposal**. Tell the user to run `/skill apply <id>` after review.

Do **not** write `~/.covo-agent/skills/.../SKILL.md` yourself.
Do **not** apply, reject, or quarantine from the tool.

For skills that ship in this repo (`skills/<category>/<name>/`), use `skill-authoring` and edit the file in git. This skill is only for user-local skills.

## Workflow

1. Establish the contract: trigger, expected outcome, and whether the skill needs scripts/references.
2. Propose with `skill_manage(action='create'|'edit'|'patch')` or `skill_workshop`. Do not write the live file.
3. Return the proposal id and tell the user: `/skill apply <id>`.
4. Stop. Live install is user-only.

## Frontmatter

Required: `name`, `description` (≤1024 chars).

```yaml
---
name: my-skill-name
description: Use when <trigger>. <one-line behavior>.
version: 1.0.0
author: Covo Agent
license: MIT
metadata:
  tags: [short, tags]
---
```

`description` is the discovery surface. Start with "Use when...". Put triggering conditions there, not a full workflow dump.

## Body shape

```
# Title

## Overview
## When to Use
## Commands / Implementation
## Common Pitfalls
## Verification Checklist
```

Keep commands real: real binary names, real flags, real install sources. Never invent a Homebrew tap or CLI.

## Iron law

No live SKILL.md writes. Proposal → user `/skill apply`. In-repo skills → `skill-authoring`.

