---
name: himalaya
description: "IMAP/SMTP mail via Himalaya: list, read, search, compose, reply, forward, copy, move, delete."
version: 1.2.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [himalaya]
metadata:
  tags: [email, imap, smtp, terminal, mail]
  related_skills: [gog]
---

# Himalaya — terminal email

Use `himalaya` subcommands (`folder`, `envelope`, `message`). Do not use the old `himalaya list` / `himalaya read` forms.

This is for operating a mailbox from the terminal. If the user only needs Gmail
search/send and already has `gog`, prefer that skill. Do not paste passwords
into chat.

## Setup

```bash
brew install himalaya
# or: cargo install himalaya --locked
himalaya --version
```

Config path: `~/.config/himalaya/config.toml`. Prefer `himalaya account configure`
in a real terminal (`process` `pty=true`) so the wizard can prompt locally.

Example (password via a command, not inline):

```toml
[accounts.personal]
email = "you@example.com"
display-name = "Your Name"
default = true

backend.type = "imap"
backend.host = "imap.example.com"
backend.port = 993
backend.encryption.type = "tls"
backend.login = "you@example.com"
backend.auth.type = "password"
backend.auth.cmd = "pass show email/imap"

message.send.backend.type = "smtp"
message.send.backend.host = "smtp.example.com"
message.send.backend.port = 587
message.send.backend.encryption.type = "start-tls"
message.send.backend.login = "you@example.com"
message.send.backend.auth.type = "password"
message.send.backend.auth.cmd = "pass show email/smtp"
```

Gmail: use an App Password, not the account password. Store it in a keyring
or `pass`, never in the SKILL transcript.

## Read / search

```bash
himalaya folder list
himalaya envelope list
himalaya message read <id>
himalaya envelope list from alice@example.com subject invoice
```

## Write

```bash
himalaya message write
himalaya template write
himalaya template send < /tmp/message.txt
himalaya message reply <id>
himalaya message forward <id>
```

## Organize

```bash
himalaya message copy <id> <folder>
himalaya message move <id> <folder>
himalaya message delete <id>
himalaya flag add <id> --flag seen
himalaya flag remove <id> --flag seen
```

## Safety

- Confirm before send, delete, or bulk move
- Use `--account` when multiple accounts exist
- Quote exact message IDs in summaries; do not dump full bodies unless asked

