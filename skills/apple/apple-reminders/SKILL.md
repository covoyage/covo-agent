---
name: apple-reminders
description: "List, add, edit, complete, or delete Apple Reminders via remindctl on macOS."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [macos]
prerequisites:
  commands: [remindctl]
metadata:
  tags: [apple, reminders, macos, todo]
---

# Apple Reminders

```bash
# Install
brew install megacli/tap/remindctl

# List lists
remindctl lists

# List reminders
remindctl list "Personal"

# Add
remindctl add "Personal" "Buy groceries" --due "tomorrow 5pm"

# Complete
remindctl complete "Buy groceries"

# Delete
remindctl delete "Buy groceries"

# Search
remindctl search "groceries"
```
