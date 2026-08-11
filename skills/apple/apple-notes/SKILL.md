---
name: apple-notes
description: "Create, view, edit, delete, search, or export Apple Notes via the memo CLI on macOS."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [macos]
prerequisites:
  commands: [memo]
metadata:
  tags: [apple, notes, macos, memo]
---

# Apple Notes — via Memo CLI

```bash
# Install
brew install megacli/tap/memo

# Create
memo create "Meeting Notes" -b "Discussed Q4 roadmap"

# List
memo list

# Search
memo search "roadmap"

# Read
memo show "Meeting Notes"

# Edit
memo edit "Meeting Notes" -b "Updated: new timeline"

# Delete
memo delete "Meeting Notes"
```
