---
name: security-testing-jwt
description: Verify token validation, claim enforcement, and signing-key boundary failures.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# JWT Verification

Document issuer, audience, algorithm, key identifier, expiry, subject, role, tenant, and token-use expectations. Compare a valid baseline with one controlled token change at a time.

Check signature enforcement, algorithm pinning, key selection, claim validation, token confusion between services, revocation, and authorization derived from mutable claims. Decoding a token or observing weak metadata is not proof.

Confirm only when a modified, expired, wrong-audience, wrong-issuer, or cross-context token is accepted beyond its intended authority. Save the accepted request and the rejected control without recording reusable secrets unnecessarily.