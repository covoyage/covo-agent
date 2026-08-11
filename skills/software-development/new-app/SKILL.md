---
name: new-app
description: "Autonomously create new applications from scratch: requirements, scaffolding, implementation, verification."
version: 1.0.0
author: Covo Agent
platforms: [linux, macos, windows]
metadata:
  tags: [scaffold, new-project, bootstrap, init, create-app]
  related_skills: [plan, entering-plan-mode, spike]
---

# New App — Create Applications from Scratch

Build a complete, functional prototype from zero. This skill guides through
the full lifecycle: understand → plan → approve → build → verify → feedback.

## Phase 1 — Understand Requirements

Identify core features, UX goals, platform, and constraints. If anything is
ambiguous, use `clarify` to ask targeted questions:
- What type of app? (web, CLI, library, game, mobile)
- Key features and must-haves?
- Visual aesthetic or design constraints?
- Deployment target?

## Phase 2 — Propose Plan

Present a structured summary before writing code:
- **App type and purpose**
- **Tech stack** with rationale
- **Key features** and how users interact
- **Visual approach** (dark/light, modern/minimal)
- **Architecture sketch** (entry point, core modules, data flow)

### Default Tech Stack

| Type | Preferred |
|------|-----------|
| Frontend web | React + Vite + CSS |
| Backend API | Go net/http or Python FastAPI |
| Full-stack | Next.js or Go + React |
| CLI | Go (cobra) or Python (click) |
| 2D/3D game | HTML/CSS/JS + Three.js (3D) |
| Static site | HTML + CSS + vanilla JS |

## Phase 3 — Get Approval

Present the plan and use `exit_plan_mode` or `clarify` to get user approval
before writing any code. Do NOT skip this phase.

## Phase 4 — Implement

1. **Scaffold** with `bash` (e.g. `npm create vite`, `go mod init`)
2. **Convert plan to tasks** with `kanban create`
3. **Build incrementally**: write → test → verify each feature
4. **Create placeholders** for assets you can't generate (simple SVGs, CSS patterns)
5. **Self-contain**: everything in one project, no external services by default

## Phase 5 — Verify

- Run the app with `bash` and confirm no compile/runtime errors
- Test core features manually
- Fix any bugs found
- Ensure visual quality (consistent spacing, colors, responsive layout)

## Phase 6 — Deliver

- Provide the start command (e.g. `npm start`, `go run .`)
- List what was built and what can be improved
- Ask for feedback
