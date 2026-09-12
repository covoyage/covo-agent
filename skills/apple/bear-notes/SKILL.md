---
name: bear-notes
description: "Create, search, and manage Bear notes via the grizzly CLI on macOS."
version: 1.1.0
author: Covo Agent
license: MIT
platforms: [macos]
prerequisites:
  commands: [grizzly]
metadata:
  tags: [bear, notes, macos, markdown, grizzly]
---

# Bear Notes — grizzly

Use `grizzly` to talk to Bear.app through x-callback-url.

## When to Use

- Bear is installed and the user wants notes created, tagged, appended, or opened there

Do not use for Apple Notes (`apple-notes`) or Obsidian (`obsidian`).

## Setup

```bash
go install github.com/tylerwince/grizzly/cmd/grizzly@latest
```

Bear must be running. Some operations need an API token:

1. Bear → Help → API Token → Copy Token
2. `mkdir -p ~/.config/grizzly && echo "YOUR_TOKEN" > ~/.config/grizzly/token`

Do not paste the token into chat. Token file: `~/.config/grizzly/token`.
Env: `GRIZZLY_TOKEN_FILE`, `GRIZZLY_CALLBACK_URL`, `GRIZZLY_TIMEOUT`.

Optional `~/.config/grizzly/config.toml`:

```toml
token_file = "~/.config/grizzly/token"
callback_url = "http://127.0.0.1:42123/success"
timeout = "5s"
```

## Commands

```bash
echo "Note content" | grizzly create --title "My Note" --tag work
grizzly create --title "Quick Note" --tag inbox < /dev/null

grizzly open-note --id "NOTE_ID" --enable-callback --json
echo "More" | grizzly add-text --id "NOTE_ID" --mode append --token-file ~/.config/grizzly/token

grizzly tags --enable-callback --json --token-file ~/.config/grizzly/token
grizzly open-tag --name "work" --enable-callback --json
```

Useful flags: `--dry-run`, `--print-url`, `--enable-callback` (needed to read data), `--json`.

## Common Pitfalls

1. There is no `grizzly show` / `grizzly list` / `grizzly search` in this CLI.
2. Reads need `--enable-callback`; otherwise you only fire a URL.
3. Token-backed ops fail silently if `~/.config/grizzly/token` is missing.

