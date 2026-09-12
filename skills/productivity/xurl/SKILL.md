---
name: xurl
description: "Authenticated X posts, replies, search, DMs, media, and raw v2 API via the xurl CLI."
version: 1.1.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [xurl]
metadata:
  tags: [twitter, x, social, posting, search, xurl]
---

# xurl — X API CLI

Use the `xurl` binary. Do not use tweepy or paste API keys into Python one-liners.

## Secret safety

- Never read, print, summarize, or upload `~/.xurl`
- Never ask the user to paste tokens into chat
- Do not run auth commands with inline secrets
- Do not use `--verbose` in agent sessions (it can leak auth headers)
- Check auth with `xurl auth status`

## Setup

```bash
brew install xdevplatform/tap/xurl
# or: npm install -g @xdevplatform/xurl
xurl auth status
```

## Shortcuts

```bash
xurl post "Hello world!"
xurl reply POST_ID "Nice."
xurl quote POST_ID "My take"
xurl delete POST_ID
xurl read POST_ID
xurl search "query" -n 20
xurl whoami
xurl user @handle
xurl timeline -n 20
xurl mentions -n 10
xurl like POST_ID
xurl unlike POST_ID
xurl repost POST_ID
xurl bookmark POST_ID
xurl followers -n 20
xurl following -n 20
xurl follow @handle
xurl unfollow @handle
xurl block @handle
xurl mute @handle
xurl dm @handle "message"
xurl dms -n 10
```

`POST_ID` may be a full `https://x.com/<user>/status/<id>` URL. Confirm before post, delete, follow, block, or DM.

## Media and raw API

```bash
xurl media upload image.jpg
xurl media upload clip.mp4
xurl media status MEDIA_ID
xurl post "caption" --media-id MEDIA_ID

xurl /2/users/me
xurl -X POST /2/tweets -d '{"text":"Hello world!"}'
xurl '/2/tweets/search/recent?query=example&max_results=10'
```

Keep complex JSON in a temp file. Videos may need `media status` polling.

## Errors

- 401/403: `xurl auth status` (scope or app mismatch)
- 429: back off
- Non-zero exit on API/auth/network errors; JSON on stdout when successful

