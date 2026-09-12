---
name: findmy
description: "Use when locating Apple devices or AirTags on macOS: no findmy CLI; optional Find My.app + screenshot + view_image."
version: 1.2.0
author: Covo Agent
license: MIT
platforms: [macos]
metadata:
  tags: [apple, findmy, location, devices, airtag, macos]
  related_skills: [computer-use]
---

# Find My — no supported CLI

There is no supported `findmy` command and no Homebrew tap for it.
Do not run `findmy devices`, `findmy locate`, `findmy sound`, or `brew install megacli/tap/findmy`.
Do not hit undocumented iCloud endpoints. Do not invent device coordinates.

## Prefer the user

1. Open **Find My.app**, or iCloud.com → Find Devices.
2. Play sound / Lost Mode stays in the app — those are not scriptable here.

## Optional: read the screen (macOS only)

Only when the user asked you to look, and they accept that this activates Find My
and takes a screenshot (Screen Recording permission may be required).

```
computer_use action=list_apps
# If FindMy is not running:
# osascript -e 'tell application "FindMy" to activate'
computer_use action=wait ms=2000
computer_use action=screenshot
view_image path:<screenshot path> prompt:"List devices/items visible and any location or status text. Do not invent missing devices."
```

Report only what `view_image` describes. If the window is blank, permission-blocked,
or the screenshot is the wrong display, say so and stop.

Play sound, mark lost, or erase a device: tell the user to do it in Find My.app.
Do not click those controls unless the user named the exact device and action.

