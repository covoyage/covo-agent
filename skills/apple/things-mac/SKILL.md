---
name: things-mac
description: "Add, update, list, and search Things 3 todos, inbox, today, projects, areas, and tags on macOS."
version: 1.1.0
author: Covo Agent
license: MIT
platforms: [macos]
prerequisites:
  commands: [things]
metadata:
  tags: [things, todo, gtd, macos, projects]
---

# Things 3 — things3-cli

Binary name is `things` (not `things-cli`). Reads the local Things database; writes go through the Things URL scheme.

## When to Use

- User wants inbox/today/upcoming, or to add/update todos in Things 3 on this Mac

## Setup

```bash
GOBIN=/opt/homebrew/bin go install github.com/ossianhempel/things3-cli/cmd/things@latest
```

If DB reads fail, grant Full Disk Access to the process that runs `things`.
Optional: `THINGSDB` or `--db` for the `ThingsData-*` folder.
Optional: `THINGS_AUTH_TOKEN` so updates do not need `--auth-token`.

Do not put the auth token in chat or command lines when the env var works.

## Read (database)

```bash
things inbox --limit 50
things today
things upcoming
things search "query"
things projects
things areas
things tags
```

## Write (URL scheme)

Preview first: `things --dry-run add "Title"`

```bash
things add "Buy milk"
things add "Buy milk" --notes "2% + bananas"
things add "Book flights" --list "Travel"
things add "Pack charger" --list "Travel" --heading "Before"
things add "Call dentist" --tags "health,phone"
things add "Trip prep" --checklist-item "Passport" --checklist-item "Tickets"

cat <<'EOF' | things add -
Title line
Notes line 1
EOF
```

Updates need the UUID from search plus auth:

```bash
things search "milk" --limit 5
things update --id <UUID> --auth-token "$THINGS_AUTH_TOKEN" "New title"
things update --id <UUID> --completed
things update --id <UUID> --canceled
things update --id <UUID> --append-notes "..."
things --dry-run update --id <UUID> --completed
```

## Limits

- There is no delete/trash write command. `things trash` is read-only listing.
- There is no `things complete "Title"` — complete by UUID via `update --completed`.
- `--tag` is wrong; use `--tags`.

