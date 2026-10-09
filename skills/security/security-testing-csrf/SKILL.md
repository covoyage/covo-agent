---
name: security-testing-csrf
description: Verify cross-site request forgery against meaningful authenticated state changes.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# CSRF Verification

Map cookie authentication, SameSite behavior, origin and referer checks, anti-CSRF tokens, content types, and CORS assumptions. Select an endpoint with a meaningful state change.

Reproduce from a cross-site context under realistic browser rules. A missing token field is not proof when cookies are not sent or the server rejects the origin.

Confirm only when an attacker-controlled origin can cause the victim's authenticated state change without an intended confirmation. Record browser context, cookie attributes, request, resulting state, and exchange ids.