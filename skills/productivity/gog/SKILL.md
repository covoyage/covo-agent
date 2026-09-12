---
name: gog
description: "Google Workspace via the gog CLI: Gmail, Calendar, Drive, Contacts, Sheets, and Docs."
version: 1.1.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [gog]
metadata:
  tags: [google, gmail, calendar, drive, docs, sheets, workspace, gog]
---

# gog — Google Workspace CLI

Use the `gog` binary (not a Python snippet, not `gws`). Requires OAuth once.

## Setup

```bash
brew install gogcli   # or follow https://gogcli.sh
gog auth credentials /path/to/client_secret.json
gog auth add you@gmail.com --services gmail,calendar,drive,contacts,docs,sheets
gog auth list
```

Do not paste client secrets or refresh tokens into chat.

## Gmail

```bash
gog gmail search 'newer_than:7d' --max 10
gog gmail messages search "in:inbox from:boss" --max 20 --account you@example.com
gog gmail send --to a@b.com --subject "Hi" --body "Hello"
gog gmail send --to a@b.com --subject "Hi" --body-file ./message.txt
gog gmail send --to a@b.com --subject "Hi" --body-file -
gog gmail send --to a@b.com --subject "Hi" --body-html "<p>Hello</p>"
gog gmail drafts create --to a@b.com --subject "Hi" --body-file ./message.txt
gog gmail drafts send <draftId>
gog gmail send --to a@b.com --subject "Re: Hi" --body "Reply" --reply-to-message-id <msgId>
```

`--body` does not unescape `\n`. Prefer `--body-file` (or `--body-file -`) for multi-line mail.

## Calendar / Drive / Sheets / Docs

```bash
gog calendar events <calendarId> --from <iso> --to <iso>
gog calendar create <calendarId> --summary "Title" --from <iso> --to <iso>
gog calendar create <calendarId> --summary "Title" --from <iso> --to <iso> --event-color 7
gog calendar update <calendarId> <eventId> --summary "New Title" --event-color 4
gog calendar colors

gog drive search "query" --max 10
gog contacts list --max 20

gog sheets get <sheetId> "Tab!A1:D10" --json
gog sheets update <sheetId> "Tab!A1:B2" --values-json '[["A","B"],["1","2"]]' --input USER_ENTERED
gog sheets append <sheetId> "Tab!A:C" --values-json '[["x","y","z"]]' --insert INSERT_ROWS
gog sheets clear <sheetId> "Tab!A2:Z"
gog sheets metadata <sheetId> --json

gog docs export <docId> --format txt --out /tmp/doc.txt
gog docs cat <docId>
```

Event color IDs are 1–11 (`gog calendar colors`). Confirm before send, delete, or bulk sheet clear.

## Common Pitfalls

1. Do not fall back to half-written `python3 -c` Google API samples.
2. Use `--account` when more than one identity is configured.

