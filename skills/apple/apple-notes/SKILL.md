---
name: apple-notes
description: "Create, view, edit, delete, search, move, or export Apple Notes via the memo CLI on macOS."
version: 1.1.0
author: Covo Agent
license: MIT
platforms: [macos]
prerequisites:
  commands: [memo]
metadata:
  tags: [apple, notes, macos, memo]
---

# Apple Notes — memo CLI

Use `memo notes` to manage Apple Notes.app from the terminal.

## When to Use

- User wants to create, list, search, edit, move, or export Notes on this Mac
- Content should live in Notes.app (syncs via iCloud)

Do not use for Bear, Obsidian, or Notion — those have their own skills.

## Setup

```bash
brew tap antoniorodr/memo
brew install antoniorodr/memo/memo
```

macOS only. Grant Automation access to Notes.app when prompted
(System Settings → Privacy & Security → Automation).

## Commands

```bash
memo notes                      # list all notes
memo notes -f "Folder Name"     # filter by folder
memo notes -s "query"           # fuzzy search
memo notes -a "Note Title"      # quick-add with title (opens editor)
memo notes -e                   # edit (interactive pick)
memo notes -d                   # delete (interactive pick)
memo notes -m                   # move to folder (interactive pick)
memo notes -ex                  # export selected note to HTML/Markdown
```

## Limitations

- Cannot edit notes that contain images or attachments
- Several flags are interactive; they need a real terminal (use `process` with `pty=true` if the agent TTY cannot prompt)
- Do not invent `memo create` / `memo list` / `memo show` — those are not this CLI

## Verification

- [ ] `memo notes` lists notes without a permissions error
- [ ] Create/edit is visible in Notes.app

