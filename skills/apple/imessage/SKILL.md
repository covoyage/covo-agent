---
name: imessage
description: "Send and receive iMessages/SMS via the imsg CLI on macOS."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [macos]
prerequisites:
  commands: [imsg]
metadata:
  tags: [apple, imessage, sms, messaging, macos]
---

# iMessage — via imsg CLI

```bash
# Install
brew install megacli/tap/imsg

# List recent chats
imsg chats -l 10

# Read messages in a chat
imsg chat "Mom" -l 5

# Send a message
imsg send "Mom" "On my way home"

# Send to phone number
imsg send "+1234567890" "Hello from terminal"

# Search messages
imsg search "meeting"

# Unread count
imsg unread
```
