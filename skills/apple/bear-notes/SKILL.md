---
name: bear-notes
description: "Create, search, and manage Bear notes via grizzly CLI on macOS."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [macos]
prerequisites:
  commands: [grizzly]
metadata:
  tags: [bear, notes, macos, markdown, grizzly]
---

# Bear Notes

```bash
# Install
brew install megacli/tap/grizzly

# Create note with tags
grizzly create "Meeting Notes #work/project" -b "## Agenda\n- Q4 planning"

# List recent notes
grizzly list

# Search
grizzly search "meeting"

# Read
grizzly show "Meeting Notes"

# Append
grizzly append "Meeting Notes" -b "\n## Action Items\n- Review timeline"

# Export as markdown
grizzly export "Meeting Notes" > meeting.md
```
