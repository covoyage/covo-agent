---
name: apple-reminders
description: "List, add, complete, or delete Apple Reminders and reminder lists via remindctl on macOS."
version: 1.1.0
author: Covo Agent
license: MIT
platforms: [macos]
prerequisites:
  commands: [remindctl]
metadata:
  tags: [apple, reminders, macos, todo]
---

# Apple Reminders — remindctl

Use `remindctl` to manage Apple Reminders so items sync to iPhone/iPad.

## When to Use

- User mentions the Reminders app, or wants a to-do that appears on their phone
- Managing reminder lists or due dates that live in Reminders.app

## When NOT to Use

- One-shot "ping me in this chat" → `cronjob`, not Reminders
- Calendar events → Calendar, not Reminders
- Project work tracking → GitHub Issues, Linear, Notion, or kanban
- User says "remind me" but means an agent alert — ask which they want

## Setup

```bash
brew install steipete/tap/remindctl
remindctl status
remindctl authorize    # if access is missing
```

macOS only. Grant Reminders permission when prompted.

## Commands

```bash
remindctl                 # today
remindctl today
remindctl tomorrow
remindctl week
remindctl overdue
remindctl all
remindctl 2026-01-04      # specific date

remindctl list                          # all lists
remindctl list Work                     # one list
remindctl list Projects --create
remindctl list Work --delete

remindctl add "Buy milk"
remindctl add --title "Call mom" --list Personal --due tomorrow
remindctl add --title "Meeting prep" --due "2026-02-15 09:00"

remindctl complete 1 2 3
remindctl delete 4A83 --force

remindctl today --json
remindctl today --plain
```

Date values: `today`, `tomorrow`, `yesterday`, `YYYY-MM-DD`, `YYYY-MM-DD HH:mm`, ISO 8601.

## Intent check

User: "Remind me to check the deploy in 2 hours"

Ask: Apple Reminders (syncs to phone) or a local `cronjob` alert in this session?

## Common Pitfalls

1. Completing by title instead of ID — list first, then `complete` by ID.
2. Treating this as the agent scheduler — use `cronjob` for chat/session alerts.

