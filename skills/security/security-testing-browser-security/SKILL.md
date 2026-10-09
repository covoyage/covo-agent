---
name: security-testing-browser-security
description: Verify browser-origin, messaging, framing, and cross-origin policy failures.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Browser Security Verification

Map origins, frames, windows, service workers, storage, cross-origin requests, and postMessage handlers. Test under real browser enforcement rather than inferring behavior from headers alone.

For CORS, prove a hostile origin can read a sensitive authenticated response. For messaging, prove origin or source validation failure reaches a privileged sink. For framing, prove a meaningful action can be overlaid or driven.

Record browser origin, policy headers, message or request, observable effect, and exchange ids. Configuration oddities without a sensitive cross-origin effect remain coverage notes.