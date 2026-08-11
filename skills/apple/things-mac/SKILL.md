---
name: things-mac
description: "Manage Things 3 todos: inbox, today, projects, areas, and tags on macOS."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [macos]
prerequisites:
  commands: [things-cli]
metadata:
  tags: [things, todo, gtd, macos, projects]
---

# Things 3

```bash
# Install
brew install megacli/tap/things-cli

# Add to inbox
things add "Review Q4 budget"

# Add with details
things add "Fix login bug" --notes "Users report 500 error on /login" --tag "bug" --when "today"

# List today
things today

# List inbox
things inbox

# List projects
things projects

# Search
things search "budget"

# Complete item
things complete "Review Q4 budget"

# Show project details
things project "Website Redesign"

# Show areas
things areas
```
