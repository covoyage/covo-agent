---
name: computer-use
description: "Use when driving the desktop GUI: screenshot first, inspect with read, then click/type using that frame_id."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [desktop, screenshot, accessibility, gui, computer-use]
  related_skills: [tmux]
---

# Computer Use

Drive visible desktop apps with the `computer_use` tool. This is **not** a
background virtual display: click, type, and drag move the real cursor and
can steal focus. Prefer terminal tools (`process`, `tmux`) when a CLI exists.

## When to Use

- The target has no usable CLI (native Settings, installer UI, Find My.app)
- You must confirm what is on screen before clicking
- Accessibility-driven click/type after a fresh screenshot

Do **not** use for: shell commands, page content in a browser (`browser`
navigate/click/type), or anything you can do with `read`/`edit` in the
workspace. `open_url` only shows a page in the OS default browser — it does
not mean this tool should drive that window. Reserve `computer_use` for
desktop chrome (address bar, permission prompts, native dialogs).

## Canonical loop

1. `computer_use` `action=screenshot` — save PNG, get `path`, `width`, `height`, `frame_id`.
2. `read` on that `path` (optional `prompt` to locate a control).
3. Click/drag/right-click **only** with that `frame_id`. Coordinates are pixels
   in the screenshot, origin top-left.
4. `action=wait` (`ms`, max 10000) after UI changes, then screenshot again.
   Frames expire after **30 seconds**. Stale or unknown `frame_id` → screenshot again.

```
computer_use action=screenshot
read path:<returned path> prompt:"Where is the Save button? Give pixel x,y."
computer_use action=click x:<x> y:<y> frame_id:<id>
computer_use action=wait ms=800
computer_use action=screenshot
```

Screenshot files live under the process temp dir `covo-computer/` (PNG path is
in the tool result). `read` accepts that directory.

## Actions

| Action | Needs fresh frame_id | Notes |
|---|---|---|
| `screenshot` | no | Returns path + frame_id |
| `wait` | no | `ms` default 500, max 10000 |
| `list_apps` | no | Visible windows/processes |
| `get_app_state` | no | Requires `app` name |
| `click` | **yes** | Left click at `x`,`y` |
| `drag` | **yes** | From `x`,`y` by `dx`,`dy` |
| `secondary_action` | **yes** | Right click at `x`,`y` |
| `scroll` | no | `amount` (sign = direction) |
| `type_text` | no | Types into the focused control |
| `press_key` | no | Named key |
| `set_value` | no | Set focused field |

Platforms: macOS osascript, Linux xdotool/wmctrl (screenshot: grim or ImageMagick
`import`), Windows PowerShell/user32.

## Safety

- Confirm with the user before clicks that submit, delete, purchase, or send.
- Do not spray clicks from memory of an old screenshot.
- After any navigation, screenshot again — do not reuse `frame_id`.
- `type_text` goes to whatever currently has focus. Screenshot/list_apps first
  if you are not sure.
- Never invent a separate desktop CLI; this tool is the interface.

## Common Pitfalls

1. Clicking without `frame_id` after a new screenshot — the previous frame is invalid.
2. Using `read` on a remote URL for desktop inspection — only local paths (workspace or `covo-computer/`).
3. Assuming the user's mouse stays put — it will move.
4. Waiting longer than 10s in one `wait` call — loop screenshot/wait instead.

## Verification

- [ ] Last click/drag used a `frame_id` from a screenshot taken < 30s ago
- [ ] Screen was re-captured after the UI changed
- [ ] Destructive actions were confirmed
