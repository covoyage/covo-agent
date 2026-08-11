---
name: findmy
description: "Track Apple devices, AirTags, and Find My items via the FindMy app on macOS."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [macos]
prerequisites:
  commands: [findmy]
metadata:
  tags: [apple, findmy, location, devices, airtag, macos]
---

# Find My — Device Tracking

```bash
# Install
brew install megacli/tap/findmy

# List all devices
findmy devices

# Find specific device
findmy locate "iPhone"

# Find AirTags
findmy items

# Play sound on device
findmy sound "iPhone"

# Get device details
findmy info "MacBook Pro"

# Get current location
findmy location "iPhone"

# List family devices
findmy family
```
