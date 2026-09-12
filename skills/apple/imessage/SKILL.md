---
name: imessage
description: "Send and read iMessage/SMS on macOS via the imsg CLI (chats, history, send, search)."
version: 1.1.0
author: Covo Agent
license: MIT
platforms: [macos]
prerequisites:
  commands: [imsg]
metadata:
  tags: [apple, imessage, sms, messaging, macos]
---

# iMessage — imsg

Use `imsg` for local Messages.app history, chat discovery, and sends on this Mac.

## When to Use

- Read or send iMessage/SMS from this Mac
- Look up a chat ID before sending

Do not use for Telegram, Slack, Discord, or the current agent chat — reply there with the normal session tools.

## Setup

```bash
brew install steipete/tap/imsg
imsg status --json
```

Needs:

- macOS with Messages.app signed in
- Full Disk Access for the process that runs `imsg` (chat.db)
- Automation permission for Messages.app on send
- SMS: Text Message Forwarding from the user's iPhone

## Resolve, then send

Never guess a recipient from a casual name when several chats could match. Show display name, handle, and the exact text, then confirm unless the user already gave an exact handle and message.

```bash
imsg chats --limit 25 --json
imsg search --query "dinner" --match contains --json
imsg history --chat-id 42 --limit 20 --json
imsg group --chat-id 42 --json
```

```bash
imsg send --to "+14155551212" --text "On my way" --service auto
imsg send --chat-id 42 --text "On my way"
imsg send --chat-id 42 --file /path/to/photo.jpg
```

Prefer `--chat-id` for an existing thread. Use `--to` only for an unambiguous phone/email. Use `--service sms` only when requested.

## Safety

- Confirm before send, react, or any visible state change
- Do not dump full chat history into the transcript unless the user asked
- There is no `imsg send "Mom" "text"` positional form — use `--to` / `--chat-id` / `--text`

