---
name: himalaya
description: "Terminal IMAP/SMTP email: list, read, search, compose, reply, forward, delete."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [himalaya]
metadata:
  tags: [email, imap, smtp, terminal, mail]
---

# Himalaya — Terminal Email

```bash
# Install
cargo install himalaya

# Configure
himalaya config

# List inbox
himalaya list

# Read an email
himalaya read 1

# Search
himalaya search "meeting"

# Compose and send
himalaya write
# Opens editor; send with :send

# Reply
himalaya reply 3

# Forward
himalaya forward 5

# Move to folder
himalaya move 2 Archive

# Delete
himalaya delete 1

# List folders
himalaya folders
```
