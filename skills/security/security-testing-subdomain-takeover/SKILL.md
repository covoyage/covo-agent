---
name: security-testing-subdomain-takeover
description: Verify dangling DNS ownership and safely claimable external resources.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Subdomain Takeover Verification

Resolve the full DNS chain and identify the external provider, resource state, account boundary, and current HTTP or TLS behavior. Check whether the target is intentionally parked or protected by provider-side domain verification.

A dangling CNAME or provider error page is not enough. Do not claim a production resource unless explicitly required and reversible; prefer provider documentation or a safe ownership preflight.

Confirm only when the referenced resource is demonstrably claimable by an unrelated account. Record DNS answers, provider fingerprint, claimability evidence, and affected hostname.