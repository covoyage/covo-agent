---
name: security-testing-request-smuggling
description: Verify HTTP request-boundary disagreement using low-impact connection differentials.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# HTTP Request Smuggling Verification

Identify front-end and back-end protocol transitions, connection reuse, HTTP versions, transfer framing, and normalization behavior. Use isolated requests and a harmless canary path.

Timeouts, generic gateway errors, or one malformed response are insufficient. Repeat with matching controls and avoid tests that could affect another user's connection.

Confirm only when a request boundary discrepancy causes a deterministic follow-up response shift, routing change, or canary consumption. Record raw framing, topology assumptions, timing, and both control and test exchanges.