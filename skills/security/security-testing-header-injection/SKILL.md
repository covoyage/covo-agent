---
name: security-testing-header-injection
description: Verify response or request header injection across normalization boundaries.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Header Injection Verification

Map user input entering redirects, cookies, cache headers, forwarding headers, mail headers, and proxy metadata. Compare raw wire behavior with framework-level representations.

Encoded newlines reflected in a body or safely normalized values are not proof. Check whether an independent header, response boundary, cache key, or downstream routing decision changes.

Confirm only with repeatable wire-level or downstream behavior. Record raw headers, intermediaries, normalization steps, affected boundary, and exchange ids.